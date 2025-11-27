// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// convertResourceForPlugin converts a resource's properties to plugin format
// by extracting $value from opaque value structures (e.g., {"$value": "secret", "$visibility": "Opaque"})
// becomes just "secret". This must be done before sending to the plugin since the resolver
// lives in the agent and plugins may be remote.
func convertResourceForPlugin(res pkgmodel.Resource) (pkgmodel.Resource, error) {
	if res.Properties == nil {
		return res, nil
	}

	convertedProps, err := resolver.ConvertToPluginFormat(res.Properties)
	if err != nil {
		return res, err
	}

	// Return a copy with converted properties
	result := res
	result.Properties = convertedProps
	return result, nil
}

// ResourceUpdater is the Ergo state machine responsible for executing resource updates in the metastructure.
// A resource update is a sequence of plugin operations that are applied to a resource in the cloud. Different
// resource updates go through different states depending on the type of operation being performed. The supported
// operation types are Create, Update, Delete, and Synchronize. Before every Delete and Update operation, the
// affected resource is synchronized to ensure that the latest state of the resource in the cloud is known. IF
// we detect that the resource state in the cloud is different from the state in the metastructure (stack), we
// reject the resource update.
//
// The state transitions are as follows:
//
//	                   +-----------------------+                 +-----------------------+
//	                   |     Initializing      | --------------> |     Synchronizing     |----
//	                   +-----------------------+                 +-----------------------+    |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               v                                   |     |                |
//	                   +-----------------------+                       |     |                |
//	  -----------------|       Resolving       |<----------------------      |                |
//	|                  +-----------------------+                             |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        v           v                                   v                |
//	|    +-----------------------+   +-----------------------+   +-----------------------+    |
//	|    |       Creating        |   |       Updating        |   |       Deleting        |    |
//	|    +-----------------------+   +-----------------------+   +-----------------------+    |
//	|                   |  |               |           |                           |  |       |
//	|                   |  |               |           |                           |  |       |
//	|                   |  |        -------             ----------------------     |  |       |
//	|                   |   --------------------------------------------      |    |  |       |
//	|                   |          |     ---------------------------------------------        |
//	|                   v          v    v                               v     v    v          |
//	|                  +----------------------+                   +----------------------+    |
//	|                  | FinishedSuccessFully |                   |  FinishedWithErrors  |<---
//	|                  +----------------------+                   +----------------------+
//	|                                                                         ^
//	|                                                                         |
//	 -------------------------------------------------------------------------

type ResourceUpdater struct {
	statemachine.StateMachine[ResourceUpdateData]
}

func newResourceUpdater() gen.ProcessBehavior {
	return &ResourceUpdater{}
}

type ResourceUpdateFinished struct {
	Uri   pkgmodel.FormaeURI
	State ResourceUpdateState
}

type StartResourceUpdate struct {
	ResourceUpdate ResourceUpdate
	CommandID      string
	UpdateId       string
}

type PluginOperatorMissingInAction struct{}

type ResolveCacheMissingInAction struct{}

type Shutdown struct{}

// PersistResourceUpdate is sent to the ResourcePersister actor to store a resource update
// in the datastore after a successful plugin operation.
type PersistResourceUpdate struct {
	CommandID         string
	ResourceOperation OperationType
	PluginOperation   resource.Operation
	ResourceUpdate    ResourceUpdate
}

const (
	StateInitializing         = gen.Atom("initializing")
	StateSynchronizing        = gen.Atom("synchronizing")
	StateResolving            = gen.Atom("resolving")
	StateDeleting             = gen.Atom("deleting")
	StateCreating             = gen.Atom("creating")
	StateUpdating             = gen.Atom("updating")
	StateExiting              = gen.Atom("exiting")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
	StateRejected             = gen.Atom("rejected")
)

const PluginOperationCallTimeout = 60 // seconds

type ResourceUpdateData struct {
	resourceUpdate  *ResourceUpdate
	commandID       string
	labelTagKeys    []string
	resourceLabeler *ResourceLabeler
	retryConfig     pkgmodel.RetryConfig
	requestedBy     gen.PID
	commandSource   FormaCommandSource

	// Because the discovery process can alter the resource URI (by changing the resource label), we need to keep
	// track of the original resource URI for notifying both the forma_command_persister as well as the changeset
	// executor.
	// Once we switch out the URI for a stable KSUID we can remove this field.
	originalResourceKsuidURI pkgmodel.FormaeURI
}

func (r *ResourceUpdater) Init(args ...any) (statemachine.StateMachineSpec[ResourceUpdateData], error) {
	data := ResourceUpdateData{
		requestedBy: args[0].(gen.PID),
	}
	initialState := StateInitializing
	if len(args) > 1 {
		initialState = args[1].(gen.Atom)
	}

	pluginCfg, ok := r.Env("RetryConfig")
	if !ok {
		r.Log().Error("ResourceUpdater: missing 'RetryConfig' environment variable")
		return statemachine.StateMachineSpec[ResourceUpdateData]{}, fmt.Errorf("resourceUpdater: missing 'RetryConfig' environment variable")
	}
	data.retryConfig = pluginCfg.(pkgmodel.RetryConfig)

	discoveryCfg, ok := r.Env("DiscoveryConfig")
	if !ok {
		r.Log().Error("ResourceUpdater: missing 'DiscoveryConfig' environment variable")
		return statemachine.StateMachineSpec[ResourceUpdateData]{}, fmt.Errorf("resourceUpdater: missing 'DiscoveryConfig' environment variable")
	}
	data.labelTagKeys = discoveryCfg.(pkgmodel.DiscoveryConfig).LabelTagKeys

	ds, ok := r.Env("Datastore")
	if !ok {
		r.Log().Error("ResourceUpdater: missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[ResourceUpdateData]{}, fmt.Errorf("resourceUpdater: missing 'Datastore' environment variable")
	}
	data.resourceLabeler = NewResourceLabeler(ds.(ResourceDataLookup))

	r.Log().Debug("ResourceUpdater %s initialized", r.Name())

	return statemachine.NewStateMachineSpec(initialState,

		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateMessageHandler(StateInitializing, start),
		statemachine.WithStateMessageHandler(StateDeleting, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateDeleting, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateDeleting, shutdown),
		statemachine.WithStateMessageHandler(StateCreating, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateCreating, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateCreating, shutdown),
		statemachine.WithStateMessageHandler(StateUpdating, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateUpdating, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateUpdating, shutdown),
		statemachine.WithStateMessageHandler(StateResolving, resourceResolved),
		statemachine.WithStateMessageHandler(StateResolving, resolveCacheMissingInAction),
		statemachine.WithStateMessageHandler(StateResolving, resourceFailedToResolve),
		statemachine.WithStateMessageHandler(StateResolving, shutdown),
		statemachine.WithStateMessageHandler(StateSynchronizing, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdown),
		statemachine.WithStateMessageHandler(StateRejected, shutdown),
	), nil
}

// Returns the address (ProcessID) of the global resource persister.
func resourcePersisterProcess(proc gen.Process) gen.ProcessID {
	return gen.ProcessID{
		Name: actornames.ResourcePersister,
		Node: proc.Node().Name(),
	}
}

// Returns the address (ProcessID) of the global forma command persister.
func formaCommandPersisterProcess(proc gen.Process) gen.ProcessID {
	return gen.ProcessID{
		Name: gen.Atom("FormaCommandPersister"),
		Node: proc.Node().Name(),
	}
}

func shutdown(state gen.Atom, data ResourceUpdateData, shutdown Shutdown, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, error) {
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError || newState == StateRejected {
		proc.Log().Debug("ResourceUpdater: sending completion message to forma command persister", "state", newState, "commandID", data.commandID)
		_, err := proc.Call(
			formaCommandPersisterProcess(proc),
			messages.MarkResourceUpdateAsComplete{
				CommandID:                  data.commandID,
				ResourceURI:                data.originalResourceKsuidURI,
				FinalState:                 data.resourceUpdate.State,
				ResourceStartTs:            data.resourceUpdate.StartTs,
				ResourceModifiedTs:         data.resourceUpdate.ModifiedTs,
				ResourceProperties:         data.resourceUpdate.Resource.Properties,
				ResourceReadOnlyProperties: data.resourceUpdate.Resource.ReadOnlyProperties,
				Version:                    data.resourceUpdate.Version,
			},
		)
		if err != nil {
			proc.Log().Error("Failed to send MarkAsComplete message to forma command persister", "error", err)
		}

		// Send a ResourceUpdateFinished message to the requester to inform it about the final state of the resource update.
		proc.Log().Debug("ResourceUpdater: sending ResourceUpdateFinished message to requester", "state", newState, "uri", data.originalResourceKsuidURI)
		err = proc.Send(
			data.requestedBy,
			ResourceUpdateFinished{
				Uri:   data.originalResourceKsuidURI,
				State: data.resourceUpdate.State,
			})
		if err != nil {
			proc.Log().Error("failed to send ResourceUpdateFinished message to requester", "error", err)
		}

		// Send ourselves a shutdown message to terminate the process.
		proc.Log().Debug("ResourceUpdater: sending shutdown message to self", "state", newState)
		err = proc.Send(proc.PID(), Shutdown{})
		if err != nil {
			proc.Log().Error("ResourceUpdater: failed to send terminate message: %v", err)
		}
	}
	return newState, data, nil
}

func start(state gen.Atom, data ResourceUpdateData, message StartResourceUpdate, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.resourceUpdate = &message.ResourceUpdate
	data.commandID = message.CommandID
	data.commandSource = message.ResourceUpdate.Source
	data.resourceUpdate.StartTs = util.TimeNow()
	data.resourceUpdate.ModifiedTs = data.resourceUpdate.StartTs
	data.originalResourceKsuidURI = data.resourceUpdate.Resource.URI()

	return nextState(state, data, proc)
}

func synchronize(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	// Convert properties to plugin format (extracts $value from opaque structures)
	convertedResource, err := convertResourceForPlugin(data.resourceUpdate.Resource)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	convertedExisting, err := convertResourceForPlugin(data.resourceUpdate.ExistingResource)
	if err != nil {
		proc.Log().Error("failed to convert existing resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	progress, err := doPluginOperation(data.resourceUpdate.Resource.URI(), plugin.ReadResource{
		Namespace:        convertedExisting.Namespace(),
		ExistingResource: convertedExisting,
		Resource:         convertedResource,
		Target:           data.resourceUpdate.ResourceTarget,
		NativeID:         data.resourceUpdate.Resource.NativeID,
		IsSync:           data.resourceUpdate.IsSync(),
		IsDelete:         data.resourceUpdate.IsDelete(),

		// If the source is discovery, remove sensitive information from the resource update.
		RedactSensitive: data.commandSource == FormaCommandSourceDiscovery,
	}, proc)
	if err != nil {
		proc.Log().Error("failed to synchronize resource", "error", err, "resourceURI", data.resourceUpdate.Resource.URI())
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	return handleProgressUpdate(state, data, *progress, proc)
}

func delete(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	// Convert properties to plugin format (extracts $value from opaque structures)
	convertedResource, err := convertResourceForPlugin(data.resourceUpdate.Resource)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	convertedExisting, err := convertResourceForPlugin(data.resourceUpdate.ExistingResource)
	if err != nil {
		proc.Log().Error("failed to convert existing resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	metadata, err := convertedExisting.GetMetadata()
	if err != nil {
		proc.Log().Error("failed to get metadata for resource update: %v, resourceURI: %s", err, data.resourceUpdate.Resource.URI())
		metadata = json.RawMessage("{}")
	}
	deleteOperation := plugin.DeleteResource{
		Namespace:    convertedResource.Namespace(),
		NativeID:     convertedResource.NativeID,
		Resource:     convertedResource,
		ResourceType: convertedResource.Type,
		Metadata:     metadata,
		Target:       data.resourceUpdate.ResourceTarget,
	}

	// First we check if progress already was made on the delete operation. This can happen for example if the node crashed while the
	// delete operation was in progress. If so, we try to recover from the previous progress.
	if found, lastKnownProgress := data.resourceUpdate.FindProgress(resource.OperationDelete); found {
		return recoverFromPreviousProgress(StateDeleting, data, lastKnownProgress, deleteOperation, proc)
	}

	// If no progress was made yet, we start a new delete operation.
	result, err := doPluginOperation(
		data.resourceUpdate.Resource.URI(),
		deleteOperation,
		proc)
	if err != nil {
		proc.Log().Error("failed to start delete operation", "error", err, "resourceURI", data.resourceUpdate.Resource.URI())
		return StateDeleting, data, nil, nil
	}

	return handleProgressUpdate(state, data, *result, proc)
}

func resolve(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	if len(data.resourceUpdate.RemainingResolvables) == 0 {
		return nextState(state, data, proc)
	}

	first := data.resourceUpdate.RemainingResolvables[0]
	data.resourceUpdate.RemainingResolvables = data.resourceUpdate.RemainingResolvables[1:]

	err := proc.Send(
		gen.ProcessID{
			Node: proc.Node().Name(),
			Name: actornames.ResolveCache(data.commandID),
		},
		messages.ResolveValue{
			ResourceURI: first,
		})
	if err != nil {
		return StateResolving, data, nil, fmt.Errorf("failed to send ResolveValue message to resolve cache: %w", err)
	}

	timeout := statemachine.StateTimeout{
		Duration: data.retryConfig.StatusCheckInterval * 2,
		Message:  ResolveCacheMissingInAction{},
	}

	return StateResolving, data, []statemachine.Action{timeout}, nil
}

func resourceResolved(state gen.Atom, data ResourceUpdateData, message messages.ValueResolved, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	err := data.resourceUpdate.ResolveValue(message.ResourceURI, message.Value)
	if err != nil {
		proc.Log().Error("failed to resolve value for resource update", "error", err, "resourceURI", message.ResourceURI)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	return resolve(state, data, proc)
}

func create(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	// Convert properties to plugin format (extracts $value from opaque structures)
	convertedResource, err := convertResourceForPlugin(data.resourceUpdate.Resource)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	createOperation := plugin.CreateResource{
		Namespace: convertedResource.Namespace(),
		Resource:  convertedResource,
		Target:    data.resourceUpdate.ResourceTarget,
	}

	// First we check if progress already was made on the create operation. This can happen for example if the node crashed while the
	// create operation was in progress. If so, we try to recover from the previous progress.
	if found, lastKnownProgress := data.resourceUpdate.FindProgress(resource.OperationCreate); found {
		return recoverFromPreviousProgress(StateCreating, data, lastKnownProgress, createOperation, proc)
	}

	result, err := doPluginOperation(
		data.resourceUpdate.Resource.URI(),
		createOperation,
		proc)
	if err != nil {
		proc.Log().Error("failed to start create operation", "error", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	return handleProgressUpdate(state, data, *result, proc)
}

func update(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	// Special case: bringing tag-less resources under management without property changes
	// For resources that don't support tags (e.g., AWS::EC2::VPCGatewayAttachment),
	// we skip the plugin Update call since there are no actual changes to make in the cloud.
	// Instead, we create a synthetic ProgressResult using the existing resource's properties.
	hasEmptyPatch := data.resourceUpdate.Resource.PatchDocument == nil ||
		string(data.resourceUpdate.Resource.PatchDocument) == "[]" ||
		string(data.resourceUpdate.Resource.PatchDocument) == ""

	isBringingUnderManagement := data.resourceUpdate.ExistingResource.Stack == constants.UnmanagedStack &&
		data.resourceUpdate.Resource.Stack != constants.UnmanagedStack

	if isBringingUnderManagement && hasEmptyPatch {
		proc.Log().Debug("Bringing tag-less resource under management without property changes",
			"resourceURI", data.resourceUpdate.Resource.URI(),
			"oldStack", data.resourceUpdate.ExistingResource.Stack,
			"newStack", data.resourceUpdate.Resource.Stack)

		// Merge Properties and ReadOnlyProperties to get complete cloud state
		completeProperties, err := util.MergeJSON(
			data.resourceUpdate.ExistingResource.Properties,
			data.resourceUpdate.ExistingResource.ReadOnlyProperties,
		)
		if err != nil {
			proc.Log().Error("failed to merge properties for tag-less resource", "error", err)
			data.resourceUpdate.MarkAsFailed()
			return StateFinishedWithError, data, nil, nil
		}

		// Create synthetic ProgressResult with existing resource data
		// No ConvertToPluginFormat needed - properties are already in plain JSON format
		syntheticResult := resource.ProgressResult{
			Operation:          resource.OperationUpdate,
			OperationStatus:    resource.OperationStatusSuccess,
			StatusMessage:      "Brought under management without property changes (tag-less resource)",
			NativeID:           data.resourceUpdate.ExistingResource.NativeID,
			ResourceType:       data.resourceUpdate.Resource.Type,
			ResourceProperties: completeProperties,
			StartTs:            util.TimeNow(),
			ModifiedTs:         util.TimeNow(),
		}

		return handleProgressUpdate(state, data, syntheticResult, proc)
	}

	// Convert properties to plugin format (extracts $value from opaque structures)
	convertedResource, err := convertResourceForPlugin(data.resourceUpdate.Resource)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	convertedExisting, err := convertResourceForPlugin(data.resourceUpdate.ExistingResource)
	if err != nil {
		proc.Log().Error("failed to convert existing resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	updateOperation := plugin.UpdateResource{
		Namespace:        convertedResource.Namespace(),
		NativeID:         convertedResource.NativeID,
		Resource:         convertedResource,
		ExistingResource: convertedExisting,
		PatchDocument:    string(data.resourceUpdate.Resource.PatchDocument),
		Target:           data.resourceUpdate.ResourceTarget,
	}

	// First we check if progress already was made on the update operation. This can happen for example if the node crashed while the
	// update operation was in progress. If so, we try to recover from the previous progress.
	if found, lastKnownProgress := data.resourceUpdate.FindProgress(resource.OperationUpdate); found {
		return recoverFromPreviousProgress(StateUpdating, data, lastKnownProgress, updateOperation, proc)
	}

	result, err := doPluginOperation(
		data.resourceUpdate.Resource.URI(),
		updateOperation,
		proc)
	if err != nil {
		proc.Log().Error("failed to start update operation", "error", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	return handleProgressUpdate(state, data, *result, proc)
}

func recoverFromPreviousProgress(state gen.Atom, data ResourceUpdateData, lastKnownProgress *resource.ProgressResult, operation plugin.StatusCheck, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	// If the lastKnownProgress was finished, we can let the progress handler process the result and determine next steps.
	if lastKnownProgress.HasFinished() {
		return handleProgressUpdate(state, data, *lastKnownProgress, proc)
	}

	// Otherwise we retrieve the actual progress by spawning a plugin operator in the WaitingForResource state.
	actualProgress, err := resumeWaitingForResource(state, data, *lastKnownProgress, operation, proc)
	if err != nil {
		proc.Log().Error("failed to resume waiting for resource", "error", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	// If the actual progress is finished, again we let the progress handler process the result and determine next steps.
	if actualProgress.HasFinished() {
		return handleProgressUpdate(state, data, *actualProgress, proc)
	}

	// If not, this means that the plugin operator is waiting for the operation to be completed, so we stay in the
	// deleting state and wait for the next progress update.
	timeout := statemachine.StateTimeout{
		Duration: data.retryConfig.StatusCheckInterval * 2, // We generously wait twice the interval before we give up.
		Message:  PluginOperatorMissingInAction{},
	}

	return state, data, []statemachine.Action{timeout}, nil
}

func resumeWaitingForResource(state gen.Atom, data ResourceUpdateData, progress resource.ProgressResult, operation plugin.StatusCheck, proc gen.Process) (*resource.ProgressResult, error) {
	actualProgress, err := doPluginOperation(data.resourceUpdate.Resource.URI(), plugin.ResumeWaitingForResource{
		Namespace:         data.resourceUpdate.Resource.Namespace(),
		ResourceOperation: currentOperation(state),
		Request:           operation.StatusCheck(&progress),
		PreviousAttempts:  progress.Attempts,
	}, proc)
	if err != nil {
		proc.Log().Error("failed to resume waiting for resource", "error", err)
		return nil, fmt.Errorf("failed to resume waiting for resource: %w", err)
	}

	return actualProgress, nil
}

// handleProgressUpdate handles progress updates from th plugin operator. It persists any progress made, and moves the
// state machine to the next state when the plugin operation finished successfully. After the last plugin operation, or
// after the first error, it reports the final state to the stack updater and exits.
func handleProgressUpdate(state gen.Atom, data ResourceUpdateData, message resource.ProgressResult, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	err := data.resourceUpdate.RecordProgress(&message)
	if err != nil {
		proc.Log().Error("failed to record progress for resource update", "error", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	proc.Log().Debug("ResourceUpdater: sending progress update to the forma command persister", "state", state, "resourceURI", data.resourceUpdate.Resource.URI(), "progress", message.Operation)
	_, err = proc.Call(
		formaCommandPersisterProcess(proc),
		messages.UpdateResourceProgress{
			CommandID:          data.commandID,
			ResourceURI:        data.resourceUpdate.Resource.URI(),
			ResourceStartTs:    data.resourceUpdate.StartTs,
			ResourceModifiedTs: message.ModifiedTs,
			ResourceState:      data.resourceUpdate.State,
			Progress:           message,
		},
	)
	if err != nil {
		proc.Log().Error("failed to send UpdateResourceProgress message to forma command persister", "error", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	// If the plugin operation has finished successfully, we persist the resource update to the stack, inform the stack updater and exit
	if message.FinishedSuccessfully() {
		if data.commandSource == FormaCommandSourceDiscovery {
			if data.resourceUpdate.Filter(json.RawMessage(data.resourceUpdate.Resource.Properties), data.resourceUpdate.ResourceTarget) {
				proc.Log().Debug("Skipping discovered resource",
					"resourceType", data.resourceUpdate.Resource.Type,
					"nativeID", data.resourceUpdate.Resource.NativeID)
				data.resourceUpdate.MarkAsSuccess()
				return StateFinishedSuccessfully, data, nil, nil
			}

			// Calculate and set the label for discovered (unmanaged) resources.
			data.resourceUpdate.Resource.Label = data.resourceLabeler.LabelForUnmanagedResource(data.resourceUpdate.Resource.NativeID, data.resourceUpdate.Resource.Type, pkgmodel.GetTagsFromProperties(data.resourceUpdate.Resource.Properties), data.labelTagKeys)
		}

		operation := currentOperation(state)
		hash, err := proc.Call(resourcePersisterProcess(proc), PersistResourceUpdate{
			CommandID:         data.commandID,
			ResourceOperation: data.resourceUpdate.Operation,
			PluginOperation:   operation,
			ResourceUpdate:    *data.resourceUpdate,
		})

		if err != nil {
			proc.Log().Error("failed to persist resource update", "error", err)
			data.resourceUpdate.MarkAsFailed()
			return StateFinishedWithError, data, nil, nil
		}
		data.resourceUpdate.Version = hash.(string)

		// If we successfully persisted the read operation in the Synchronizing state, we should reject the resource update
		// and exit the state machine.
		if state == StateSynchronizing && data.resourceUpdate.Operation != OperationRead && operation == resource.OperationRead && hash != "" && !data.resourceUpdate.IsDelete() {
			proc.Log().Warning("Resource update rejected as a change to the resource was detected")
			data.resourceUpdate.Reject()

			return StateRejected, data, nil, nil
		}

		return nextState(state, data, proc)
	}

	if message.Failed() {
		return StateFinishedWithError, data, nil, nil
	}

	// If the plugin operation is still in progress, we stay in the current state and wait for the next progress update. We set up
	// a state timeout in case the plugin operator crashes.
	timeout := statemachine.StateTimeout{
		Duration: data.retryConfig.StatusCheckInterval * 2,
		Message:  PluginOperatorMissingInAction{},
	}

	return state, data, []statemachine.Action{timeout}, nil
}

func nextState(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	switch state {
	case StateInitializing:
		switch data.resourceUpdate.Operation {
		case OperationCreate:
			return resolve(StateResolving, data, proc)
		case OperationUpdate:
			// If the resource update has progress, we go to the updating state, otherwise we synchronize.
			if data.resourceUpdate.HasProgress() {
				return update(StateUpdating, data, proc)
			}
		case OperationDelete:
			// If the resource update has progress, we go to the deleting state, otherwise we synchronize.
			if data.resourceUpdate.HasProgress() {
				return delete(StateDeleting, data, proc)
			}
		default:
		}
		return synchronize(StateSynchronizing, data, proc)

	case StateSynchronizing:
		if data.resourceUpdate.IsSync() {
			return StateFinishedSuccessfully, data, nil, nil
		}
		if data.resourceUpdate.RequiresDelete() {
			return delete(StateDeleting, data, proc)
		}
		return resolve(StateResolving, data, proc)
	case StateDeleting:
		if !data.resourceUpdate.IsCreate() && !data.resourceUpdate.IsUpdate() {
			data.resourceUpdate.MarkAsSuccess()
			return StateFinishedSuccessfully, data, nil, nil
		}
		return resolve(StateResolving, data, proc)
	case StateResolving:
		if data.resourceUpdate.IsCreate() {
			return create(StateCreating, data, proc)
		}
		return update(StateUpdating, data, proc)
	case StateCreating:
		data.resourceUpdate.MarkAsSuccess()
		return StateFinishedSuccessfully, data, nil, nil
	case StateUpdating:
		data.resourceUpdate.MarkAsSuccess()
		return StateFinishedSuccessfully, data, nil, nil
	default:
		// We should never reach this point, so if we do we exit the state machine with an error.
		proc.Log().Error("ResourceUpdater reached an unexpected state", "state", state, "data", data)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, gen.TerminateReasonPanic
	}
}

func doPluginOperation(resourceURI pkgmodel.FormaeURI, operation plugin.PluginOperation, proc gen.Process) (*resource.ProgressResult, error) {
	// Generate a random operationID based on UUID
	operationID := uuid.New().String()

	// Spawn a PluginOperator via PluginCoordinator
	proc.Log().Debug("Spawning plugin operator via PluginCoordinator",
		"resourceURI", resourceURI,
		"operation", string(operation.Operation()),
		"namespace", operation.PluginNamespace())

	spawnResult, err := proc.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
		messages.SpawnPluginOperator{
			Namespace:   operation.PluginNamespace(),
			ResourceURI: string(resourceURI),
			Operation:   string(operation.Operation()),
			OperationID: operationID,
			RequestedBy: proc.PID(),
		})
	if err != nil {
		proc.Log().Error("failed to spawn plugin operator", "error", err)
		return nil, fmt.Errorf("failed to spawn plugin operator: %w", err)
	}

	result, ok := spawnResult.(messages.SpawnPluginOperatorResult)
	if !ok {
		return nil, fmt.Errorf("expected SpawnPluginOperatorResult, got %T", spawnResult)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("spawn plugin operator failed: %s", result.Error)
	}

	// Call the spawned PluginOperator with the operation
	proc.Log().Debug("Resource updater: calling plugin operator process",
		"resourceURI", resourceURI,
		"operation", operation.Operation(),
		"pid", result.PID)

	response, err := proc.CallWithTimeout(result.PID, operation, PluginOperationCallTimeout)
	if err != nil {
		return nil, err
	}

	progressResult, ok := response.(resource.ProgressResult)
	if !ok {
		return nil, fmt.Errorf("expected ProgressResult, got %T", response)
	}

	return &progressResult, nil
}

func pluginOperationMissingInAction(state gen.Atom, data ResourceUpdateData, message PluginOperatorMissingInAction, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	proc.Log().Error("Plugin operator is missing in action", "state", state, "data", data)
	data.resourceUpdate.MarkAsFailed()
	return StateFinishedWithError, data, nil, nil
}

func resolveCacheMissingInAction(state gen.Atom, data ResourceUpdateData, message ResolveCacheMissingInAction, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	proc.Log().Error("Resolve cache is missing in action", "state", state, "data", data)
	data.resourceUpdate.MarkAsFailed()
	return StateFinishedWithError, data, nil, nil
}

func resourceFailedToResolve(state gen.Atom, data ResourceUpdateData, message messages.FailedToResolveValue, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	proc.Log().Error("Failed to resolve resource property", "resourceUri", message.ResourceURI)
	data.resourceUpdate.MarkAsFailed()
	return StateFinishedWithError, data, nil, nil
}

func currentOperation(state gen.Atom) resource.Operation {
	switch state {
	case StateSynchronizing:
		return resource.OperationRead
	case StateCreating:
		return resource.OperationCreate
	case StateUpdating:
		return resource.OperationUpdate
	case StateDeleting:
		return resource.OperationDelete
	default:
		panic(fmt.Sprintf("currentOperation: unknown state %s", state))
	}
}
