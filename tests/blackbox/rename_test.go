// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/stretchr/testify/require"
)

// Deterministic rename coverage: create resources, rename one via the apply
// overlay, and assert the rename invariants against real inventory. The
// rapid-driven TestProperty_RenameViaApply explores rename under generated
// sequences, but whether any sequence exercises a rename depends on the
// draws; this test guarantees the rename path executes on every run.
func TestRenameViaApply_Deterministic(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()
		h.ResetAgentState(t)
		config := PropertyTestConfig{ResourceCount: 10, StackCount: 1, EnableRename: true}
		model := NewStateModel(config.StackCount, config.ResourceCount)
		h.SetupStacks(t, model, config)

		create := Operation{Kind: OpApply, ApplyMode: "reconcile", StackIndex: 0,
			ResourceIDs: []int{0, 1, 2}, Properties: defaultDestroyParentProps,
			ChildProperties: defaultDestroyChildProps, RenameSlotIndex: -1}
		h.ExecuteOperation(t, &create, model)
		h.DrainPendingCommands(t, model, 30*time.Second)
		require.Equal(t, StateExists, model.Resource(0, 0).State, "create must land before the rename")

		// Rename the parent (slot 0) while its child (slot 1) references it:
		// the overlay rewrites the child's $res reference to the new label.
		rename := Operation{Kind: OpApply, ApplyMode: "reconcile", StackIndex: 0,
			ResourceIDs: []int{0, 1, 2}, Properties: defaultDestroyParentProps,
			ChildProperties: defaultDestroyChildProps, RenameSlotIndex: 0, RenameNewLabel: "renamed-0"}
		h.ExecuteOperation(t, &rename, model)
		h.DrainPendingCommands(t, model, 30*time.Second)

		require.NotZero(t, h.RenamesAccepted, "the rename apply must be accepted")
		require.Equal(t, "renamed-0", model.Resource(0, 0).CurrentLabel)

		// A second rename of the same slot exercises the PreviousLabel chain.
		rename2 := Operation{Kind: OpApply, ApplyMode: "reconcile", StackIndex: 0,
			ResourceIDs: []int{0, 1, 2}, Properties: defaultDestroyParentProps,
			ChildProperties: defaultDestroyChildProps, RenameSlotIndex: 0, RenameNewLabel: "renamed-1"}
		h.ExecuteOperation(t, &rename2, model)
		h.DrainPendingCommands(t, model, 30*time.Second)

		h.TriggerSyncAndWait(t, model)
		h.AssertAllInvariants(t, model)

		managed, _, err := h.extractManagedAndUnmanagedInventory()
		require.NoError(t, err)
		var labels []string
		for _, r := range managed {
			if r.Stack == "stack-0" {
				labels = append(labels, r.Label)
			}
		}
		require.Contains(t, labels, "renamed-1", "inventory must carry the renamed label")
		require.NotContains(t, labels, "renamed-0", "the intermediate label must be gone")
		require.NotContains(t, labels, "res-stack-0-a", "the original label must be gone")
	})
}
