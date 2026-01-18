// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// ActivePluginOperators tracks the number of currently active PluginOperator instances.
// This is used for debugging and monitoring to detect resource leaks.
var ActivePluginOperators atomic.Int64

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
	Namespace    string
	DesiredState model.Resource
	Target       model.Target
}

type UpdateResource struct {
	Namespace     string
	NativeID      string
	DesiredState  model.Resource
	PriorState    model.Resource // The prior resource state
	PatchDocument string
	Target        model.Target
}

type DeleteResource struct {
	Namespace    string
	NativeID     string
	Resource     model.Resource
	ResourceType string
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

// ListedResource is a lightweight representation of a discovered resource,
// containing only the NativeID needed for discovery. This keeps the Listing
// message small to avoid exceeding Ergo's message size limits.
type ListedResource struct {
	NativeID     string
	ResourceType string
}

// Listing is the response from a List operation, containing discovered resources.
// Uses ListedResource instead of full resource.Resource to minimize message size.
type Listing struct {
	Namespace      string
	Resources      []byte // gzip-compressed JSON of []ListedResource
	ResourceType   string
	ListParameters map[string]ListParam
	Target         model.Target
	Error          error
}

// CompressListedResources compresses a slice of ListedResource to gzip-compressed JSON.
// This is required because Ergo has a hardcoded 64KB buffer limit.
func CompressListedResources(resources []ListedResource) ([]byte, error) {
	jsonData, err := json.Marshal(resources)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal listed resources: %w", err)
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}

	if _, err := gz.Write(jsonData); err != nil {
		return nil, fmt.Errorf("failed to write compressed data: %w", err)
	}

	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// DecompressListedResources decompresses gzip-compressed JSON to a slice of ListedResource.
func DecompressListedResources(data []byte) ([]ListedResource, error) {
	if len(data) == 0 {
		return nil, nil
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	var resources []ListedResource
	if err := json.NewDecoder(gz).Decode(&resources); err != nil {
		return nil, fmt.Errorf("failed to decode listed resources: %w", err)
	}

	return resources, nil
}

type PluginOperatorCheckStatus struct {
	Namespace         string
	RequestID         string
	NativeID          string // NativeID returned by initial Create/Update
	ResourceType      string // Optional resource type for status checks
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
	return PluginOperatorCheckStatus{
		Namespace:         r.Resource.Namespace(),
		RequestID:         r.NativeID,
		ResourceType:      r.Resource.Type,
		Target:            r.Target,
		ResourceOperation: resource.OperationRead,
		Request:           r,
	}
}

func (c CreateResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	return PluginOperatorCheckStatus{
		Namespace:         c.Namespace,
		RequestID:         progress.RequestID,
		NativeID:          progress.NativeID,
		ResourceType:      c.DesiredState.Type,
		Target:            c.Target,
		ResourceOperation: resource.OperationCreate,
		Request:           c,
	}
}

func (u UpdateResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	return PluginOperatorCheckStatus{
		Namespace:         u.Namespace,
		RequestID:         progress.RequestID,
		NativeID:          progress.NativeID,
		ResourceType:      u.DesiredState.Type,
		Target:            u.Target,
		ResourceOperation: resource.OperationUpdate,
		Request:           u,
	}
}

func (d DeleteResource) StatusCheck(progress *resource.ProgressResult) PluginOperatorCheckStatus {
	return PluginOperatorCheckStatus{
		Namespace:         d.Namespace,
		RequestID:         progress.RequestID,
		NativeID:          progress.NativeID,
		ResourceType:      d.ResourceType,
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

	// Metrics
	operationStartTime time.Time
	operationDuration  otelmetric.Float64Histogram
	operationCounter   otelmetric.Int64Counter
	retryCounter       otelmetric.Int64Counter
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
	// Track active operators for debugging
	count := ActivePluginOperators.Add(1)
	o.Log().Debug("PluginOperator started, active operators: %d", count)

	data := PluginUpdateData{
		attempts: 1,
	}

	initialState := StateNotStarted
	if len(args) > 0 {
		initialState = args[0].(gen.Atom)
	}

	pluginEnv, ok := o.Env("Plugin")
	if !ok {
		pluginEnv, ok = o.Node().Env("Plugin")
		if !ok {
			o.Log().Error("PluginOperator: missing 'Plugin' environment variable")
			return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'Plugin' environment variable")
		}
	}
	data.plugin = pluginEnv.(ResourcePlugin)

	ctx, ok := o.Env("Context")
	if !ok {
		ctx, ok = o.Node().Env("Context")
		if !ok {
			o.Log().Error("PluginOperator: missing 'Context' environment variable")
			return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'Context' environment variable")
		}
	}
	data.context = ctx.(context.Context)

	// RetryConfig from node environment
	cfg, ok := o.Env("RetryConfig")
	if !ok {
		o.Log().Error("PluginOperator: missing 'RetryConfig' environment variable")
		return statemachine.StateMachineSpec[PluginUpdateData]{}, fmt.Errorf("pluginOperator: missing 'RetryConfig' environment variable")
	}
	data.config = cfg.(model.RetryConfig)

	// Initialize OTel metrics
	if err := setupPluginOperatorMetrics(&data); err != nil {
		o.Log().Error("Failed to setup plugin operator metrics: %v", err)
		// Don't fail initialization if metrics setup fails
	}

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

func shutdown(from gen.PID, state gen.Atom, data PluginUpdateData, shutdown PluginOperatorShutdown, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data PluginUpdateData, proc gen.Process) (gen.Atom, PluginUpdateData, error) {
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError {
		// Track active operators for debugging
		count := ActivePluginOperators.Add(-1)
		proc.Log().Debug("PluginOperator finished (state=%s), active operators: %d", newState, count)

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

func read(from gen.PID, state gen.Atom, data PluginUpdateData, operation ReadResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	data.requestedBy = from
	data.operationStartTime = time.Now() // Start timing
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
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

func create(from gen.PID, state gen.Atom, data PluginUpdateData, operation CreateResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	data.requestedBy = from
	data.operationStartTime = time.Now() // Start timing
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	proc.Log().Debug("PluginOperator: starting create operation for %s", operation.DesiredState.Type)

	// Properties are expected to be pre-resolved by ResourceUpdater
	result, err := data.plugin.Create(data.context, &resource.CreateRequest{
		DesiredState: &operation.DesiredState,
		Target:       &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to create resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func update(from gen.PID, state gen.Atom, data PluginUpdateData, operation UpdateResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	data.requestedBy = from
	data.operationStartTime = time.Now() // Start timing
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	result, err := data.plugin.Update(data.context, &resource.UpdateRequest{
		NativeID:      &operation.NativeID,
		PriorState:    &operation.PriorState,
		DesiredState:  &operation.DesiredState,
		PatchDocument: &operation.PatchDocument,
		Target:        &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to update resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func delete(from gen.PID, state gen.Atom, data PluginUpdateData, operation DeleteResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	data.requestedBy = from
	data.operationStartTime = time.Now() // Start timing
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	result, err := data.plugin.Delete(data.context, &resource.DeleteRequest{
		NativeID:     &operation.NativeID,
		ResourceType: operation.ResourceType,
		Target:       &operation.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to delete resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation, proc, result.ProgressResult)
}

func status(from gen.PID, state gen.Atom, data PluginUpdateData, operation PluginOperatorCheckStatus, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, nil, nil
	}

	proc.Log().Debug("PluginOperator: checking status of resource %s", operation.RequestID)

	result, err := data.plugin.Status(data.context, &resource.StatusRequest{
		RequestID:    operation.RequestID,
		NativeID:     operation.NativeID,
		ResourceType: operation.ResourceType,
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

func retry(from gen.PID, state gen.Atom, data PluginUpdateData, operation PluginOperatorRetry, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	var nextState gen.Atom
	var newData PluginUpdateData
	var progress resource.ProgressResult
	var actions []statemachine.Action
	var pluginErr error

	data.attempts++

	// Use data.requestedBy (the original caller) instead of from (which is the PluginOperator's own PID
	// when retry messages are sent via SendAfter). This preserves the correct recipient for progress updates.
	switch operation.ResourceOperation {
	case resource.OperationRead:
		nextState, newData, progress, actions, pluginErr = read(data.requestedBy, state, data, operation.Request.(ReadResource), proc)
	case resource.OperationCreate:
		nextState, newData, progress, actions, pluginErr = create(data.requestedBy, state, data, operation.Request.(CreateResource), proc)
	case resource.OperationUpdate:
		nextState, newData, progress, actions, pluginErr = update(data.requestedBy, state, data, operation.Request.(UpdateResource), proc)
	case resource.OperationDelete:
		nextState, newData, progress, actions, pluginErr = delete(data.requestedBy, state, data, operation.Request.(DeleteResource), proc)
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

func resume(from gen.PID, state gen.Atom, data PluginUpdateData, operation ResumeWaitingForResource, proc gen.Process) (gen.Atom, PluginUpdateData, resource.ProgressResult, []statemachine.Action, error) {
	data.requestedBy = from
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, NamespaceMismatchError, nil, nil
	}

	data.attempts = operation.PreviousAttempts
	proc.Log().Debug("PluginOperator: resume waiting for resource %s", operation.Request.RequestID)

	result, err := data.plugin.Status(data.context, &resource.StatusRequest{
		RequestID:    operation.Request.RequestID,
		NativeID:     operation.Request.NativeID,
		ResourceType: operation.Request.ResourceType,
		Target:       &operation.Request.Target,
	})
	if err != nil {
		proc.Log().Error("PluginOperator: failed to get resume waiting for resource: %v", err)
		return StateFinishedWithError, data, data.newUnforeseenError(), nil, nil
	}

	return handlePluginResult(data, operation.Request, proc, result.ProgressResult)
}

// calculateExponentialBackoff returns a delay that doubles with each attempt.
// For throttling errors, this prevents the "thundering herd" problem where
// all throttled operations retry simultaneously after a fixed delay.
// Formula: baseDelay * 2^(attempt-1), capped at 30 seconds.
func calculateExponentialBackoff(attempt int, baseDelay time.Duration) time.Duration {
	const maxBackoff = 30 * time.Second

	if attempt <= 1 {
		return baseDelay
	}

	// Calculate 2^(attempt-1)
	multiplier := 1 << (attempt - 1) // 1, 2, 4, 8, ...
	backoff := baseDelay * time.Duration(multiplier)

	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

// isThrottlingError checks if an error is a throttling/rate limit error.
// This checks for common throttling indicators in error messages since
// the AWS SDK wraps errors when retries are exhausted.
func isThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "ThrottlingException") ||
		strings.Contains(errStr, "Throttling") ||
		strings.Contains(errStr, "Rate exceeded") ||
		strings.Contains(errStr, "TooManyRequestsException") ||
		strings.Contains(errStr, "RequestLimitExceeded")
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

	// Record metrics when operation finishes (success or final failure)
	if progress.FinishedSuccessfully() || (progress.OperationStatus == resource.OperationStatusFailure && data.attempts >= maxAttempts) {
		recordPluginMetrics(data, operation, &progress)
	}

	if progress.FinishedSuccessfully() {
		proc.Log().Debug("PluginOperator: %T operation finished successfully", operation)
		return StateFinishedSuccessfully, data, progress, nil, nil
	}
	if progress.OperationStatus == resource.OperationStatusFailure {
		if resource.IsRecoverable(progress.ErrorCode) && data.attempts <= maxAttempts {
			// Use exponential backoff for throttling errors to avoid thundering herd
			retryDelay := data.config.RetryDelay
			if progress.ErrorCode == resource.OperationErrorCodeThrottling {
				retryDelay = calculateExponentialBackoff(data.attempts, data.config.RetryDelay)
				proc.Log().Debug("PluginOperator: %T throttled, backing off for %v before retry (%d/%d)", operation, retryDelay, data.attempts, maxAttempts)
			} else {
				proc.Log().Info("PluginOperator: %T operation failed with recoverable error code %s. Status message: %s. Retrying (%d/%d)", operation, progress.ErrorCode, progress.StatusMessage, data.attempts, maxAttempts)
			}
			data.LastStatusMessage = progress.StatusMessage
			var retryMessage PluginOperatorRetry
			if val, ok := operation.(PluginOperatorCheckStatus); ok {
				retryMessage = PluginOperatorRetry{ResourceOperation: val.ResourceOperation, Request: val.Request}
			} else {
				retryMessage = PluginOperatorRetry{ResourceOperation: operation.Operation(), Request: operation}
			}

			_, err := proc.SendAfter(proc.PID(), retryMessage, retryDelay)
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

func list(from gen.PID, state gen.Atom, data PluginUpdateData, operation ListResources, proc gen.Process) (gen.Atom, PluginUpdateData, []statemachine.Action, error) {
	data.requestedBy = from
	if !validateNamespace(data, operation.Namespace, proc) {
		return StateFinishedWithError, data, nil, nil
	}

	proc.Log().Debug("PluginOperator: listing resources of type %s in target %s with list parameters %v", operation.ResourceType, operation.Target.Label, operation.ListParameters)
	var nextPageToken *string
	pagenr := 1
	var listedResources []ListedResource

	const maxListAttempts = 4 // Total attempts including initial

	for {
		additionalProps := make(map[string]string)
		for _, v := range operation.ListParameters {
			additionalProps[v.ListParam] = v.ListValue
		}

		var result *resource.ListResult
		var err error

		// Retry loop with exponential backoff for throttling
		for attempt := 1; attempt <= maxListAttempts; attempt++ {
			result, err = data.plugin.List(data.context, &resource.ListRequest{
				ResourceType:         operation.ResourceType,
				Target:               &operation.Target,
				PageSize:             100,
				PageToken:            nextPageToken,
				AdditionalProperties: additionalProps,
			})
			if err == nil {
				break // Success
			}

			// Check if this is a throttling error worth retrying
			if isThrottlingError(err) && attempt < maxListAttempts {
				backoff := calculateExponentialBackoff(attempt, data.config.RetryDelay)
				proc.Log().Debug("PluginOperator: list %s throttled, backing off for %v before retry (%d/%d)",
					operation.ResourceType, backoff, attempt, maxListAttempts)
				time.Sleep(backoff)
				continue
			}

			// Non-throttling error or max attempts reached
			break
		}

		if err != nil {
			proc.Log().Error("PluginOperator: failed to list resources of type %s in target %s with list parameters %v: %v", operation.ResourceType, operation.Target.Label, operation.ListParameters, err)

			// Important: Retain resource type + target so discovery can remove the resource (and eventually return to idle)
			sendErr := proc.Send(data.requestedBy, Listing{
				Namespace:      operation.Namespace,
				Resources:      nil, // Empty compressed data on error
				ResourceType:   operation.ResourceType,
				ListParameters: operation.ListParameters,
				Target:         operation.Target,
				Error:          err,
			})
			if sendErr != nil {
				proc.Log().Error("PluginOperator: failed to send empty list result: %v", sendErr)
			}
			return StateFinishedWithError, data, nil, nil
		}
		proc.Log().Debug("PluginOperator: received %d resources in page %d for resources of type %s in target %s", len(result.Resources), pagenr, operation.ResourceType, operation.Target.Label)
		pagenr++
		// Convert full resources to lightweight ListedResource (only NativeID and ResourceType needed for discovery)
		for _, res := range result.Resources {
			listedResources = append(listedResources, ListedResource{
				NativeID:     res.NativeID,
				ResourceType: operation.ResourceType,
			})
		}
		nextPageToken = result.NextPageToken
		if nextPageToken == nil || *nextPageToken == "" {
			break
		}
	}

	// Compress resources to work around Ergo's 64KB message size limit
	compressedResources, err := CompressListedResources(listedResources)
	if err != nil {
		proc.Log().Error("PluginOperator: failed to compress list result: %v", err)
		return StateFinishedWithError, data, nil, fmt.Errorf("pluginOperator: failed to compress list result: %v", err)
	}
	proc.Log().Debug("PluginOperator: compressed %d resources from JSON to %d bytes", len(listedResources), len(compressedResources))

	err = proc.Send(data.requestedBy, Listing{
		Namespace:      operation.Namespace,
		Resources:      compressedResources,
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

// setupPluginOperatorMetrics initializes OTel metrics for plugin operations
func setupPluginOperatorMetrics(data *PluginUpdateData) error {
	meter := otel.Meter("formae/plugin_operator")

	var err error
	data.operationDuration, err = meter.Float64Histogram(
		"formae.plugin.operation.duration_ms",
		otelmetric.WithDescription("Duration of plugin operations"),
		otelmetric.WithUnit("ms"),
	)
	if err != nil {
		return fmt.Errorf("failed to create operation duration histogram: %w", err)
	}

	data.operationCounter, err = meter.Int64Counter(
		"formae.plugin.operation.total",
		otelmetric.WithDescription("Total plugin operations"),
	)
	if err != nil {
		return fmt.Errorf("failed to create operation counter: %w", err)
	}

	data.retryCounter, err = meter.Int64Counter(
		"formae.plugin.operation.retries.total",
		otelmetric.WithDescription("Total plugin operation retries"),
	)
	if err != nil {
		return fmt.Errorf("failed to create retry counter: %w", err)
	}

	return nil
}

// recordPluginMetrics records metrics for a completed plugin operation
func recordPluginMetrics(data PluginUpdateData, operation StatusCheck, progress *resource.ProgressResult) {
	if !data.operationStartTime.IsZero() {
		duration := time.Since(data.operationStartTime).Milliseconds()

		// Determine result status
		var result string
		if progress.FinishedSuccessfully() {
			result = "success"
		} else if progress.OperationStatus == resource.OperationStatusFailure {
			result = "failure"
		} else {
			result = "unknown"
		}

		attrs := []attribute.KeyValue{
			attribute.String("plugin", data.plugin.Namespace()),
			attribute.String("operation", string(operation.Operation())),
			attribute.String("result", result),
		}

		// Add resource type if available
		if progress.ResourceType != "" {
			attrs = append(attrs, attribute.String("resource_type", progress.ResourceType))
		}

		// Record duration
		if data.operationDuration != nil {
			data.operationDuration.Record(data.context, float64(duration), otelmetric.WithAttributes(attrs...))
		}

		// Record operation count
		if data.operationCounter != nil {
			data.operationCounter.Add(data.context, 1, otelmetric.WithAttributes(attrs...))
		}

		// Record retries if there were any
		if data.attempts > 1 && data.retryCounter != nil {
			retryAttrs := append(attrs, attribute.Int("attempts", data.attempts))
			if progress.ErrorCode != "" {
				retryAttrs = append(retryAttrs, attribute.String("error_code", string(progress.ErrorCode)))
			}
			data.retryCounter.Add(data.context, int64(data.attempts-1), otelmetric.WithAttributes(retryAttrs...))
		}
	}
}
