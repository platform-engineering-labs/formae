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

	// PriorProperties carries the caller's last-known model for this resource
	// (the stored row), as an OPTIONAL hint. It is never required to satisfy a
	// Read: a plugin may ignore it entirely, and most should. A Read still
	// reports actual cloud state — this is not an input the read is computed
	// from.
	//
	// Its purpose is disambiguation, not data. Some resources have an ambiguous
	// boundary: an IAM role's inline policies, for example, are a separate,
	// queryable sub-resource, so "read the role" can legitimately mean with or
	// without them embedded. Which projection is correct depends on how the
	// caller models the resource — inline, versus as standalone child resources
	// — and only the caller's model knows that. A plugin may consult
	// PriorProperties to report the projection the caller manages, so a caller
	// that manages a child as a standalone resource is not shown phantom drift
	// (or driven to destroy it on a reconcile).
	//
	// Trade-off: this couples Read to the caller's model, which is a small leak
	// in an otherwise pure operation. Prefer designing resource types with an
	// unambiguous boundary so the hint is unnecessary; reach for it only where
	// the cloud API genuinely conflates a resource with its children.
	//
	// Empty when the caller has no prior state (create/status read-back,
	// discovery). Treat empty as "unknown" and fall back to default behaviour.
	PriorProperties json.RawMessage
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
	OperationStatusCanceled   OperationStatus = "Canceled"
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
	OperationErrorCodeResourceConflict:     {},
}

func IsRecoverable(code OperationErrorCode) bool {
	_, found := recoverableErrorCodes[code]
	return found
}
