// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// The ResolveCache is a transient cache that lives for the duration of a changeset execution. In a changeset
// multiple resources often resolve the same value. We do not want to do a read for each of these resolvables,
// therefore we cache these values.
//
// A resolve is answered from the cache when it can be, and otherwise reads the
// source through a PluginOperator. The operator owns retry: the first attempt
// comes back synchronously, and every later attempt is pushed here as a
// plugin.TrackedProgress from the operator's PID. A resolve waiting on such a
// read is parked in inFlight under that PID until the operator reports a
// finished result.
type ResolveCache struct {
	act.Actor

	cache    map[pkgmodel.FormaeURI]gjson.Result
	inFlight map[gen.PID]*resolveInFlight
}

// resolveInFlight is a resolve that has left the cache-hit fast path. It
// carries the requester and everything a read's completion needs, so the
// operator's pushed progress, which names only the native id, can be matched
// back to the resolve it answers.
type resolveInFlight struct {
	from        gen.PID
	resourceURI pkgmodel.FormaeURI
	loadResult  messages.LoadResourceResult

	// configRefs are the opaque references in the source target's config whose
	// live value has not been injected into config yet. At rest such a
	// credential is a bare $ref with no $value (reference-don't-store), so the
	// source resource cannot be read until each is resolved, and each resolves
	// like any other reference: from the cache, or by reading its source.
	configRefs []pkgmodel.FormaeURI
	config     json.RawMessage

	// reading is the resource whose read this resolve is parked on: the
	// source of a config ref, or the resolve's own source resource.
	reading pkgmodel.Resource
}

type Shutdown struct{}

func NewResolveCache() gen.ProcessBehavior {
	return &ResolveCache{}
}

func (r *ResolveCache) Init(args ...any) error {
	r.cache = make(map[pkgmodel.FormaeURI]gjson.Result)
	r.inFlight = make(map[gen.PID]*resolveInFlight)
	r.Log().Debug("ResolveCache actor initialized")
	return nil
}

func (r *ResolveCache) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case messages.ResolveValue:
		r.startResolve(from, msg.ResourceURI)
	case plugin.TrackedProgress:
		r.handleProgress(from, msg)
	case Shutdown:
		r.Log().Debug("ResolveCache received shutdown request")
		return gen.TerminateReasonNormal
	default:
		r.Log().Error("Received unknown message type=%v", reflect.TypeOf(msg))
	}
	return nil
}

// resolveMissReason builds a human-readable explanation for a terminal
// resolve miss — a referenced property that is absent from the source
// resource even after a successful Read. It names the reference and the
// missing property so the operator can act without log spelunking, and
// additionally identifies the source resource by triplet when it is known.
func resolveMissReason(resourceURI pkgmodel.FormaeURI, source *pkgmodel.Resource) string {
	property := resourceURI.PropertyPath()
	if source != nil && source.Label != "" {
		return fmt.Sprintf("could not resolve reference %q: source resource %q has no property %q",
			string(resourceURI), source.Stack+"/"+source.Type+"/"+source.Label, property)
	}
	return fmt.Sprintf("could not resolve reference %q: source resource has no property %q",
		string(resourceURI), property)
}

// rootDigestOf computes the canonical-domain root digest of a resolved value
// while its gjson type is still known: the wrapped/enveloped forms are
// unwrapped first, a string digests as the string it is, and everything else
// digests as its JSON form. The flattened Value string in the message is
// type-lossy and must never be re-digested downstream.
func rootDigestOf(value gjson.Result) string {
	unwrapped := provenance.UnwrapEffectiveValue(value)
	if unwrapped.Type == gjson.String {
		return provenance.DigestOfString(unwrapped.String())
	}
	return provenance.DigestOfJSON(unwrapped.Raw)
}

// startResolve handles a new ResolveValue request: answers from the cache when
// it can, otherwise loads the source resource from the persister and advances
// the resolve towards its first read.
func (r *ResolveCache) startResolve(from gen.PID, resourceURI pkgmodel.FormaeURI) {
	if props, ok := r.cache[resourceURI.Stripped()]; ok {
		r.Log().Debug("Cache hit for resource URI uri=%v", resourceURI)
		r.answer(from, resourceURI, props, nil)
		return
	}

	r.Log().Debug("Cache miss for resource URI uri=%v", resourceURI)
	loadResult, err := r.loadResource(resourceURI)
	if err != nil {
		r.Log().Error("Failed to load resource from resource persister resourceURI=%v: %v", resourceURI, err)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI,
			Reason: fmt.Sprintf("could not resolve reference %q: %v", string(resourceURI), err)})
		return
	}

	r.advance(&resolveInFlight{
		from:        from,
		resourceURI: resourceURI,
		loadResult:  loadResult,
		configRefs:  resolver.ExtractOpaqueResolvableURIsFromJSON(loadResult.Target.Config),
		config:      bytes.Clone(loadResult.Target.Config),
	})
}

// loadResource fetches a resource and its target from the persister.
func (r *ResolveCache) loadResource(uri pkgmodel.FormaeURI) (messages.LoadResourceResult, error) {
	result, err := messages.UnwrapCall(r.Call(
		gen.ProcessID{Name: actornames.ResourcePersister, Node: r.Node().Name()},
		messages.LoadResource{ResourceURI: uri.Stripped()}))
	if err != nil {
		return messages.LoadResourceResult{}, err
	}
	loadResult, ok := result.(messages.LoadResourceResult)
	if !ok {
		return messages.LoadResourceResult{}, fmt.Errorf("unexpected reply from the resource store: %T", result)
	}
	return loadResult, nil
}

// advance takes a resolve as far as the cache allows: it injects every config
// ref whose source is cached, then answers from the cache when the source
// resource is there. The first thing it cannot find in the cache it reads,
// parking the resolve until that read finishes; completing a read re-enters
// advance, so a resolve is a sequence of reads separated by waits on the
// operator, each one closer to the answer.
func (r *ResolveCache) advance(rf *resolveInFlight) {
	for len(rf.configRefs) > 0 {
		ref := rf.configRefs[0]
		props, ok := r.cache[ref.Stripped()]
		if !ok {
			source, err := r.loadResource(ref)
			if err != nil {
				r.Log().Error("Failed to load credential source for target config ref=%v target=%s: %v", ref, rf.loadResult.Target.Label, err)
				r.fail(rf, fmt.Sprintf("failed to resolve opaque reference %q for target %q: load error", ref, rf.loadResult.Target.Label))
				return
			}
			// The source of a credential is itself a managed resource whose own
			// target auth is a plain credential, not another opaque ref
			// (transitive-opaque is rejected at admission), so a metadata strip
			// is sufficient for its config.
			sourceConfig := source.Target.Config
			if plain, err := resolver.ConvertToPluginFormat(sourceConfig); err == nil {
				sourceConfig = plain
			}
			r.read(rf, source.Resource, sourceConfig)
			return
		}
		value := resolvedValueAt(props, ref.PropertyPath())
		if !value.Exists() {
			r.Log().Error("Credential source has no such property for target config ref=%v target=%s", ref, rf.loadResult.Target.Label)
			r.fail(rf, fmt.Sprintf("failed to resolve opaque reference %q for target %q: property %q absent from read result",
				ref, rf.loadResult.Target.Label, ref.PropertyPath()))
			return
		}
		config, err := resolver.ResolvePropertyReferences(ref, rf.config, provenance.UnwrapEffectiveValue(value).String())
		if err != nil {
			r.Log().Error("Failed to inject credential into target config ref=%v target=%s: %v", ref, rf.loadResult.Target.Label, err)
			r.fail(rf, fmt.Sprintf("failed to resolve opaque reference %q for target %q: inject error", ref, rf.loadResult.Target.Label))
			return
		}
		rf.config = config
		rf.configRefs = rf.configRefs[1:]
	}

	if props, ok := r.cache[rf.resourceURI.Stripped()]; ok {
		r.answer(rf.from, rf.resourceURI, props, &rf.loadResult.Resource)
		return
	}

	// Strip the $ref/$value/$visibility wrappers so the plugin receives plain
	// JSON. Fail closed on a conversion error (e.g. an irrecoverable $hashed
	// field) rather than hand the raw envelope to the plugin.
	config, err := resolver.ConvertToPluginFormat(rf.config)
	if err != nil {
		r.Log().Error("Failed to prepare target config for resolve-read resourceURI=%v target=%s: %v", rf.resourceURI, rf.loadResult.Target.Label, err)
		r.fail(rf, fmt.Sprintf("failed to prepare resolved config for target %q: convert error", rf.loadResult.Target.Label))
		return
	}
	r.read(rf, rf.loadResult.Resource, config)
}

// read starts a plugin Read of res on behalf of rf. A first attempt that has
// finished completes inline; otherwise the resolve is parked on the operator,
// whose later attempts arrive as TrackedProgress pushes.
func (r *ResolveCache) read(rf *resolveInFlight, res pkgmodel.Resource, config json.RawMessage) {
	rf.reading = res
	progress, operator, err := resource_update.ReadResourceViaPlugin(r, res, config)
	if err != nil {
		r.Log().Error("Failed to read resource via plugin resourceURI=%v: %v", res.URI(), err)
		r.fail(rf, fmt.Sprintf("could not read %q: %v", string(res.URI()), err))
		return
	}
	if !progress.HasFinished() {
		r.inFlight[operator] = rf
		return
	}
	r.completeRead(rf, progress)
}

// handleProgress consumes an attempt the operator pushed. Only a finished
// attempt moves the parked resolve on; an unfinished one means the operator is
// still retrying, and a push no resolve is waiting on is from an operator
// whose resolve already finished.
func (r *ResolveCache) handleProgress(operator gen.PID, progress plugin.TrackedProgress) {
	rf, ok := r.inFlight[operator]
	if !ok {
		r.Log().Debug("Ignoring progress from an operator no resolve is waiting on operator=%v nativeID=%s", operator, progress.NativeID)
		return
	}
	if !progress.HasFinished() {
		r.Log().Debug("Operator retrying read resourceURI=%v errorCode=%s attempt=%d/%d",
			rf.reading.URI(), progress.ErrorCode, progress.Attempts, progress.MaxAttempts)
		return
	}
	delete(r.inFlight, operator)
	r.completeRead(rf, &progress)
}

// completeRead caches a finished read's properties and advances the resolve, or
// fails it when the operator gave up.
func (r *ResolveCache) completeRead(rf *resolveInFlight, progress *plugin.TrackedProgress) {
	if !progress.FinishedSuccessfully() {
		r.Log().Error("ResolveCache: read failed errorCode=%s resourceURI=%v attempts=%d",
			progress.ErrorCode, rf.reading.URI(), progress.Attempts)
		r.fail(rf, fmt.Sprintf("could not read %q: %s after %d attempt(s)", string(rf.reading.URI()), progress.ErrorCode, progress.Attempts))
		return
	}
	parsed := gjson.ParseBytes([]byte(progress.ResourceProperties))
	r.cache[rf.reading.URI()] = r.preserveRefMetadata(rf.reading, parsed)
	r.Log().Debug("Cached resolved properties uri=%v", rf.reading.URI())
	r.advance(rf)
}

// answer sends the requested property out of the source's cached properties,
// or a terminal miss naming the property when it is absent. source, when
// known, identifies the resource by triplet in the miss reason.
func (r *ResolveCache) answer(from gen.PID, resourceURI pkgmodel.FormaeURI, props gjson.Result, source *pkgmodel.Resource) {
	value := resolvedValueAt(props, resourceURI.PropertyPath())
	if !value.Exists() {
		r.Log().Error("Unable to resolve property in cached properties property=%s resourceURI=%v", resourceURI.PropertyPath(), resourceURI)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI, Reason: resolveMissReason(resourceURI, source)})
		return
	}
	_ = r.Send(from, messages.ValueResolved{ResourceURI: resourceURI, Value: value.String(),
		SourceRootDigest: rootDigestOf(value)})
}

func (r *ResolveCache) fail(rf *resolveInFlight, reason string) {
	_ = r.Send(rf.from, messages.FailedToResolveValue{ResourceURI: rf.resourceURI, Reason: reason})
}

func (r *ResolveCache) preserveRefMetadata(originalResource pkgmodel.Resource, pluginResult gjson.Result) gjson.Result {
	schemaOpaqueFields := originalResource.Schema.Opaque()

	if !hasOpaqueValues(originalResource.Properties) && len(schemaOpaqueFields) == 0 {
		return pluginResult
	}

	opaqueFields := make(map[string]bool, len(schemaOpaqueFields))
	for _, f := range schemaOpaqueFields {
		opaqueFields[f] = true
	}

	originalProps := gjson.Parse(string(originalResource.Properties))

	pluginProps := make(map[string]any)
	if err := json.Unmarshal([]byte(pluginResult.Raw), &pluginProps); err != nil {
		r.Log().Error("Failed to unmarshal plugin result for metadata merging: %v", err)
		return pluginResult
	}

	modified := false
	for propName, propValue := range pluginProps {
		originalProp := originalProps.Get(propName)
		isOpaque := opaqueFields[propName] ||
			(originalProp.Exists() && originalProp.Get("$visibility").String() == "Opaque")
		if isOpaque {
			pluginProps[propName] = map[string]any{
				"$value":      propValue,
				"$visibility": "Opaque",
			}
			if strategy := originalProp.Get("$strategy").String(); strategy != "" {
				pluginProps[propName].(map[string]any)["$strategy"] = strategy
			}
			modified = true
		}
	}

	if !modified {
		return pluginResult
	}

	enhanced, err := json.Marshal(pluginProps)
	if err != nil {
		r.Log().Error("Failed to marshal enhanced properties: %v", err)
		return pluginResult
	}

	return gjson.Parse(string(enhanced))
}

func hasOpaqueValues(props json.RawMessage) bool {
	return bytes.Contains(props, []byte(`"$visibility"`)) &&
		bytes.Contains(props, []byte(`"Opaque"`))
}

// resolvedValueAt extracts the resolved value for propertyPath from cached
// plugin properties. preserveRefMetadata wraps an opaque field as
// {"$value": <fieldValue>, "$visibility": "Opaque"}, so a scalar secret whose
// path IS the field name resolves directly. A ref into a MAP-shaped opaque
// secret selects a key (e.g. "decodedData.username") that lives beneath the
// wrapper at "<field>.$value.<subpath>"; when the direct lookup misses, descend
// into the opaque parent's $value and re-wrap the leaf in the same envelope
// shape so downstream handling is identical for scalar and map secrets.
func resolvedValueAt(props gjson.Result, propertyPath string) gjson.Result {
	if v := props.Get(propertyPath); v.Exists() {
		return v
	}
	root, subpath, nested := strings.Cut(propertyPath, ".")
	if !nested {
		return gjson.Result{}
	}
	parent := props.Get(root)
	if parent.Get("$visibility").String() != pkgmodel.VisibilityOpaque {
		return gjson.Result{}
	}
	leaf := parent.Get("$value." + subpath)
	if !leaf.Exists() {
		return gjson.Result{}
	}
	wrapped, err := json.Marshal(map[string]any{
		"$value":      leaf.Value(),
		"$visibility": pkgmodel.VisibilityOpaque,
	})
	if err != nil {
		return gjson.Result{}
	}
	return gjson.ParseBytes(wrapped)
}
