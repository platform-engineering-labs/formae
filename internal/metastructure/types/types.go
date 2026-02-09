// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package types

// FormaCommandSource indicates where a FormaCommand originated from
type FormaCommandSource string

const (
	FormaCommandSourceUser        FormaCommandSource = "user"
	FormaCommandSourceSynchronize FormaCommandSource = "synchronize"
	FormaCommandSourceDiscovery   FormaCommandSource = "discovery"
)

// OperationType is the high-level operation being performed on a resource or target.
// This is distinct from a DelegateCommand.
// An Operation can be Individual or Composite.
type OperationType string

const (
	// Individual ops
	OperationCreate OperationType = "create"
	OperationUpdate OperationType = "update"
	OperationDelete OperationType = "delete"
	OperationRead   OperationType = "read"

	// Composite ops
	OperationReplace OperationType = "replace" // delete + create
)

// ResourceUpdateState represents the current state of a resource update operation
type ResourceUpdateState string

const (
	ResourceUpdateStateUnknown    ResourceUpdateState = "Unknown"
	ResourceUpdateStateNotStarted ResourceUpdateState = "NotStarted"
	ResourceUpdateStatePending    ResourceUpdateState = "Pending"
	ResourceUpdateStateInProgress ResourceUpdateState = "InProgress"
	ResourceUpdateStateFailed     ResourceUpdateState = "Failed"
	ResourceUpdateStateSuccess    ResourceUpdateState = "Success"
	ResourceUpdateStateCanceled   ResourceUpdateState = "Canceled"
	ResourceUpdateStateRejected   ResourceUpdateState = "Rejected"
)

// TargetUpdateState represents the state of a target update
type TargetUpdateState string

const (
	TargetUpdateStateNotStarted TargetUpdateState = "NotStarted"
	TargetUpdateStateSuccess    TargetUpdateState = "Success"
	TargetUpdateStateFailed     TargetUpdateState = "Failed"
)

// StackUpdateState represents the state of a stack update
type StackUpdateState string

const (
	StackUpdateStateNotStarted StackUpdateState = "NotStarted"
	StackUpdateStateSuccess    StackUpdateState = "Success"
	StackUpdateStateFailed     StackUpdateState = "Failed"
)

// PolicyUpdateState represents the state of a policy update
type PolicyUpdateState string

const (
	PolicyUpdateStateNotStarted PolicyUpdateState = "NotStarted"
	PolicyUpdateStateSuccess    PolicyUpdateState = "Success"
	PolicyUpdateStateFailed     PolicyUpdateState = "Failed"
)
