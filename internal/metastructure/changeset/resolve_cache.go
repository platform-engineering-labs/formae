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
	compRes    []byte
	config     json.RawMessage
}

type Shutdown struct{}

func NewResolveCache() gen.ProcessBehavior {
	return &ResolveCache{}
}

func (r *ResolveCache) Init(args ...any) error {
	r.cache = make(map[pkgmodel.FormaeURI]gjson.Result)

	// Read retry config from node environment (set by metastructure).
	if cfg, ok := r.Env("RetryConfig"); ok {
		if retryCfg, ok := cfg.(pkgmodel.RetryConfig); ok {
			r.maxRetries = retryCfg.MaxRetries
			r.retryDelay = retryCfg.RetryDelay
		}
	}
	if r.maxRetries == 0 {
		r.maxRetries = 3
	}
	if r.retryDelay == 0 {
		r.retryDelay = 2 * time.Second
	}

	r.Log().Debug("ResolveCache actor initialized",
		"maxRetries", r.maxRetries, "retryDelay", r.retryDelay)

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
		r.Log().Error("Received unknown message type", "messageType", reflect.TypeOf(msg))
	}
	return nil
}

// startResolve handles a new ResolveValue request: checks the cache, loads from
// the persister if needed, and kicks off the first read attempt.
func (r *ResolveCache) startResolve(from gen.PID, resourceURI pkgmodel.FormaeURI) {
	// Check if the resource is already in the cache
	if json, ok := r.cache[resourceURI.Stripped()]; ok {
		r.Log().Debug("Cache hit for resource URI", "uri", resourceURI, "value", json)
		value := json.Get(resourceURI.PropertyPath())
		if !value.Exists() {
			r.Log().Error("Unable to resolve property in cached properties",
				"property", resourceURI.PropertyPath(), "resourceURI", resourceURI)
			_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
			return
		}
		_ = r.Send(from, messages.ValueResolved{ResourceURI: resourceURI, Value: value.String()})
		return
	}

	// Load the resource from the stack to get the native id
	r.Log().Debug("Cache miss for resource URI", "uri", resourceURI)
	stackerResult, err := r.Call(
		gen.ProcessID{Name: actornames.ResourcePersister, Node: r.Node().Name()},
		messages.LoadResource{
			ResourceURI: resourceURI.Stripped(),
		})
	if err != nil {
		r.Log().Error("Failed to load resource from resource persister", "resourceURI", resourceURI, "error", err)
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
		return
	}
	loadResourceResult, ok := stackerResult.(messages.LoadResourceResult)
	if !ok {
		r.Log().Error("Unexpected result type from resource persister", "resultType", reflect.TypeOf(stackerResult))
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
		return
	}

	// The persisted target config may contain resolvable metadata ($ref/$value
	// wrappers) that plugins cannot parse.  Strip these to produce clean JSON
	// the plugin can unmarshal.
	targetConfig := loadResourceResult.Target.Config
	if cleanConfig, err := resolver.ConvertToPluginFormat(targetConfig); err == nil {
		targetConfig = cleanConfig
	}

	compRes, err := plugin.CompressResource(loadResourceResult.Resource)
	if err != nil {
		r.Log().Error("Failed to compress resource", "resourceURI", resourceURI, "error", err)
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
		return
	}

	// Execute the first attempt inline (no delay).
	retry := resolveRetry{
		From:        from,
		ResourceURI: resourceURI,
		Attempt:     1,
		loadResult:  loadResourceResult,
		compRes:     compRes,
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
		r.Log().Error("Failed to read resource via plugin", "resourceURI", resourceURI, "error", err)
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
		return
	}

	// Retry on recoverable errors via SendAfter (non-blocking).
	if progress.OperationStatus == resource.OperationStatusFailure && resource.IsRecoverable(progress.ErrorCode) {
		if retry.Attempt < r.maxRetries {
			r.Log().Info("ResolveCache: recoverable error, retrying",
				"errorCode", progress.ErrorCode, "resourceURI", resourceURI,
				"attempt", retry.Attempt, "maxRetries", r.maxRetries)
			retry.Attempt++
			if _, err := r.SendAfter(r.PID(), retry, r.retryDelay); err != nil {
				r.Log().Error("Failed to schedule resolve retry", "error", err)
				_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
			}
			return
		}
		r.Log().Error("ResolveCache: exhausted retries",
			"errorCode", progress.ErrorCode, "resourceURI", resourceURI,
			"attempts", retry.Attempt)
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
		return
	}

	// Non-recoverable failure — do not cache, report immediately.
	if progress.OperationStatus == resource.OperationStatusFailure {
		r.Log().Error("ResolveCache: non-recoverable error reading resource",
			"errorCode", progress.ErrorCode, "resourceURI", resourceURI)
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
		return
	}

	// Success — cache and respond.
	parsed := gjson.ParseBytes([]byte(progress.ResourceProperties))
	enhancedParsed := r.preserveRefMetadata(retry.loadResult.Resource, parsed)

	r.cache[resourceURI.Stripped()] = enhancedParsed
	r.Log().Debug("Cached resolved properties", "uri", resourceURI, "value", enhancedParsed)
	value := enhancedParsed.Get(resourceURI.PropertyPath())
	if !value.Exists() {
		r.Log().Error("Unable to resolve property in cached properties",
			"property", resourceURI.PropertyPath(), "resourceURI", resourceURI)
		_ = r.Send(from, messages.FailedToResolveValue(messages.ResolveValue{ResourceURI: resourceURI}))
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

	progressResult, err := r.Call(
		spawnRes.PID,
		plugin.ReadResource{
			Namespace:         retry.loadResult.Resource.Namespace(),
			ResourceType:      retry.loadResult.Resource.Type,
			ResourceNamespace: retry.loadResult.Resource.Namespace(),
			ExistingResource:  retry.compRes,
			Resource:          retry.compRes,
			NativeID:          retry.loadResult.Resource.NativeID,
			TargetConfig:      retry.config,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}

	progress, ok := progressResult.(plugin.TrackedProgress)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from plugin operator: %T", progressResult)
	}
	// Decompress resource properties if sent compressed over Ergo (64KB limit)
	if len(progress.CompressedResourceProperties) > 0 && len(progress.ResourceProperties) == 0 {
		decompressed, err := plugin.DecompressJSON(progress.CompressedResourceProperties)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress resource properties: %w", err)
		}
		progress.ResourceProperties = decompressed
	}
	return &progress, nil
}

func (r *ResolveCache) preserveRefMetadata(originalResource pkgmodel.Resource, pluginResult gjson.Result) gjson.Result {
	if !hasOpaqueValues(originalResource.Properties) {
		return pluginResult
	}

	originalProps := gjson.Parse(string(originalResource.Properties))

	pluginProps := make(map[string]any)
	if err := json.Unmarshal([]byte(pluginResult.Raw), &pluginProps); err != nil {
		r.Log().Error("Failed to unmarshal plugin result for metadata merging", "error", err)
		return pluginResult
	}

	modified := false
	for propName, propValue := range pluginProps {
		originalProp := originalProps.Get(propName)
		if originalProp.Exists() && originalProp.Get("$visibility").String() == "Opaque" {
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
		r.Log().Error("Failed to marshal enhanced properties", "error", err)
		return pluginResult
	}

	return gjson.Parse(string(enhanced))
}

func hasOpaqueValues(props json.RawMessage) bool {
	return bytes.Contains(props, []byte(`"$visibility"`)) &&
		bytes.Contains(props, []byte(`"Opaque"`))
}
