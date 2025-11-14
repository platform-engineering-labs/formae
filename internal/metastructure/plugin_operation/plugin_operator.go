// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_operation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// PluginOperator is the Ergo state machine responsible for operations on cloud resources. The operator handles the
// lifecycle of plugin operations, including reading, creating, updating, and deleting resources.
// All the CRUD methods on the PluginOperator are syncrhonous and return a ProgressResult. If this ProgressResult
// is in a final state (which the caller can interrogate via HasFinished(), FinishedSuccessfully(), and Failed()
// methods), the PluginOperator will shut itself down. Otherwise, the PluginOperator will transition into either
// the WaitingForResource or Retrying state, depending on the status of the progress and it will start publishing
// subsequent progress updates to the requester of the plugin operation.
//
// The machine transitions are as follows:
//
//                              +-----------------------+
//          ------------------- |      NotStarted       |--------------------
//         |                    +-----------------------+                    |
//         |                        |               |                        |
//         |                        |               |                        |
//         |                        |               |                        |
//         |                        |               |                        |
//         |                        |               |                        |
//         |                        v               v                        |
//         |   +-----------------------+           +----------------------+  |
//         |   |      Waiting          |<--------- |   Retrying           |  |
//         |   +-----------------------+           +----------------------+  |
//         |         |        |  |                      ^      |    |        |
//         |         |        |  |                      |      |    |        |
//         |         |        |   ----------------------       |    |        |
//         |         |        |                                |    |        |
//         |         |      --|--------------------------------     |        |
//         |         |     |  |                                     |        |
//         |         |     |   --------------------------------     |        |
//         |         |     |                                   |    |        |
//         |         v     v                                   v    v        |
//         |   +----------------------+            +----------------------+  |
//          -> | FinishedSuccessfully |            |  FinishedWithErrors  |<-
//             +----------------------+            +----------------------+
//

type PluginOperator struct {
	statemachine.StateMachine[PluginUpdateData]
}

func newPluginOperator() gen.ProcessBehavior {
	return &PluginOperator{}
}

const (
	StateNotStarted           = gen.Atom("not_started")
	StateWaitingForResource   = gen.Atom("waiting_for_resource")
	StateRetrying             = gen.Atom("retrying")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
)

// Message types for the PluginOperator state machine

type StartPluginOperation struct{}

type Shutdown struct{}

type ResumeWaitingForResource struct {
	Namespace         string
	ResourceOperation resource.Operation
	Request           CheckStatus
	PreviousAttempts  int
}

type Retry struct {
	ResourceOperation resource.Operation
	Request           any
}

type ReadResource struct {
	Namespace        string
	NativeID         string
	Target           model.Target
	Resource         model.Resource
	ExistingResource model.Resource // The existing resource to read
	IsSync           bool
	IsDelete         bool

	// RedactSensitive declares intent to remove sensitive properties
	RedactSensitive bool
}

func (r *ReadResource) TreatNotFoundAsSuccess() bool {
	return r.IsSync || r.IsDelete
}

type CreateResource struct {
	Namespace string
	Resource  model.Resource
	Target    model.Target
}

type UpdateResource struct {
	Namespace        string
	NativeID         string
	Resource         model.Resource
	ExistingResource model.Resource // The existing resource to update
	PatchDocument    string
	Target           model.Target
}

type DeleteResource struct {
	Namespace    string
	NativeID     string
	Resource     model.Resource
	ResourceType string
	Metadata     json.RawMessage
	Target       model.Target
}

type ListParam struct {
	ParentProperty string
	ListParam      string
	ListValue      string
}

type ListResources struct {
	Namespace      string
	ResourceType   string
	Target         model.Target
	ListParameters map[string]ListParam
}

type Listing struct {
	Namespace      string
	Resources      []resource.Resource
	ResourceType   string
	ListParameters map[string]ListParam
	Target         model.Target
	Error          error
}

type CheckStatus struct {
	Namespace         string
	RequestID         string
	ResourceType      string          // Optional resource type for status checks
	Metadata          json.RawMessage // Optional metadata for status checks which might be necessary for some resources
	Target            model.Target
	ResourceOperation resource.Operation
	Request           any
}

// StatusCheck is an interface that facilitates generating a status check message from the different resource
// operations so the retry logic can be generalized.
type StatusCheck interface {
	StatusCheck(progress *resource.ProgressResult) CheckStatus
	Operation() resource.Operation
}

func (r ReadResource) StatusCheck(progress *resource.ProgressResult) CheckStatus {

	resolvedProperties, err := resolver.ConvertToPluginFormat(r.ExistingResource.Properties)
	if err != nil {
		slog.Debug("PluginOperator: failed to resolve properties for resource %s: %v", r.ExistingResource.Type, err)
	}
	r.ExistingResource.Properties = json.RawMessage(resolvedProperties)
	metadata, err := r.ExistingResource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource %s: %v", r.ExistingResource.Type, err)
		metadata = json.RawMessage("{}")
	}
	return CheckStatus{
		Namespace:         r.Resource.Namespace(),
		RequestID:         r.NativeID,
		ResourceType:      r.Resource.Type,
		Metadata:          metadata,
		Target:            r.Target,
		ResourceOperation: resource.OperationRead,
		Request:           r,
	}
}

func (c CreateResource) StatusCheck(progress *resource.ProgressResult) CheckStatus {
	resolvedProperties, err := resolver.ConvertToPluginFormat(c.Resource.Properties)
	if err != nil {
		slog.Debug("PluginOperator: failed to resolve properties for resource %s: %v", c.Resource.Type, err)
	}
	c.Resource.Properties = json.RawMessage(resolvedProperties)
	metadata, err := c.Resource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource %s: %v", c.Resource.Type, err)
		metadata = json.RawMessage("{}")
	}
	return CheckStatus{
		Namespace:         c.Namespace,
		RequestID:         progress.RequestID,
		ResourceType:      c.Resource.Type,
		Metadata:          metadata, //Required for legacy resources
		Target:            c.Target,
		ResourceOperation: resource.OperationCreate,
		Request:           c,
	}
}

func (u UpdateResource) StatusCheck(progress *resource.ProgressResult) CheckStatus {
	resolvedProperties, err := resolver.ConvertToPluginFormat(u.ExistingResource.Properties)
	if err != nil {
		slog.Debug("PluginOperator: failed to resolve properties for resource %s: %v", u.ExistingResource.Type, err)
	}
	u.ExistingResource.Properties = json.RawMessage(resolvedProperties)
	metadata, err := u.ExistingResource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource %s: %v", u.ExistingResource.Type, err)
		metadata = json.RawMessage("{}")
	}

	return CheckStatus{
		Namespace:         u.Namespace,
		RequestID:         progress.RequestID,
		ResourceType:      u.Resource.Type,
		Metadata:          metadata,
		Target:            u.Target,
		ResourceOperation: resource.OperationUpdate,
		Request:           u,
	}
}

func (d DeleteResource) StatusCheck(progress *resource.ProgressResult) CheckStatus {
	resolvedProperties, err := resolver.ConvertToPluginFormat(d.Resource.Properties)
	if err != nil {
		slog.Debug("PluginOperator: failed to resolve properties for resource %s: %v", d.Resource.Type, err)
	}
	d.Resource.Properties = json.RawMessage(resolvedProperties)
	metadata, err := d.Resource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource %s: %v", d.Resource.Type, err)
		metadata = json.RawMessage("{}")
	}
	return CheckStatus{
		Namespace:         d.Namespace,
		RequestID:         progress.RequestID,
		ResourceType:      d.ResourceType,
		Metadata:          metadata,
		Target:            d.Target,
		ResourceOperation: resource.OperationDelete,
		Request:           d,
	}
}

func (c CheckStatus) StatusCheck(progress *resource.ProgressResult) CheckStatus {
	return c
}

// PluginOperation is an interface that all resource operation messages implement to allow for handling them
// in a generic way.
type PluginOperation interface {
	Operation() resource.Operation
}

func (r ReadResource) Operation() resource.Operation {
	return resource.OperationRead
}

func (c CreateResource) Operation() resource.Operation {
	return resource.OperationCreate
}

func (u UpdateResource) Operation() resource.Operation {
	return resource.OperationUpdate
}

func (d DeleteResource) Operation() resource.Operation {
	return resource.OperationDelete
}

func (r ResumeWaitingForResource) Operation() resource.Operation {
	return r.ResourceOperation
}

func (c CheckStatus) Operation() resource.Operation {
	return resource.OperationNoOp
}

type PluginUpdateData struct {
	attempts          int
	LastStatusMessage string

	config        model.RetryConfig
	pluginManager *plugin.Manager
	context       context.Context
	requestedBy   gen.PID
}

var PluginNotFoundError = resource.ProgressResult{OperationStatus: resource.OperationStatusFailure, ErrorCode: resource.OperationErrorCodePluginNotFound, Attempts: 1, MaxAttempts: 1}

func (data PluginUpdateData) newUnforeseenError() resource.ProgressResult {
	return resource.ProgressResult{
		OperationStatus: resource.OperationStatusFailure,
		ErrorCode:       resource.OperationErrorCodeUnforeseenError,
		MaxAttempts:     int(data.config.MaxRetries) + 1,
		Attempts:        data.attempts,
	}
}

func (o *PluginOperator) Init(args ...any) (statemachine.StateMachineSpec[PluginUpdateData], error) {
	data := PluginUpdateData{
		requestedBy: args[0].(gen.PID),
		attempts:    1,
	}

	initialState := StateNotStarted
	if len(args) > 1 {
		initialState = args[1].(gen.Atom)
	}

	pluginManager, ok := o.Env("PluginManager")
	if !ok {
		o.Log().Error("PluginOperator: missing 'PluginManager' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'PluginManager' environment variable")
	}
	data.pluginManager = pluginManager.(*plugin.Manager)

	conext, ok := o.Env("Context")
	if !ok {
		o.Log().Error("PluginOperator: missing 'Context' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'Context' environment variable")
	}
	data.context = conext.(context.Context)

	cfg, ok := o.Env("RetryConfig")
	if !ok {
		o.Log().Error("PluginOperator: missing 'RetryConfig' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'RetryConfig' environment variable")
	}
	data.config = cfg.(model.RetryConfig)

	return statemachine.NewStateMachineSpec(initialState,
		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateCallHandler(StateNotStarted, read),
		statemachine.WithStateCallHandler(StateNotStarted, create),
		statemachine.WithStateCallHandler(StateNotStarted, update),
		statemachine.WithStateCallHandler(StateNotStarted, delete),
		statemachine.WithStateCallHandler(StateNotStarted, resume),
		statemachine.WithStateCallHandler(StateRetrying, read),
		statemachine.WithStateCallHandler(StateRetrying, create),
		statemachine.WithStateCallHandler(StateRetrying, update),
		statemachine.WithStateCallHandler(StateRetrying, delete),

		statemachine.WithStateMessageHandler(StateNotStarted, list),
		statemachine.WithStateMessageHandler(StateWaitingForResource, status),
		statemachine.WithStateMessageHandler(StateRetrying, retry),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdown),
	), nil
}

func shutdown(state gen.Atom, data PluginUpdateData, shutdown Shutdown, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data PluginUpdateData, proc gen.Process) (gen.Atom, PluginUpdateData, error) {
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError {
		err := proc.Send(proc.PID(), Shutdown{})
		if err != nil {
			proc.Log().Error("PluginOperator: failed to send terminate message: %v", err)
		}
	}
	return newState, data, nil
}

func read(state gen.Atom, data PluginUpdateData, operation ReadResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	plugin, err := data.pluginManager.ResourcePlugin(operation.ExistingResource.Namespace())
	if err != nil {
		proc.Log().Error("PluginOperator: read failed to get resource plugin for namespace %s: %v", operation.Resource.Namespace(), err)
		return StateFinishedWithError, data, PluginNotFoundError, nil, nil
	}

	properties, err := resolver.ConvertToPluginFormat(operation.ExistingResource.Properties)
	if err != nil {
		proc.Log().Error("failed to replace dynamic properties for resource update", "error", err, "resourceURI", operation.Resource.URI())
	}
	existingResource := operation.ExistingResource
	existingResource.Properties = properties
	metadata, err := existingResource.GetMetadata()
	if err != nil {
		proc.Log().Error("failed to get metadata for resource update", "error", err, "resourceURI", operation.Resource.URI())
		metadata = json.RawMessage("{}")
	}
	progressResult := resource.ProgressResult{
		Operation:    resource.OperationRead,
		NativeID:     operation.NativeID,
		ResourceType: operation.Resource.Type,
	}
	proc.Log().Debug("PluginOperator: starting read operation for %s", operation.NativeID)
	result, err := (*plugin).Read(data.context, &resource.ReadRequest{
		NativeID:        operation.NativeID,
		ResourceType:    operation.Resource.Type,
		Metadata:        metadata,
		Target:          &operation.Target,
		RedactSensitive: operation.RedactSensitive,
	})
	if err != nil {
		proc.Log().Debug("PluginOperator: failed to read resource: %v", err)
		progressResult.OperationStatus = resource.OperationStatusFailure
		progressResult.ErrorCode = resource.OperationErrorCodeUnforeseenError
		progressResult.StatusMessage = err.Error()
	} else if result.ErrorCode != "" && !operation.TreatNotFoundAsSuccess() {
		progressResult.OperationStatus = resource.OperationStatusFailure
		progressResult.ErrorCode = result.ErrorCode
	} else if result.ErrorCode == resource.OperationErrorCodeNotFound && operation.TreatNotFoundAsSuccess() {
		proc.Log().Debug("PluginOperator: resource %s not found, marking operation as successful due to sync or delete", operation.NativeID)
		progressResult.OperationStatus = resource.OperationStatusSuccess
		progressResult.ErrorCode = result.ErrorCode
	} else {
		progressResult.OperationStatus = resource.OperationStatusSuccess
		progressResult.ResourceProperties = json.RawMessage(result.Properties)
	}

	return handlePluginResult(data, operation, proc, &progressResult)
}

func create(state gen.Atom, data PluginUpdateData, operation CreateResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	plugin, err := data.pluginManager.ResourcePlugin(operation.Namespace)
	if err != nil {
		proc.Log().Error("PluginOperator: create failed to get resource plugin for namespace %s: %v", operation.Namespace, err)
		return StateFinishedWithError, data, PluginNotFoundError, nil, nil
	}
	proc.Log().Debug("PluginOperator: starting create operation for %s", operation.Resource.Type)

	props, err := resolver.ConvertToPluginFormat(operation.Resource.Properties)
	if err != nil {
		proc.Log().Error("PluginOperator: failed to resolve properties for resource %s: %v", operation.Resource.Type, err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	copyResource := operation.Resource
	copyResource.Properties = json.RawMessage(props)

	result, err := (*plugin).Create(data.context, &resource.CreateRequest{
		Resource: &copyResource,
		Target:   &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to create resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func update(state gen.Atom, data PluginUpdateData, operation UpdateResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	plugin, err := data.pluginManager.ResourcePlugin(operation.Namespace)
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to get resource plugin for namespace %s: %v", operation.Namespace, err)
		//		proc.sendError(err)
		return StateFinishedWithError, data, PluginNotFoundError, nil, nil
	}
	existingResource := operation.ExistingResource
	resolvedExistingProperties, err := resolver.ConvertToPluginFormat(existingResource.Properties)
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to resolve metadata for existing resource %s: %v", operation.ExistingResource.Type, err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	existingResource.Properties = resolvedExistingProperties
	oldMetadata, err := existingResource.GetMetadata()
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to get metadata for existing resource %s: %v", operation.ExistingResource.Type, err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}
	properties, err := resolver.ConvertToPluginFormat(operation.Resource.Properties)
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to parse metadata for resource %s: %v", operation.Resource.Type, err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}
	newResource := operation.Resource
	newResource.Properties = properties
	metadata, err := newResource.GetMetadata()
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to get metadata for resource %s: %v", operation.Resource.Type, err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	result, err := (*plugin).Update(data.context, &resource.UpdateRequest{
		NativeID:      &operation.NativeID,
		Resource:      &operation.Resource,
		PatchDocument: &operation.PatchDocument,
		OldMetadata:   oldMetadata,
		Metadata:      metadata,
		Target:        &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to update resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func delete(state gen.Atom, data PluginUpdateData, operation DeleteResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	plugin, err := data.pluginManager.ResourcePlugin(operation.Namespace)
	if err != nil {
		proc.Log().Error("PluginOperator: delete failed to get resource plugin for namespace %s: %v", operation.Namespace, err)
		//		proc.sendError(err)
		return StateFinishedWithError, data, PluginNotFoundError, nil, nil
	}
	result, err := (*plugin).Delete(data.context, &resource.DeleteRequest{
		NativeID:     &operation.NativeID,
		ResourceType: operation.ResourceType,
		Metadata:     operation.Metadata,
		Target:       &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to delete resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func status(state gen.Atom, data PluginUpdateData, operation CheckStatus, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	plugin, err := data.pluginManager.ResourcePlugin(operation.Namespace)
	if err != nil {
		proc.Log().Error("PluginOperator: status failed to get resource plugin for namespace %s: %v", operation.Namespace, err)
		return StateFinishedWithError, data, nil, nil
	}
	proc.Log().Debug("PluginOperator: checking status of resource %s", operation.RequestID)

	result, err := (*plugin).Status(data.context, &resource.StatusRequest{
		RequestID:    operation.RequestID,
		ResourceType: operation.ResourceType,
		Metadata:     operation.Metadata,
		Target:       &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to get status of resource: %v", err)
		return StateFinishedWithError, data, nil, nil
	}

	nextState, data, progress, actions, pluginErr := handlePluginResult(data, operation, proc, result.ProgressResult)
	err = proc.Send(data.requestedBy, progress)
	if err != nil {
		proc.Log().Error("PluginOperator: failed to send status result: %v", err)
		return StateFinishedWithError, data, nil, nil
	}

	return nextState, data, actions, pluginErr
}

func retry(state gen.Atom, data PluginUpdateData, operation Retry, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	var nextState gen.Atom
	var newData PluginUpdateData
	var progress resource.ProgressResult
	var actions []statemachine.Action
	var pluginErr error

	data.attempts++

	switch operation.ResourceOperation {
	case resource.OperationRead:
		nextState, newData, progress, actions, pluginErr = read(state, data, operation.Request.(ReadResource), proc)
	case resource.OperationCreate:
		nextState, newData, progress, actions, pluginErr = create(state, data, operation.Request.(CreateResource), proc)
	case resource.OperationUpdate:
		nextState, newData, progress, actions, pluginErr = update(state, data, operation.Request.(UpdateResource), proc)
	case resource.OperationDelete:
		nextState, newData, progress, actions, pluginErr = delete(state, data, operation.Request.(DeleteResource), proc)
	}

	progress.Attempts = data.attempts
	progress.MaxAttempts = data.config.MaxRetries + 1

	proc.Log().Debug("PluginOperator: sending progress update to resource updater %v", data.requestedBy)
	err := proc.Send(data.requestedBy, progress)
	if err != nil {
		proc.Log().Error("PluginOperator: failed to send status result: %v", err)
		return StateFinishedWithError, data, nil, fmt.Errorf("pluginOperator: failed to send status result: %v", err)
	}

	return nextState, newData, actions, pluginErr
}

func resume(state gen.Atom, data PluginUpdateData, operation ResumeWaitingForResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	data.attempts = operation.PreviousAttempts
	plugin, err := data.pluginManager.ResourcePlugin(operation.Namespace)
	if err != nil {
		proc.Log().Error("PluginOperator: resume failed to get resource plugin for namespace %s: %v", operation.Namespace, err)
		return StateFinishedWithError, data, resource.ProgressResult{}, nil, nil
	}
	proc.Log().Debug("PluginOperator: resume waiting for resource %s", operation.Request.RequestID)
	result, err := (*plugin).Status(data.context, &resource.StatusRequest{
		RequestID:    operation.Request.RequestID,
		ResourceType: operation.Request.ResourceType,
		Metadata:     operation.Request.Metadata,
		Target:       &operation.Request.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to get resume waiting for resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation.Request, proc, result.ProgressResult)
}

func handlePluginResult(data PluginUpdateData, operation StatusCheck, proc gen.Process, result *resource.ProgressResult) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	maxAttempts := int(data.config.MaxRetries) + 1
	progress := *result
	progress.Attempts = data.attempts
	progress.MaxAttempts = maxAttempts
	progress.ModifiedTs = util.TimeNow()

	if data.attempts > 1 {
		progress.StatusMessage = data.LastStatusMessage
	}

	if progress.FinishedSuccessfully() {
		proc.Log().Debug("PluginOperator: %T operation finished successfully", operation)
		return StateFinishedSuccessfully, data, progress, nil, nil
	}
	if progress.OperationStatus == resource.OperationStatusFailure {
		if resource.IsRecoverable(progress.ErrorCode) && data.attempts <= maxAttempts {
			proc.Log().Info("PluginOperator: %T operation failed with recoverable error code %s. Status message: %s. Retrying (%d/%d)", operation, progress.ErrorCode, progress.StatusMessage, data.attempts, maxAttempts)
			data.LastStatusMessage = progress.StatusMessage
			var retryMessage Retry
			if val, ok := operation.(CheckStatus); ok {
				retryMessage = Retry{ResourceOperation: val.ResourceOperation, Request: val.Request}
			} else {
				retryMessage = Retry{ResourceOperation: operation.Operation(), Request: operation}
			}

			_, err := proc.SendAfter(proc.PID(), retryMessage, data.config.RetryDelay)
			if err != nil {
				proc.Log().Error("PluginOperator: failed to send retry message: %v", err)
				return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
			}

			return StateRetrying, data, progress, nil, nil
		}

		proc.Log().Error("PluginOperator: %T operation failed with error code %s. Status message: %s", operation, progress.ErrorCode, progress.StatusMessage)
		return StateFinishedWithError, data, progress, nil, nil
	}

	// If we get here, it means the operation is still in progress and we schedule a status check.
	proc.Log().Debug("PluginOperator: %T operation is still in progress, scheduling status check after %d seconds", operation, data.config.StatusCheckInterval.Seconds())
	statusCheck := statemachine.GenericTimeout{
		Name:     "CheckStatus",
		Duration: data.config.StatusCheckInterval,
		Message:  operation.StatusCheck(&progress),
	}

	return StateWaitingForResource, data, progress, []statemachine.Action{statusCheck}, nil
}

func list(state gen.Atom, data PluginUpdateData, operation ListResources, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	plugin, err := data.pluginManager.ResourcePlugin(operation.Namespace)
	if err != nil {
		proc.Log().Error("PluginOperator: list failed to get resource plugin for namespace %s: %v", operation.Namespace, err)
		return StateFinishedWithError, data, nil, nil
	}

	proc.Log().Debug("PluginOperator: listing resources of type %s in target %s with list parameters %v", operation.ResourceType, operation.Target.Label, operation.ListParameters)
	var nextPageToken *string
	var resources []resource.Resource
	for {
		additionalProps := make(map[string]string)
		for _, v := range operation.ListParameters {
			additionalProps[v.ListParam] = v.ListValue
		}
		result, err := (*plugin).List(data.context, &resource.ListRequest{
			ResourceType:         operation.ResourceType,
			Target:               &operation.Target,
			PageSize:             100,
			PageToken:            nextPageToken,
			AdditionalProperties: additionalProps,
		})
		if err != nil {
			proc.Log().Error("PluginOperator: failed to list resources of type %s in target %s with list parameters %v: %v", operation.ResourceType, operation.Target.Label, operation.ListParameters, err)

			// Important: Retain resource type + target so discovery can remove the resource (and eventually return to idle)
			err = proc.Send(data.requestedBy, Listing{
				Namespace:      operation.Namespace,
				Resources:      []resource.Resource{},
				ResourceType:   operation.ResourceType,
				ListParameters: operation.ListParameters,
				Target:         operation.Target,
				Error:          err,
			})
			if err != nil {
				proc.Log().Error("PluginOperator: failed to send empty list result: %v", err)
			}
			return StateFinishedWithError, data, nil, nil
		}
		resources = append(resources, result.Resources...)
		nextPageToken = result.NextPageToken
		if nextPageToken == nil || *nextPageToken == "" {
			break
		}
	}

	err = proc.Send(data.requestedBy, Listing{
		Namespace:      operation.Namespace,
		Resources:      resources,
		ResourceType:   operation.ResourceType,
		ListParameters: operation.ListParameters,
		Target:         operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to send list result: %v", err)
		return StateFinishedWithError, data, nil, fmt.Errorf("pluginOperator: failed to send list result: %v", err)
	}

	return StateFinishedSuccessfully, data, nil, nil
}
