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
			h.TriggerSyncAndWait(t)

			h.AssertAllInvariants(t, model)
		})
	})
}

// TestProperty_Rename exercises ONLY the OpRename operation against a
// stack of pre-applied resources. No other chaos ops (no applies, no
// destroys, no cloud drift, no crashes). The intent is to isolate the
// rename code path so any invariant failure points squarely at rename
// logic, not at interaction with another op.
//
// Sequence per rapid sample:
//  1. ResetAgentState, NewStateModel, SetupStacks — gives us a stack
//     with resources already in StateExists.
//  2. Draw N OpRename ops on slots that exist. Each op picks a slot
//     and a fresh label.
//  3. Execute the renames in order.
//  4. DrainPendingCommands + TriggerSyncAndWait + AssertAllInvariants
//     (which runs CheckInvariants — duplicate-NativeID guard — and
//     CheckRenameInvariants — no old-label-still-current guard).
func TestProperty_Rename(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			// Config drives only resource pool size + rename count. No
			// other ops are enabled.
			config := PropertyTestConfig{
				ResourceCount: 10,
				StackCount:    1,
			}

			h.ResetAgentState(t)
			model := NewStateModel(config.StackCount, config.ResourceCount)
			h.SetupStacks(t, model, config)

			// Draw a small number of rename operations directly. We do NOT
			// use OperationSequenceGen here because that draws from
			// allowedKinds and would mix other ops in.
			pool := resourcePoolForConfig(config)
			slotCount := slotCountForConfig(config, pool)
			renameCount := rapid.IntRange(1, 5).Draw(rt, "renameCount")

			for i := 0; i < renameCount; i++ {
				op := Operation{
					Kind:            OpRename,
					StackIndex:      0,
					RenameSlotIndex: renameSlotIndexGen(rt, config, pool, slotCount),
					RenameNewLabel:  renameLabelGen(rt),
					SequenceNum:     i,
				}
				h.ExecuteOperation(t, &op, model)
			}

			h.DrainPendingCommands(t, model, 30*time.Second)
			h.TriggerSyncAndWait(t)
			h.AssertAllInvariants(t, model)
		})
	})
}
