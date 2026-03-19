// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Thre ResolveCache is a transient cache that lives for the duration of a changeset execution. In a changeset
// multiple resources often resolve the same value. We do not want to do a read for each of these resolvables,
// therefore we cache these values.
type ResolveCache struct {
	act.Actor

	cache   map[pkgmodel.FormaeURI]gjson.Result
	pending map[string]*pendingResolve
}

type pendingResolve struct {
	resource     pkgmodel.Resource
	targetConfig json.RawMessage
	requesters   []pendingResolveRequester
}

type pendingResolveRequester struct {
	from        gen.PID
	resourceURI pkgmodel.FormaeURI
}

type Shutdown struct{}

func NewResolveCache() gen.ProcessBehavior {
	return &ResolveCache{}
}

func (r *ResolveCache) Init(args ...any) error {
	r.cache = make(map[pkgmodel.FormaeURI]gjson.Result)
	r.pending = make(map[string]*pendingResolve)

	r.Log().Debug("ResolveCache actor initialized")

	return nil
}

func (r *ResolveCache) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case messages.ResolveValue:
		return r.handleResolveValue(from, msg)
	case plugin.TrackedProgress:
		return r.handleTrackedProgress(msg)
	case Shutdown:
		r.Log().Debug("ResolveCache received shutdown request")
		return gen.TerminateReasonNormal
	default:
		r.Log().Error("Received unknown message type", "messageType", reflect.TypeOf(msg))
	}
	return nil
}

func (r *ResolveCache) handleResolveValue(from gen.PID, msg messages.ResolveValue) error {
	if value, err := r.resolveCachedValue(msg.ResourceURI); err == nil {
		return r.Send(from, messages.ValueResolved{ResourceURI: msg.ResourceURI, Value: value})
	}

	resourceState, targetConfig, err := r.loadResource(msg.ResourceURI)
	if err != nil {
		return r.Send(from, messages.FailedToResolveValue(msg))
	}

	if pending := r.pending[resourceState.NativeID]; pending != nil {
		pending.requesters = append(pending.requesters, pendingResolveRequester{from: from, resourceURI: msg.ResourceURI})
		return nil
	}

	progress, err := r.startRead(resourceState, targetConfig, msg.ResourceURI)
	if err != nil {
		return r.Send(from, messages.FailedToResolveValue(msg))
	}
	requesters := []pendingResolveRequester{{from: from, resourceURI: msg.ResourceURI}}
	if progress.HasFinished() {
		return r.finishPendingRead(resourceState, progress, requesters)
	}
	if progress.NativeID == "" {
		progress.NativeID = resourceState.NativeID
	}
	r.pending[resourceState.NativeID] = &pendingResolve{resource: resourceState, targetConfig: targetConfig, requesters: requesters}
	return nil
}

func (r *ResolveCache) handleTrackedProgress(progress plugin.TrackedProgress) error {
	pending := r.pending[progress.NativeID]
	if pending == nil || !progress.HasFinished() {
		return nil
	}
	delete(r.pending, progress.NativeID)
	return r.finishPendingRead(pending.resource, progress, pending.requesters)
}

func (r *ResolveCache) finishPendingRead(resourceState pkgmodel.Resource, progress plugin.TrackedProgress, requesters []pendingResolveRequester) error {
	if progress.Failed() {
		for _, requester := range requesters {
			if err := r.Send(requester.from, messages.FailedToResolveValue{ResourceURI: requester.resourceURI}); err != nil {
				return err
			}
		}
		return nil
	}

	parsed := gjson.ParseBytes([]byte(progress.ResourceProperties))
	enhancedParsed := r.preserveRefMetadata(resourceState, parsed)
	r.cache[resourceState.URI()] = enhancedParsed

	for _, requester := range requesters {
		value := enhancedParsed.Get(requester.resourceURI.PropertyPath())
		if !value.Exists() {
			if err := r.Send(requester.from, messages.FailedToResolveValue{ResourceURI: requester.resourceURI}); err != nil {
				return err
			}
			continue
		}
		if err := r.Send(requester.from, messages.ValueResolved{ResourceURI: requester.resourceURI, Value: value.String()}); err != nil {
			return err
		}
	}
	return nil
}

func (r *ResolveCache) resolveCachedValue(resourceURI pkgmodel.FormaeURI) (string, error) {
	// Check if the resource is already in the cache
	if json, ok := r.cache[resourceURI.Stripped()]; ok {
		r.Log().Debug("Cache hit for resource URI", "uri", resourceURI, "value", json)
		value := json.Get(resourceURI.PropertyPath())
		if !value.Exists() {
			r.Log().Error("Unable to resolve property %s in cached properties for resource %s", resourceURI.PropertyPath(), resourceURI)
			return "", fmt.Errorf("property %s not found in cached properties for resource %s", resourceURI.PropertyPath(), resourceURI)
		}
		return value.String(), nil
	}
	return "", fmt.Errorf("cache miss")
}

func (r *ResolveCache) loadResource(resourceURI pkgmodel.FormaeURI) (pkgmodel.Resource, json.RawMessage, error) {
	r.Log().Debug("Cache miss for resource URI", "uri", resourceURI)
	stackerResult, err := r.Call(
		gen.ProcessID{Name: actornames.ResourcePersister, Node: r.Node().Name()},
		messages.LoadResource{
			ResourceURI: resourceURI.Stripped(),
		})
	if err != nil {
		r.Log().Error("Failed to load resource from resource persister", "resourceURI", resourceURI, "error", err)
		return pkgmodel.Resource{}, nil, fmt.Errorf("failed to load resource from resource persister: %w", err)
	}
	loadResourceResult, ok := stackerResult.(messages.LoadResourceResult)
	if !ok {
		r.Log().Error("Unexpected result type from resource persister", "resultType", reflect.TypeOf(stackerResult))
		return pkgmodel.Resource{}, nil, fmt.Errorf("unexpected result type from resource persister: %T", stackerResult)
	}
	if !loadResourceResult.Found {
		return pkgmodel.Resource{}, nil, fmt.Errorf("resource %s not found", resourceURI.KSUID())
	}
	return loadResourceResult.Resource, loadResourceResult.Target.Config, nil
}

func (r *ResolveCache) startRead(resourceState pkgmodel.Resource, targetConfig json.RawMessage, resourceURI pkgmodel.FormaeURI) (plugin.TrackedProgress, error) {
	// Spawn the plugin operator via PluginCoordinator
	operationID := uuid.New().String()
	spawnResult, err := r.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: r.Node().Name()},
		messages.SpawnPluginOperator{
			Namespace:   resourceState.Namespace(),
			ResourceURI: string(resourceURI.Stripped()),
			Operation:   string(resource.OperationRead),
			OperationID: operationID,
			RequestedBy: r.PID(),
		})
	if err != nil {
		r.Log().Error("Failed to spawn plugin operator for resource", "resourceURI", resourceURI, "error", err)
		return plugin.TrackedProgress{}, fmt.Errorf("failed to spawn plugin operator for resource: %w", err)
	}
	spawnRes, ok := spawnResult.(messages.SpawnPluginOperatorResult)
	if !ok {
		r.Log().Error("Unexpected result type from PluginCoordinator", "resultType", reflect.TypeOf(spawnResult))
		return plugin.TrackedProgress{}, fmt.Errorf("unexpected result type from PluginCoordinator: %T", spawnResult)
	}
	if spawnRes.Error != "" {
		r.Log().Error("Failed to spawn plugin operator", "error", spawnRes.Error)
		return plugin.TrackedProgress{}, fmt.Errorf("failed to spawn plugin operator: %s", spawnRes.Error)
	}

	progressResult, err := r.Call(
		spawnRes.PID,
		plugin.ReadResource{
			Namespace:        resourceState.Namespace(),
			ExistingResource: resourceState,
			Resource:         resourceState,
			NativeID:         resourceState.NativeID,
			TargetConfig:     targetConfig,
		})

	if err != nil {
		r.Log().Error("Failed to read resource", "resourceURI", resourceURI, "error", err)
		return plugin.TrackedProgress{}, fmt.Errorf("failed to read resource: %w", err)
	}
	progress, ok := progressResult.(plugin.TrackedProgress)
	if !ok {
		r.Log().Error("Unexpected result type from plugin operator", "resultType", reflect.TypeOf(progressResult))
		return plugin.TrackedProgress{}, fmt.Errorf("unexpected result type from plugin operator: %T", progressResult)
	}
	if progress.NativeID == "" {
		progress.NativeID = resourceState.NativeID
	}
	return progress, nil
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
