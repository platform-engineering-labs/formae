// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import "time"

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

	// Fault operations (dispatched via Ergo to TestController)

	OpInjectError      // inject a plugin error
	OpInjectLatency    // inject plugin latency
	OpClearInjections  // clear all injection rules

	// Verification

	OpVerifyState // query actual state and check invariants
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

	// For OpApply/OpDestroy: whether to wait for command completion.
	Blocking bool

	// For OpCancel: the command ID to cancel (set during execution).
	CommandID string

	// For OpInjectError: the error message and fire count (0 = permanent).
	ErrorMsg   string
	ErrorCount int

	// For OpInjectError/OpInjectLatency: which plugin operation to target.
	TargetOperation string // "Create", "Read", "Update", "Delete"

	// For OpInjectLatency: how long to delay.
	Latency time.Duration

	// For OpCloudModify/OpCloudCreate: the properties JSON to set.
	Properties string

	// For OpCloudCreate: the resource type.
	ResourceType string

	// For OpCloudModify/OpCloudDelete/OpCloudCreate: the native ID.
	NativeID string

	// Set during execution to track ordering.
	SequenceNum int
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

	// EnableFailures allows fault injection operations (OpInjectError, OpInjectLatency, OpClearInjections).
	EnableFailures bool

	// EnableCloudChanges allows out-of-band cloud operations (OpCloudModify, OpCloudDelete, OpCloudCreate).
	EnableCloudChanges bool

	// EnableConcurrency allows fire-and-forget (non-blocking) dispatches.
	EnableConcurrency bool

	// EnableCancel allows cancel operations.
	EnableCancel bool

	// OnlyReconcile forces all apply operations to use reconcile mode.
	OnlyReconcile bool
}
