// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource

import (
	"encoding/json"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

type CreateRequest struct {
	DesiredState *model.Resource
	Target       *model.Target
}

type CreateResult struct {
	ProgressResult *ProgressResult
}

type UpdateRequest struct {
	NativeID      *string
	PriorState    *model.Resource // Previous state of the resource
	DesiredState  *model.Resource // New desired state of the resource
	PatchDocument *string
	Target        *model.Target
}

type UpdateResult struct {
	ProgressResult *ProgressResult
}

type DeleteRequest struct {
	NativeID     *string
	ResourceType string
	Target       *model.Target
}

type DeleteResult struct {
	ProgressResult *ProgressResult
}

type StatusRequest struct {
	RequestID    string
	NativeID     string            // NativeID returned by initial Create/Update
	ResourceType string            // Optional resource type for status checks
	Target       *model.Target
}

type StatusResult struct {
	ProgressResult *ProgressResult
}

type ReadRequest struct {
	NativeID     string
	ResourceType string
	Target       *model.Target

	// RedactSensitive declares intent to remove sensitive properties
	RedactSensitive bool
}

type ReadResult struct {
	ResourceType string
	Properties   string
	ErrorCode    OperationErrorCode `json:"ErrorCode,omitempty"`
}

type Resource struct {
	NativeID   string
	Properties string
}

type ListRequest struct {
	ResourceType         string
	Target               *model.Target
	PageSize             int32
	PageToken            *string
	AdditionalProperties map[string]string
}

type ListResult struct {
	ResourceType  string
	Resources     []Resource
	NextPageToken *string
}

type ProgressResult struct {
	Operation       Operation
	OperationStatus OperationStatus

	RequestID          string
	NativeID           string // Required - all plugins must set this
	ResourceType       string
	ResourceProperties json.RawMessage    `json:"ResourceProperties,omitempty"`
	StartTs            time.Time          `json:"StartTs"`
	ModifiedTs         time.Time          `json:"ModifiedTs"`
	ErrorCode          OperationErrorCode `json:"ErrorCode,omitempty"`
	StatusMessage      string             `json:"StatusMessage,omitempty"`
	Attempts           int                `json:"Attempts,omitempty"`
	MaxAttempts        int                `json:"MaxAttempts,omitempty"`
}

func (pr *ProgressResult) Zero() ProgressResult {
	return ProgressResult{}
}

func (pr *ProgressResult) FinishedSuccessfully() bool {
	return pr.OperationStatus == OperationStatusSuccess
}

func (pr *ProgressResult) Failed() bool {
	if pr.OperationStatus != OperationStatusFailure {
		return false
	}
	// Attempts will always be one more than the maximum attempts allowed after exhausting all retries. This is
	// because progress results are sent during each attempt and we would have no way of knowing if the last operation
	// is finished or still in progress.
	return !IsRecoverable(pr.ErrorCode) || pr.Attempts > pr.MaxAttempts
}

func (pr *ProgressResult) InProgress() bool {
	return !pr.HasFinished()
}

func (pr *ProgressResult) HasFinished() bool {
	return pr.FinishedSuccessfully() || pr.Failed()
}

type Operation string

const (
	OperationCreate       Operation = "Create"
	OperationUpdate       Operation = "Update"
	OperationDelete       Operation = "Delete"
	OperationRead         Operation = "Read"
	OperationCheckStatus  Operation = "Status"
	OperationList         Operation = "List"
	OperationNoOp         Operation = "NoOp"
	OperationNotSupported Operation = "NotSupported"
)

type OperationStatus string

const (
	OperationStatusSuccess    OperationStatus = "Success"
	OperationStatusFailure    OperationStatus = "Failure"
	OperationStatusInProgress OperationStatus = "InProgress"
	OperationStatusPending    OperationStatus = "Pending"
)

type OperationErrorCode string

const (
	//Collected these through the AWS CloudControl API
	OperationErrorCodeNotUpdatable                 OperationErrorCode = "NotUpdatable"
	OperationErrorCodeInvalidRequest               OperationErrorCode = "InvalidRequest"
	OperationErrorCodeAccessDenied                 OperationErrorCode = "AccessDenied"
	OperationErrorCodeUnauthorizedTaggingOperation OperationErrorCode = "UnauthorizedTaggingOperation"
	OperationErrorCodeInvalidCredentials           OperationErrorCode = "InvalidCredentials"
	OperationErrorCodeAlreadyExists                OperationErrorCode = "AlreadyExists"
	OperationErrorCodeNotFound                     OperationErrorCode = "NotFound"
	OperationErrorCodeResourceConflict             OperationErrorCode = "ResourceConflict"
	OperationErrorCodeThrottling                   OperationErrorCode = "Throttling"
	OperationErrorCodeServiceLimitExceeded         OperationErrorCode = "ServiceLimitExceeded"
	OperationErrorCodeNotStabilized                OperationErrorCode = "NotStabilized"
	OperationErrorCodeGeneralServiceException      OperationErrorCode = "GeneralServiceException"
	OperationErrorCodeServiceInternalError         OperationErrorCode = "ServiceInternalError"
	OperationErrorCodeServiceTimeout               OperationErrorCode = "ServiceTimeout"
	OperationErrorCodeNetworkFailure               OperationErrorCode = "NetworkFailure"
	OperationErrorCodeInternalFailure              OperationErrorCode = "InternalFailure"

	//These our own codes
	//Happens when the dependency of resource command fails
	OperationErrorCodeDependencyFailure OperationErrorCode = "DependencyFailure"
	OperationErrorCodeUnforeseenError   OperationErrorCode = "UnforeseenError"
	OperationErrorCodePluginNotFound    OperationErrorCode = "PluginNotFound"
	OperationErrorCodeNotSet            OperationErrorCode = ""
)

var recoverableErrorCodes = map[OperationErrorCode]struct{}{
	OperationErrorCodeThrottling:           {},
	OperationErrorCodeNotStabilized:        {},
	OperationErrorCodeServiceInternalError: {},
	OperationErrorCodeServiceTimeout:       {},
	OperationErrorCodeNetworkFailure:       {},
	OperationErrorCodeInternalFailure:      {},
	OperationErrorCodeNotFound:             {},
}

func IsRecoverable(code OperationErrorCode) bool {
	_, found := recoverableErrorCodes[code]
	return found
}
