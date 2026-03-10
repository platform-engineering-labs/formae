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
				OperationCount:       Range{Min: 5, Max: 15},
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

			h.AssertAllInvariants(t, model)
		})
	})
}
