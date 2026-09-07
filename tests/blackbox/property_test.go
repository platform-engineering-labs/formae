// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build property

package blackbox

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"pgregory.net/rapid"
)

func TestProperty_SequentialHappyPath(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  10,
				OperationCount: Range{Min: 3, Max: 10},
				StackCount:     1,
			}

			// Reset agent state so each iteration starts clean
			h.ResetAgentState(t)

			model := NewStateModel(config.StackCount, config.ResourceCount)
			ops := OperationSequenceGen(config).Draw(rt, "ops")

			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
			}

			// Drain pending fire-and-forget commands before final check
			h.DrainPendingCommands(t, model, defaultCommandTimeout)

			h.AssertAllInvariants(t, model)
		})
	})
}

func TestProperty_SequentialWithFailures(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  10,
				OperationCount: Range{Min: 3, Max: 10},
				StackCount:     1,
				EnableFailures: true,
			}

			// Reset agent state so each iteration starts clean
			h.ResetAgentState(t)

			model := NewStateModel(config.StackCount, config.ResourceCount)
			ops := OperationSequenceGen(config).Draw(rt, "ops")

			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
			}

			h.DrainPendingCommands(t, model, defaultCommandTimeout)

			h.AssertAllInvariants(t, model)
		})
	})
}

func TestProperty_ConcurrentMultiStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  10,
				OperationCount: Range{Min: 5, Max: 15},
				StackCount:     3,
			}

			h.ResetAgentState(t)
			model := NewStateModel(config.StackCount, config.ResourceCount)
			ops := OperationSequenceGen(config).Draw(rt, "ops")

			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
			}

			// Drain all pending fire-and-forget commands before final check
			h.DrainPendingCommands(t, model, defaultCommandTimeout)

			h.AssertAllInvariants(t, model)
		})
	})
}

func TestProperty_ConcurrentWithFailures(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  10,
				OperationCount: Range{Min: 5, Max: 15},
				StackCount:     3,
				EnableFailures: true,
			}

			h.ResetAgentState(t)
			model := NewStateModel(config.StackCount, config.ResourceCount)
			ops := OperationSequenceGen(config).Draw(rt, "ops")

			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
			}

			// Drain pending fire-and-forget commands before final check
			h.DrainPendingCommands(t, model, defaultCommandTimeout)

			h.AssertAllInvariants(t, model)
		})
	})
}

func TestProperty_FullChaos(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 30*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:        10,
				OperationCount:       Range{Min: 10, Max: 30},
				StackCount:           3,
				EnableFailures:       true,
				EnableCloudChanges:   true,
				EnableCancel:         true,
				EnableForceReconcile: true,
				EnableTTL:            true,
				EnableCrashInjection: true,
				EnableRename:         true,
			}

			h.ResetAgentState(t)
			model := NewStateModel(config.StackCount, config.ResourceCount)

			// Set up stacks with resources and policies
			h.SetupStacks(t, model, config)

			ops := OperationSequenceGen(config).Draw(rt, "ops")
			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
			}

			// Wind down: drain pending commands
			h.DrainPendingCommands(t, model, 30*time.Second)

			// Sync cloud state with inventory. Chaos operations (cancel, crash,
			// TTL cascade) can leave cloud entries that the ResourcePersister
			// hasn't cleaned up yet, causing phantom violations.
			h.TriggerSyncAndWait(t, model)

			h.AssertAllInvariants(t, model)
		})
	})
}

// TestProperty_RenameViaApply exercises the rename-folded-into-OpApply
// path in isolation. Single stack, applies only (reconcile + patch),
// EnableRename on, no chaos ops. The generator may decide on each apply
// whether to also rename one slot from ResourceIDs; combined with the
// usual property template the apply models an update as label-only,
// property-only, or both.
//
// This is the focused regression for RFC-0041: any rename-shaped failure
// (duplicate NativeID, old label still in inventory, slot label drift
// from the overlay) fires from CheckInvariants + CheckRenameInvariants
// inside AssertAllInvariants.
func TestProperty_RenameViaApply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  10,
				OperationCount: Range{Min: 3, Max: 10},
				StackCount:     1,
				EnableRename:   true,
			}

			h.ResetAgentState(t)
			model := NewStateModel(config.StackCount, config.ResourceCount)
			h.SetupStacks(t, model, config)

			ops := OperationSequenceGen(config).Draw(rt, "ops")
			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
				// Drain after every operation: with a single stack, a second
				// apply submitted while the first is in flight is rejected by
				// the agent's stack-overlap admission check. Rename needs the
				// slot to already exist, i.e. exactly those later applies —
				// serializing keeps them (and their renames) landing.
				h.DrainPendingCommands(t, model, 30*time.Second)
			}

			h.TriggerSyncAndWait(t, model)
			h.AssertAllInvariants(t, model)
		})

		// Whether any generated sequence exercises a rename depends on the
		// draws (the slot must already exist when the rename-carrying apply
		// executes), so the count is informational here. Guaranteed rename
		// coverage lives in TestRenameViaApply_Deterministic.
		t.Logf("TestProperty_RenameViaApply: %d renames accepted across the run", h.RenamesAccepted)
	})
}
