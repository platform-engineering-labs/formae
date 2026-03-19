// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// OperationKind classifies the type of operation that can be generated
// by rapid and dispatched by the test harness.
type OperationKind int

const (
	// User operations (dispatched via REST API)

	OpApply   OperationKind = iota // apply a forma (patch or reconcile)
	OpDestroy                      // destroy resources via forma
	OpCancel                       // cancel an in-progress command

	// System operations (dispatched via admin REST API)

	OpTriggerSync      // force a synchronization run
	OpTriggerDiscovery // force a discovery run

	// Cloud operations (dispatched via Ergo to TestController)

	OpCloudModify // out-of-band modification of a resource
	OpCloudDelete // out-of-band deletion of a resource
	OpCloudCreate // out-of-band creation of a resource

	// Policy operations

	OpForceReconcile // force a reconcile on a stack with auto-reconcile policy
	OpCheckTTL       // check whether a TTL-enabled stack has expired
	OpSetTTLPolicy   // set TTL policy on a stack (expired or far-future)

	// Verification

	OpVerifyState // query actual state and check invariants

	// Crash injection

	OpCrashAgent // kill the agent with SIGKILL and restart it
)

// Operation represents a single step in a property-test operation sequence.
// Fields are populated based on Kind — not all fields apply to every kind.
type Operation struct {
	Kind OperationKind

	// For OpApply/OpDestroy: which resources from the pool to include.
	// Values are indices into the test's resource pool.
	ResourceIDs []int

	// For OpApply: "patch" or "reconcile".
	ApplyMode string

	// For OpApply/OpDestroy: which stack to target (index into StateModel.Stacks).
	StackIndex int

	// For OpCancel: the command ID to cancel (set during execution).
	CommandID string

	// For OpDestroy: "abort" or "cascade" (how to handle dependent resources).
	OnDependents string

	// For OpApply: properties template for child/grandchild resources.
	ChildProperties string

	// For OpCloudModify/OpCloudCreate: the properties JSON to set.
	Properties string

	// For OpCloudCreate: the resource type.
	ResourceType string

	// For OpCloudModify/OpCloudDelete/OpCloudCreate: the native ID.
	NativeID string

	// For cloud drift operations: whether the target is a managed inventory
	// resource or an unmanaged out-of-band resource.
	CloudTargetManaged bool

	// For OpSetTTLPolicy: true means set TTL to already-expired, false means far future.
	TTLExpired bool

	// For OpCloudCreate: child resources to create alongside the parent (OOB tuples).
	CloudChildren []CloudChildResource

	// For OpApply/OpDestroy: drawn plugin outcomes per resource slot.
	// Key format: "stackIdx:slotIdx". If a resource update has no entry, it succeeds.
	// nil map means no failure injection (all succeed).
	DrawnOutcomes map[string]DrawnOutcome

	// Set during execution to track ordering.
	SequenceNum int
}

// CloudChildResource represents a child resource in an out-of-band cloud create tuple.
type CloudChildResource struct {
	NativeID     string
	ResourceType string
	Properties   string
}

// DrawnOutcome holds the drawn plugin outcomes for a single resource slot.
// ReadSteps covers plugin Read phases, including:
//   - the pre-Read in Update/Delete chains
//   - ResolveCache reads of referenced resources before Create when the slot has
//     resolvables (for example parent/child relationships)
//
// CRUDSteps covers the Create/Update/Delete phase.
type DrawnOutcome struct {
	ReadSteps []testcontrol.ResponseStep // responses for Read in Update/Delete chains
	CRUDSteps []testcontrol.ResponseStep // responses for Create/Update/Delete step
}

// CommandKind classifies whether a pending command is an apply or destroy.
type CommandKind int

const (
	CommandKindApply CommandKind = iota
	CommandKindDestroy
	CommandKindReconcile
)

// ResourceSnapshot captures the state of a resource before a command modifies it.
type ResourceSnapshot struct {
	StackIndex int
	SlotIndex  int
	State      ResourceState
	Properties string
}

// AcceptedCommand tracks a command that was accepted by the agent during the chaos phase.
type AcceptedCommand struct {
	CommandID      string
	Snapshots      []ResourceSnapshot // pre-command state for revert on cancel
	OpLogSize      int                // operation log length immediately after acceptance
	RequestedSlots []ResourceSlotRef
	// Resolved is true when the cancel handler has already processed this
	// command. The command remains in AcceptedCommands so that
	// DrainPendingCommands can include its outcome when resolving conflicts
	// between overlapping commands (reverse-order processing).
	Resolved bool
}

type ResourceSlotRef struct {
	StackIndex int
	SlotIndex  int
}

// Range represents a min/max integer range for generators.
type Range struct {
	Min int
	Max int
}

// PropertyTestConfig controls what operations the rapid generator produces.
type PropertyTestConfig struct {
	// ResourceCount is the size of the resource pool available for operations.
	ResourceCount int

	// OperationCount is the range of how many operations to generate per sequence.
	OperationCount Range

	// EnableFailures enables per-resource-update failure injection via drawn response sequences.
	EnableFailures bool

	// EnableCloudChanges allows out-of-band cloud operations (OpCloudModify, OpCloudDelete, OpCloudCreate).
	EnableCloudChanges bool

	// EnableCancel allows cancel operations.
	EnableCancel bool

	// EnableForceReconcile enables OpForceReconcile operations.
	EnableForceReconcile bool

	// EnableTTL enables TTL policy on one stack.
	EnableTTL bool

	// EnableCrashInjection allows OpCrashAgent operations (kill -9 + restart).
	EnableCrashInjection bool

	// StackCount is the number of independent stacks (1 for sequential tests, 2-3 for concurrent).
	StackCount int
}
