// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package types

// FormaCommandSource indicates where a FormaCommand originated from
type FormaCommandSource string

const (
	FormaCommandSourceUser                FormaCommandSource = "user"
	FormaCommandSourceSynchronize         FormaCommandSource = "synchronize"
	FormaCommandSourceDiscovery           FormaCommandSource = "discovery"
	FormaCommandSourcePolicyAutoReconcile FormaCommandSource = "policy:auto-reconcile"
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

	// OperationReaped tombstones a resource whose target was reaped. It is
	// distinct from OperationDelete: a reap records that the provider was never
	// asked to delete the resource (the target became unreachable and was
	// reaped), whereas a delete records an intentional provider-side removal.
	// A reaped row is retained in the resources table but is invisible to every
	// live-resource query, exactly like a delete tombstone.
	OperationReaped OperationType = "reaped"

	// Composite ops
	OperationReplace OperationType = "replace" // delete + create

	// OperationResolve is a synthetic op that resolves opaque $ref config values
	// in-memory for an otherwise-unchanged target. It is never persisted and does
	// not trigger discovery or cloud-side mutations.
	OperationResolve OperationType = "resolve"

	// OperationDraw is a synthetic op that draws a generator's value in
	// memory for the destinations bound to it. Like OperationResolve it is
	// never persisted and never reaches a provider: it exists only to
	// produce a value the destinations in the same changeset consume. The
	// generator's own row is created, updated or deleted by the ordinary
	// Create/Update/Delete ops, which a draw never stands in for.
	OperationDraw OperationType = "draw"
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

// TerminalStates is the authoritative set of ResourceUpdateState values from which
// no further state transitions are permitted. Used by datastore backends to
// implement monotonic terminality (CAS guard).
var TerminalStates = []ResourceUpdateState{
	ResourceUpdateStateSuccess,
	ResourceUpdateStateFailed,
	ResourceUpdateStateRejected,
	ResourceUpdateStateCanceled,
}

// TargetUpdateState represents the state of a target update
type TargetUpdateState string

const (
	TargetUpdateStateNotStarted TargetUpdateState = "NotStarted"
	TargetUpdateStateInProgress TargetUpdateState = "InProgress"
	TargetUpdateStateSuccess    TargetUpdateState = "Success"
	TargetUpdateStateFailed     TargetUpdateState = "Failed"
	TargetUpdateStateCanceled   TargetUpdateState = "Canceled"
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

// GeneratorUpdateState represents the state of a generator update.
//
// InProgress is required to make a generator update schedulable as an
// ExecutionDAG node: without a state distinct from NotStarted/Success/Failed,
// MarkInProgress and IsRunning cannot be told apart, and GetExecutableUpdates,
// findRunningUpdate, and handleUpdateFinished all misbehave.
//
// Canceled is deliberately NOT included, unlike TargetUpdateState (whose
// terminal set is Success | Failed | Canceled per isTargetInFinalState). A
// target update reaches Canceled because the changeset executor's cancel
// paths (changeset_executor.go's cancel/forceCancel) range over
// data.changeset.DAG.Nodes and type-switch on *target_update.TargetUpdate
// (and *resource_update.ResourceUpdate). Nothing in this codebase adds a
// *generator_update.GeneratorUpdate to that DAG yet, and neither cancel
// switch has a case for it, so a generator node cannot reach Canceled today.
// Add it if and when a later change wires GeneratorUpdate into DAG
// construction and the executor's cancel switch.
type GeneratorUpdateState string

const (
	GeneratorUpdateStateNotStarted GeneratorUpdateState = "NotStarted"
	GeneratorUpdateStateInProgress GeneratorUpdateState = "InProgress"
	GeneratorUpdateStateSuccess    GeneratorUpdateState = "Success"
	GeneratorUpdateStateFailed     GeneratorUpdateState = "Failed"
)
