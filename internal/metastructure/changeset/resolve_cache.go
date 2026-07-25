// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// The ResolveCache is a transient cache that lives for the duration of a changeset execution. In a changeset
// multiple resources often resolve the same value. We do not want to do a read for each of these resolvables,
// therefore we cache these values.
type ResolveCache struct {
	act.Actor

	cache      map[pkgmodel.FormaeURI]gjson.Result
	maxRetries int
	retryDelay time.Duration
}

// resolveRetry is an internal message scheduled via SendAfter to retry a
// resolve operation without blocking the actor's message loop.
type resolveRetry struct {
	From        gen.PID
	ResourceURI pkgmodel.FormaeURI
	Attempt     int
	// Pre-loaded state from the first attempt so we don't re-fetch from persister.
	loadResult messages.LoadResourceResult
	config     json.RawMessage
}

type Shutdown struct{}

func NewResolveCache() gen.ProcessBehavior {
	return &ResolveCache{}
}

func (r *ResolveCache) Init(args ...any) error {
	r.cache = make(map[pkgmodel.FormaeURI]gjson.Result)

	cfg, ok := r.Env("RetryConfig")
	if !ok {
		return fmt.Errorf("resolveCache: missing 'RetryConfig' environment variable")
	}
	retryCfg, ok := cfg.(pkgmodel.RetryConfig)
	if !ok {
		return fmt.Errorf("resolveCache: 'RetryConfig' environment variable has wrong type %T", cfg)
	}
	r.maxRetries = retryCfg.MaxRetries
	r.retryDelay = retryCfg.RetryDelay

	r.Log().Debug("ResolveCache actor initialized maxRetries=%d retryDelay=%s", r.maxRetries, r.retryDelay)

	return nil
}

func (r *ResolveCache) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case messages.ResolveValue:
		r.startResolve(from, msg.ResourceURI)
	case resolveRetry:
		r.continueResolve(msg)
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

// startResolve handles a new ResolveValue request: checks the cache, loads from
// the persister if needed, and kicks off the first read attempt.
func (r *ResolveCache) startResolve(from gen.PID, resourceURI pkgmodel.FormaeURI) {
	// Check if the resource is already in the cache
	if json, ok := r.cache[resourceURI.Stripped()]; ok {
		r.Log().Debug("Cache hit for resource URI uri=%v", resourceURI)
		value := json.Get(resourceURI.PropertyPath())
		if !value.Exists() {
			r.Log().Error("Unable to resolve property in cached properties property=%s resourceURI=%v", resourceURI.PropertyPath(), resourceURI)
			_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI, Reason: resolveMissReason(resourceURI, nil)})
			return
		}
		_ = r.Send(from, messages.ValueResolved{ResourceURI: resourceURI, Value: value.String()})
		return
	}

	// Load the resource from the stack to get the native id
	r.Log().Debug("Cache miss for resource URI uri=%v", resourceURI)
	stackerResult, err := r.Call(
		gen.ProcessID{Name: actornames.ResourcePersister, Node: r.Node().Name()},
		messages.LoadResource{
			ResourceURI: resourceURI.Stripped(),
		})
	if err != nil {
		r.Log().Error("Failed to load resource from resource persister resourceURI=%v: %v", resourceURI, err)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI})
		return
	}
	loadResourceResult, ok := stackerResult.(messages.LoadResourceResult)
	if !ok {
		r.Log().Error("Unexpected result type from resource persister resultType=%v", reflect.TypeOf(stackerResult))
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI})
		return
	}

	// The persisted target config may contain resolvable metadata ($ref/$value
	// wrappers) that plugins cannot parse.  Strip these to produce clean JSON
	// the plugin can unmarshal.
	targetConfig := loadResourceResult.Target.Config
	if cleanConfig, err := resolver.ConvertToPluginFormat(targetConfig); err == nil {
		targetConfig = cleanConfig
	}

	// Execute the first attempt inline (no delay).
	retry := resolveRetry{
		From:        from,
		ResourceURI: resourceURI,
		Attempt:     1,
		loadResult:  loadResourceResult,
		config:      targetConfig,
	}
	r.continueResolve(retry)
}

// continueResolve executes a single read attempt and either resolves, schedules
// a retry via SendAfter, or sends a failure back to the original requester.
func (r *ResolveCache) continueResolve(retry resolveRetry) {
	resourceURI := retry.ResourceURI
	from := retry.From

	progress, err := r.readViaPlugin(retry)
	if err != nil {
		r.Log().Error("Failed to read resource via plugin resourceURI=%v: %v", resourceURI, err)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI})
		return
	}

	// Retry on recoverable errors via SendAfter (non-blocking).
	if progress.OperationStatus == resource.OperationStatusFailure && resource.IsRecoverable(progress.ErrorCode) {
		if retry.Attempt < r.maxRetries {
			r.Log().Info("ResolveCache: recoverable error, retrying errorCode=%s resourceURI=%v attempt=%d maxRetries=%d",
				progress.ErrorCode, resourceURI, retry.Attempt, r.maxRetries)
			retry.Attempt++
			if _, err := r.SendAfter(r.PID(), retry, r.retryDelay); err != nil {
				r.Log().Error("Failed to schedule resolve retry: %v", err)
				_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI})
			}
			return
		}
		r.Log().Error("ResolveCache: exhausted retries errorCode=%s resourceURI=%v attempts=%d",
			progress.ErrorCode, resourceURI, retry.Attempt)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI})
		return
	}

	// Non-recoverable failure — do not cache, report immediately.
	if progress.OperationStatus == resource.OperationStatusFailure {
		r.Log().Error("ResolveCache: non-recoverable error reading resource errorCode=%s resourceURI=%v",
			progress.ErrorCode, resourceURI)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI})
		return
	}

	// Success — cache and respond.
	parsed := gjson.ParseBytes([]byte(progress.ResourceProperties))
	enhancedParsed := r.preserveRefMetadata(retry.loadResult.Resource, parsed)

	r.cache[resourceURI.Stripped()] = enhancedParsed
	r.Log().Debug("Cached resolved properties uri=%v", resourceURI)
	value := enhancedParsed.Get(resourceURI.PropertyPath())
	if !value.Exists() {
		r.Log().Error("Unable to resolve property in cached properties property=%s resourceURI=%v", resourceURI.PropertyPath(), resourceURI)
		_ = r.Send(from, messages.FailedToResolveValue{ResourceURI: resourceURI, Reason: resolveMissReason(resourceURI, &retry.loadResult.Resource)})
		return
	}

	_ = r.Send(from, messages.ValueResolved{ResourceURI: resourceURI, Value: value.String()})
}

// readViaPlugin spawns a PluginOperator and executes a single Read call.
func (r *ResolveCache) readViaPlugin(retry resolveRetry) (*plugin.TrackedProgress, error) {
	operationID := uuid.New().String()
	spawnResult, err := r.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: r.Node().Name()},
		messages.SpawnPluginOperator{
			Namespace:   retry.loadResult.Resource.Namespace(),
			ResourceURI: string(retry.ResourceURI.Stripped()),
			Operation:   string(resource.OperationRead),
			OperationID: operationID,
			RequestedBy: r.PID(),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to spawn plugin operator: %w", err)
	}
	spawnRes, ok := spawnResult.(messages.SpawnPluginOperatorResult)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from PluginCoordinator: %T", spawnResult)
	}
	if spawnRes.Error != "" {
		return nil, fmt.Errorf("failed to spawn plugin operator: %s", spawnRes.Error)
	}

	// Use the same call budget as ResourceUpdater.doPluginOperation. The default
	// Ergo Call timeout (5s) is too short for live AWS API reads, which routinely
	// run longer than that — especially CloudControl GetResource immediately
	// after a Create, when SDK credential resolution and the read itself stack up.
	progressResult, err := r.CallWithTimeout(
		spawnRes.PID,
		plugin.ReadResource{
			Namespace:         retry.loadResult.Resource.Namespace(),
			ResourceType:      retry.loadResult.Resource.Type,
			ResourceNamespace: retry.loadResult.Resource.Namespace(),
			ExistingResource:  retry.loadResult.Resource,
			Resource:          retry.loadResult.Resource,
			NativeID:          retry.loadResult.Resource.NativeID,
			TargetConfig:      retry.config,
		},
		resource_update.PluginOperationCallTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}

	progress, ok := progressResult.(plugin.TrackedProgress)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from plugin operator: %T", progressResult)
	}
	return &progress, nil
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
