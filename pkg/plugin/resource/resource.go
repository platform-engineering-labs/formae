// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource

import (
	"encoding/json"
)

type CreateRequest struct {
	ResourceType string
	Label        string // Resource label for identification (used by Azure as fallback, useful for testing)
	Properties   json.RawMessage
	TargetConfig json.RawMessage
}

type CreateResult struct {
	ProgressResult *ProgressResult
}

type UpdateRequest struct {
	NativeID          string
	ResourceType      string
	Label             string // Resource label for identification (used by Azure as fallback, useful for testing)
	PriorProperties   json.RawMessage
	DesiredProperties json.RawMessage
	PatchDocument     *string
	TargetConfig      json.RawMessage
}

type UpdateResult struct {
	ProgressResult *ProgressResult
}

type DeleteRequest struct {
	NativeID     string
	ResourceType string
	TargetConfig json.RawMessage
}

type DeleteResult struct {
	ProgressResult *ProgressResult
}

type StatusRequest struct {
	RequestID    string
	NativeID     string // NativeID returned by initial Create/Update
	ResourceType string // Optional resource type for status checks
	TargetConfig json.RawMessage
}

type StatusResult struct {
	ProgressResult *ProgressResult
}

type ReadRequest struct {
	NativeID     string
	ResourceType string
	TargetConfig json.RawMessage

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
	TargetConfig         json.RawMessage
	PageSize             int32
	PageToken            *string
	AdditionalProperties map[string]string
}

type ListResult struct {
	NativeIDs     []string
	NextPageToken *string
}

type ProgressResult struct {
	Operation       Operation
	OperationStatus OperationStatus

	RequestID          string
	NativeID           string             // Required - all plugins must set this
	ResourceProperties json.RawMessage    `json:"ResourceProperties,omitempty"`
	ErrorCode          OperationErrorCode `json:"ErrorCode,omitempty"`
	StatusMessage      string             `json:"StatusMessage,omitempty"`
}

func (pr *ProgressResult) Zero() ProgressResult {
	return ProgressResult{}
}

func (pr *ProgressResult) FinishedSuccessfully() bool {
	return pr.OperationStatus == OperationStatusSuccess
}

func (pr *ProgressResult) Failed() bool {
	return pr.OperationStatus == OperationStatusFailure
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
