// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"fmt"
	"time"

	"pgregory.net/rapid"
)

// OperationSequenceGen returns a rapid generator that produces a slice of
// operations whose kinds and parameters respect the given config.
func OperationSequenceGen(config PropertyTestConfig) *rapid.Generator[[]Operation] {
	return rapid.Custom(func(t *rapid.T) []Operation {
		count := rapid.IntRange(config.OperationCount.Min, config.OperationCount.Max).Draw(t, "count")
		ops := make([]Operation, count)
		for i := range ops {
			ops[i] = SingleOperationGen(config).Draw(t, fmt.Sprintf("op-%d", i))
		}
		return ops
	})
}

// SingleOperationGen returns a rapid generator that produces a single operation
// whose kind and parameters respect the given config.
func SingleOperationGen(config PropertyTestConfig) *rapid.Generator[Operation] {
	return rapid.Custom(func(t *rapid.T) Operation {
		kinds := allowedKinds(config)
		kindIdx := rapid.IntRange(0, len(kinds)-1).Draw(t, "kind")
		kind := kinds[kindIdx]

		op := Operation{Kind: kind}
		fillOperationFields(t, &op, config)
		return op
	})
}

// allowedKinds returns the set of operation kinds permitted by the config.
func allowedKinds(config PropertyTestConfig) []OperationKind {
	// Base operations always available
	kinds := []OperationKind{OpApply, OpDestroy, OpVerifyState, OpTriggerSync, OpTriggerDiscovery}

	if config.EnableFailures {
		kinds = append(kinds, OpInjectError, OpInjectLatency, OpClearInjections)
	}
	if config.EnableCloudChanges {
		kinds = append(kinds, OpCloudModify, OpCloudDelete, OpCloudCreate)
	}
	if config.EnableCancel {
		kinds = append(kinds, OpCancel)
	}

	return kinds
}

// fillOperationFields populates the kind-specific fields on the operation.
func fillOperationFields(t *rapid.T, op *Operation, config PropertyTestConfig) {
	switch op.Kind {
	case OpApply:
		op.ResourceIDs = resourceIDsGen(t, config.ResourceCount, 1)
		op.ApplyMode = applyModeGen(t, config)
		op.Blocking = blockingGen(t, config)

	case OpDestroy:
		op.ResourceIDs = resourceIDsGen(t, config.ResourceCount, 1)
		op.Blocking = blockingGen(t, config)

	case OpCancel:
		// CommandID is set during execution, nothing to generate

	case OpTriggerSync, OpTriggerDiscovery, OpVerifyState:
		// No additional fields needed

	case OpInjectError:
		op.TargetOperation = pluginOperationGen(t)
		op.ErrorMsg = errorMsgGen(t)
		op.ErrorCount = rapid.IntRange(0, 5).Draw(t, "errorCount")

	case OpInjectLatency:
		op.TargetOperation = pluginOperationGen(t)
		op.Latency = time.Duration(rapid.IntRange(50, 5000).Draw(t, "latencyMs")) * time.Millisecond

	case OpClearInjections:
		// No additional fields needed

	case OpCloudModify:
		op.NativeID = cloudNativeIDGen(t)
		op.Properties = cloudPropertiesGen(t)

	case OpCloudDelete:
		op.NativeID = cloudNativeIDGen(t)

	case OpCloudCreate:
		op.NativeID = cloudNativeIDGen(t)
		op.ResourceType = "Test::Generic::Resource"
		op.Properties = cloudPropertiesGen(t)
	}
}

// resourceIDsGen generates a non-empty slice of unique resource indices in [0, poolSize).
func resourceIDsGen(t *rapid.T, poolSize int, minCount int) []int {
	count := rapid.IntRange(minCount, poolSize).Draw(t, "resCount")

	// Generate unique indices by drawing from a shuffled pool
	all := make([]int, poolSize)
	for i := range all {
		all[i] = i
	}
	// Fisher-Yates shuffle using rapid
	for i := poolSize - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("shuffle-%d", i))
		all[i], all[j] = all[j], all[i]
	}
	return all[:count]
}

func applyModeGen(t *rapid.T, config PropertyTestConfig) string {
	if config.OnlyReconcile {
		return "reconcile"
	}
	modes := []string{"patch", "reconcile"}
	return modes[rapid.IntRange(0, len(modes)-1).Draw(t, "mode")]
}

func blockingGen(t *rapid.T, config PropertyTestConfig) bool {
	if !config.EnableConcurrency {
		return true
	}
	return rapid.Bool().Draw(t, "blocking")
}

func pluginOperationGen(t *rapid.T) string {
	ops := []string{"Create", "Read", "Update", "Delete"}
	return ops[rapid.IntRange(0, len(ops)-1).Draw(t, "pluginOp")]
}


func errorMsgGen(t *rapid.T) string {
	msgs := []string{
		"injected-transient-error",
		"injected-permanent-error",
		"injected-throttle-error",
		"injected-not-found-error",
	}
	return msgs[rapid.IntRange(0, len(msgs)-1).Draw(t, "errorMsg")]
}

func cloudNativeIDGen(t *rapid.T) string {
	return fmt.Sprintf("cloud-%d", rapid.IntRange(1, 100).Draw(t, "nativeID"))
}

func cloudPropertiesGen(t *rapid.T) string {
	name := fmt.Sprintf("cloud-res-%d", rapid.IntRange(1, 100).Draw(t, "propName"))
	value := fmt.Sprintf("v%d", rapid.IntRange(1, 10).Draw(t, "propValue"))
	return fmt.Sprintf(`{"Name":"%s","Value":"%s"}`, name, value)
}
