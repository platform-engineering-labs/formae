// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
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
//	                     +-----------------------+
//	 ------------------- |      NotStarted       |--------------------
//	|                    +-----------------------+                    |
//	|                        |               |                        |
//	|                        |               |                        |
//	|                        |               |                        |
//	|                        |               |                        |
//	|                        |               |                        |
//	|                        v               v                        |
//	|   +-----------------------+           +----------------------+  |
//	|   |      Waiting          |<--------- |   Retrying           |  |
//	|   +-----------------------+           +----------------------+  |
//	|         |        |  |                      ^      |    |        |
//	|         |        |  |                      |      |    |        |
//	|         |        |   ----------------------       |    |        |
//	|         |        |                                |    |        |
//	|         |      --|--------------------------------     |        |
//	|         |     |  |                                     |        |
//	|         |     |   --------------------------------     |        |
//	|         |     |                                   |    |        |
//	|         v     v                                   v    v        |
//	|   +----------------------+            +----------------------+  |
//	 -> | FinishedSuccessfully |            |  FinishedWithErrors  |<-
//	    +----------------------+            +----------------------+
type PluginOperator struct {
	statemachine.StateMachine[PluginUpdateData]
}

// PluginOperatorFactoryName is the factory name for remote spawning
const PluginOperatorFactoryName = "PluginOperator"

func NewPluginOperator() gen.ProcessBehavior {
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

type PluginOperatorShutdown struct{}

type ResumeWaitingForResource struct {
	Namespace         string
	ResourceOperation resource.Operation
	Request           PluginOperatorCheckStatus
	PreviousAttempts  int
}

type PluginOperatorRetry struct {
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

type PluginOperatorCheckStatus struct {
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
	StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus
	Operation() resource.Operation
}

// StatusCheck implementations - properties are expected to be pre-resolved by ResourceUpdater
func (r ReadResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	metadata, err := r.ExistingResource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource", "type", r.ExistingResource.Type, "error", err)
		metadata = json.RawMessage("{}")
	}
	return PluginOperatorCheckStatus{
		Namespace:         r.Resource.Namespace(),
		RequestID:         r.NativeID,
		ResourceType:      r.Resource.Type,
		Metadata:          metadata,
		Target:            r.Target,
		ResourceOperation: resource.OperationRead,
		Request:           r,
	}
}

func (c CreateResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	metadata, err := c.Resource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource", "type", c.Resource.Type, "error", err)
		metadata = json.RawMessage("{}")
	}
	return PluginOperatorCheckStatus{
		Namespace:         c.Namespace,
		RequestID:         progress.RequestID,
		ResourceType:      c.Resource.Type,
		Metadata:          metadata,
		Target:            c.Target,
		ResourceOperation: resource.OperationCreate,
		Request:           c,
	}
}

func (u UpdateResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	metadata, err := u.ExistingResource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource", "type", u.ExistingResource.Type, "error", err)
		metadata = json.RawMessage("{}")
	}
	return PluginOperatorCheckStatus{
		Namespace:         u.Namespace,
		RequestID:         progress.RequestID,
		ResourceType:      u.Resource.Type,
		Metadata:          metadata,
		Target:            u.Target,
		ResourceOperation: resource.OperationUpdate,
		Request:           u,
	}
}

func (d DeleteResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	metadata, err := d.Resource.GetMetadata()
	if err != nil {
		slog.Debug("PluginOperator: failed to get metadata for resource", "type", d.Resource.Type, "error", err)
		metadata = json.RawMessage("{}")
	}
	return PluginOperatorCheckStatus{
		Namespace:         d.Namespace,
		RequestID:         progress.RequestID,
		ResourceType:      d.ResourceType,
		Metadata:          metadata,
		Target:            d.Target,
		ResourceOperation: resource.OperationDelete,
		Request:           d,
	}
}

func (c PluginOperatorCheckStatus) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	return c
}

// PluginOperation is an interface that all resource operation messages implement to allow for handling them
// in a generic way.
type PluginOperation interface {
	Operation() resource.Operation
	PluginNamespace() string
}

func (r ReadResource) Operation() resource.Operation {
	return resource.OperationRead
}

func (r ReadResource) PluginNamespace() string {
	return r.Namespace
}

func (c CreateResource) Operation() resource.Operation {
	return resource.OperationCreate
}

func (c CreateResource) PluginNamespace() string {
	return c.Namespace
}

func (u UpdateResource) Operation() resource.Operation {
	return resource.OperationUpdate
}

func (u UpdateResource) PluginNamespace() string {
	return u.Namespace
}

func (d DeleteResource) Operation() resource.Operation {
	return resource.OperationDelete
}

func (d DeleteResource) PluginNamespace() string {
	return d.Namespace
}

func (r ResumeWaitingForResource) Operation() resource.Operation {
	return r.ResourceOperation
}

func (r ResumeWaitingForResource) PluginNamespace() string {
	return r.Namespace
}

func (c PluginOperatorCheckStatus) Operation() resource.Operation {
	return resource.OperationNoOp
}

func (c PluginOperatorCheckStatus) PluginNamespace() string {
	return c.Namespace
}

type PluginUpdateData struct {
	attempts          int
	LastStatusMessage string

	config      model.RetryConfig
	plugin      ResourcePlugin
	context     context.Context
	requestedBy gen.PID
}

// NamespaceMismatchError is returned when an operation targets a different namespace than the plugin handles
var NamespaceMismatchError = resource.ProgressResult{
	OperationStatus: resource.OperationStatusFailure,
	ErrorCode:       resource.OperationErrorCodePluginNotFound,
	Attempts:        1,
	MaxAttempts:     1,
}

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

	// Get the plugin from Ergo environment (set by plugin.Run or PluginCoordinator for local spawn)
	pluginEnv, ok := o.Env("Plugin")
	if !ok {
		o.Log().Error("PluginOperator: missing 'Plugin' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'Plugin' environment variable")
	}
	data.plugin = pluginEnv.(ResourcePlugin)

	ctx, ok := o.Env("Context")
	if !ok {
		o.Log().Error("PluginOperator: missing 'Context' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'Context' environment variable")
	}
	data.context = ctx.(context.Context)

	cfg, ok := o.Env("RetryConfig")
	if !ok {
		o.Log().Error("PluginOperator: missing 'RetryConfig' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'RetryConfig' environment variable")
	}
	data.config = cfg.(model.RetryConfig)

	return statemachine.NewStateMachineSpec(initialState,
		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateCallHandler(StateNotStarted, pluginOperatorRead),
		statemachine.WithStateCallHandler(StateNotStarted, pluginOperatorCreate),
		statemachine.WithStateCallHandler(StateNotStarted, pluginOperatorUpdate),
		statemachine.WithStateCallHandler(StateNotStarted, pluginOperatorDelete),
		statemachine.WithStateCallHandler(StateNotStarted, pluginOperatorResume),
		statemachine.WithStateCallHandler(StateRetrying, pluginOperatorRead),
		statemachine.WithStateCallHandler(StateRetrying, pluginOperatorCreate),
		statemachine.WithStateCallHandler(StateRetrying, pluginOperatorUpdate),
		statemachine.WithStateCallHandler(StateRetrying, pluginOperatorDelete),

		statemachine.WithStateMessageHandler(StateNotStarted, pluginOperatorList),
		statemachine.WithStateMessageHandler(StateWaitingForResource, pluginOperatorStatus),
		statemachine.WithStateMessageHandler(StateRetrying, pluginOperatorRetry),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, pluginOperatorShutdown),
		statemachine.WithStateMessageHandler(StateFinishedWithError, pluginOperatorShutdown),
	), nil
}

func pluginOperatorShutdown(state gen.Atom, data PluginUpdateData, shutdown PluginOperatorShutdown, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data PluginUpdateData, proc gen.Process) (gen.Atom, PluginUpdateData, error) {
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError {
		err := proc.Send(proc.PID(), PluginOperatorShutdown{})
		if err != nil {
			proc.Log().Error("PluginOperator: failed to send terminate message: %v", err)
		}
	}
	return newState, data, nil
}

// validateNamespace checks that the operation targets the correct plugin
func validateNamespace(data PluginUpdateData, namespace string, proc gen.Process) bool {
	if namespace != data.plugin.Namespace() {
		proc.Log().Error("PluginOperator: namespace mismatch", "expected", data.plugin.Namespace(), "got", namespace)
		return false
	}
	return true
}

func pluginOperatorRead(state gen.Atom, data PluginUpdateData, operation ReadResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	// Properties are expected to be pre-resolved by ResourceUpdater
	existingResource := operation.ExistingResource
	metadata, err := existingResource.GetMetadata()
	if err != nil {
		proc.Log().Error("failed to get metadata for resource read", "error", err, "resourceURI", operation.Resource.URI())
		metadata = json.RawMessage("{}")
	}

	progressResult := resource.ProgressResult{
		Operation:    resource.OperationRead,
		NativeID:     operation.NativeID,
		ResourceType: operation.Resource.Type,
	}
	proc.Log().Debug("PluginOperator: starting read operation for %s", operation.NativeID)

	result, err := data.plugin.Read(data.context, &resource.ReadRequest{
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

func pluginOperatorCreate(state gen.Atom, data PluginUpdateData, operation CreateResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	proc.Log().Debug("PluginOperator: starting create operation for %s", operation.Resource.Type)

	// Properties are expected to be pre-resolved by ResourceUpdater
	result, err := data.plugin.Create(data.context, &resource.CreateRequest{
		Resource: &operation.Resource,
		Target:   &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to create resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func pluginOperatorUpdate(state gen.Atom, data PluginUpdateData, operation UpdateResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	// Properties are expected to be pre-resolved by ResourceUpdater
	existingResource := operation.ExistingResource
	oldMetadata, err := existingResource.GetMetadata()
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to get metadata for existing resource", "type", operation.ExistingResource.Type, "error", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	newResource := operation.Resource
	metadata, err := newResource.GetMetadata()
	if err != nil {
		proc.Log().Error("PluginOperator: update failed to get metadata for resource", "type", operation.Resource.Type, "error", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	result, err := data.plugin.Update(data.context, &resource.UpdateRequest{
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

func pluginOperatorDelete(state gen.Atom, data PluginUpdateData, operation DeleteResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	result, err := data.plugin.Delete(data.context, &resource.DeleteRequest{
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

func pluginOperatorStatus(state gen.Atom, data PluginUpdateData, operation PluginOperatorCheckStatus, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, nil, nil
	}

	proc.Log().Debug("PluginOperator: checking status of resource %s", operation.RequestID)

	result, err := data.plugin.Status(data.context, &resource.StatusRequest{
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

func pluginOperatorRetry(state gen.Atom, data PluginUpdateData, operation PluginOperatorRetry, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	var nextState gen.Atom
	var newData PluginUpdateData
	var progress resource.ProgressResult
	var actions []statemachine.Action
	var pluginErr error

	data.attempts++

	switch operation.ResourceOperation {
	case resource.OperationRead:
		nextState, newData, progress, actions, pluginErr = pluginOperatorRead(state, data, operation.Request.(ReadResource), proc)
	case resource.OperationCreate:
		nextState, newData, progress, actions, pluginErr = pluginOperatorCreate(state, data, operation.Request.(CreateResource), proc)
	case resource.OperationUpdate:
		nextState, newData, progress, actions, pluginErr = pluginOperatorUpdate(state, data, operation.Request.(UpdateResource), proc)
	case resource.OperationDelete:
		nextState, newData, progress, actions, pluginErr = pluginOperatorDelete(state, data, operation.Request.(DeleteResource), proc)
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

func pluginOperatorResume(state gen.Atom, data PluginUpdateData, operation ResumeWaitingForResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	data.attempts = operation.PreviousAttempts
	proc.Log().Debug("PluginOperator: resume waiting for resource %s", operation.Request.RequestID)

	result, err := data.plugin.Status(data.context, &resource.StatusRequest{
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
	progress.ModifiedTs = time.Now()

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
			var retryMessage PluginOperatorRetry
			if val, ok := operation.(PluginOperatorCheckStatus); ok {
				retryMessage = PluginOperatorRetry{ResourceOperation: val.ResourceOperation, Request: val.Request}
			} else {
				retryMessage = PluginOperatorRetry{ResourceOperation: operation.Operation(), Request: operation}
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

func pluginOperatorList(state gen.Atom, data PluginUpdateData, operation ListResources, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
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
		result, err := data.plugin.List(data.context, &resource.ListRequest{
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

	err := proc.Send(data.requestedBy, Listing{
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
