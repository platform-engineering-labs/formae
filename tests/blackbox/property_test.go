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
				ResourceCount:  5,
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

			// Final invariant check
			h.AssertAllInvariants(t)
		})
	})
}

func TestProperty_SequentialWithFailures(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  5,
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

			// After the sequence, clear all injections so the final
			// invariant check runs against a healthy system.
			h.ClearInjections(t)

			// Final invariant check — cloud state and inventory should
			// always be consistent, even after transient failures.
			h.AssertAllInvariants(t)
		})
	})
}

func TestProperty_ConcurrentMultiStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:     3,
				OperationCount:    Range{Min: 5, Max: 15},
				StackCount:        3,
				EnableConcurrency: true,
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

			h.AssertAllInvariants(t)
		})
	})
}

func TestProperty_ConcurrentWithFailures(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:     3,
				OperationCount:    Range{Min: 5, Max: 15},
				StackCount:        3,
				EnableConcurrency: true,
				EnableFailures:    true,
			}

			h.ResetAgentState(t)
			model := NewStateModel(config.StackCount, config.ResourceCount)
			ops := OperationSequenceGen(config).Draw(rt, "ops")

			for i, op := range ops {
				op.SequenceNum = i
				h.ExecuteOperation(t, &op, model)
			}

			// Clear injections so drain and final check run against a healthy system
			h.ClearInjections(t)
			h.DrainPendingCommands(t, model, defaultCommandTimeout)

			h.AssertAllInvariants(t)
		})
	})
}

func TestProperty_FullChaos(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()

		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:      3,
				OperationCount:     Range{Min: 5, Max: 15},
				StackCount:         3,
				EnableConcurrency:  true,
				EnableFailures:     true,
				EnableCloudChanges: true,
				EnableAutoReconcile: true,
				EnableTTL:          true,
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

			// Wind down: clear injections, drain pending commands
			h.ClearInjections(t)
			h.DrainPendingCommands(t, model, defaultCommandTimeout)

			// Force auto-reconcile on stacks that have the policy
			for i, stack := range model.Stacks {
				if stack.AutoReconcile {
					h.ForceReconcileAndWait(t, stack.Label, model, i)
				}
			}

			// Force TTL check for stacks with TTL policy
			h.ForceCheckTTLAndWait(t, model)

			// Synchronize cloud state with inventory before the final invariant
			// check. Chaos operations create expected inconsistencies:
			//   - OpCloudDelete removes cloud entries the agent still tracks
			//   - Failure injection can leave cloud entries from partial Creates
			h.SyncCloudStateWithInventory(t)

			h.AssertAllInvariants(t)
		})
	})
}
