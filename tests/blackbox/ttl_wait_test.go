// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
)

// TestForceCheckTTLAndWait_ObservesStackExpirerCommand sets an already-expired
// TTL policy on a stack, forces the TTL check, and confirms
// ForceCheckTTLAndWait applies the resulting destroy outcome to the model.
// The destroy command it waits on is stack-expirer sourced, so a wait that
// only polls the user-scoped command-status API can never see it complete
// and would leave the model stale (still expecting the resource to exist)
// while the real inventory no longer has it.
func TestForceCheckTTLAndWait_ObservesStackExpirerCommand(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()

		model := NewStateModel(1, 1)
		h.SetupStacks(t, model, PropertyTestConfig{})
		require.Equal(t, StateExists, model.Resource(0, 0).State, "setup should have created the resource")
		model.AcceptedCommands = nil

		op := &Operation{Kind: OpSetTTLPolicy, StackIndex: 0, TTLExpired: true}
		h.executeSetTTLPolicy(t, op, model)
		require.Len(t, model.AcceptedCommands, 1, "SetTTLPolicy should submit one apply command")
		policyCmd := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
		require.Equal(t, "Success", policyCmd.State, "setting the TTL policy should succeed")
		model.AcceptedCommands = nil

		// Let the 1-second TTL actually elapse.
		time.Sleep(2 * time.Second)

		h.ForceCheckTTLAndWait(t, model)

		require.Equal(t, StateNotExist, model.Resource(0, 0).State,
			"ForceCheckTTLAndWait should observe the stack-expirer destroy command and update the model")
		require.False(t, model.Stacks[0].TTLExpired, "TTLExpired should be cleared once the destroy command is observed")
	})
}
