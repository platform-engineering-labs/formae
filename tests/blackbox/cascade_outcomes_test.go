// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"github.com/stretchr/testify/require"
)

func TestDeletesUseDependentOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failed       ResourceSlotRef
		kind         OperationKind
		ids          []int
		onDependents string
	}{
		{"cascade_local", ResourceSlotRef{0, 2}, OpDestroy, []int{0}, "cascade"},
		{"cascade_cross_stack", ResourceSlotRef{1, 10}, OpDestroy, []int{0}, "cascade"},
		{"explicit_tree", ResourceSlotRef{0, 2}, OpDestroy, []int{0, 1, 2, 3, 4}, "abort"},
		{"reconcile_implicit", ResourceSlotRef{0, 2}, OpApply, []int{5}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := tc.failed
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				h := NewTestHarness(t, 10*time.Second)
				defer h.Cleanup()
				model := NewStateModel(2, 10)
				for si, ids := range [][]int{{0, 1, 2, 3, 4, 5}, {10, 11}} {
					if si == 1 && tc.onDependents != "cascade" {
						continue
					}
					op := Operation{Kind: OpApply, StackIndex: si, ApplyMode: "reconcile", ResourceIDs: ids,
						Properties: defaultDestroyParentProps, ChildProperties: defaultDestroyChildProps}
					h.ExecuteOperation(t, &op, model)
					require.Len(t, model.AcceptedCommands, 1)
					cmd := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
					require.Equal(t, "Success", cmd.State)
					h.DrainPendingCommands(t, model, 30*time.Second)
				}
				original := model.Resource(failed.StackIndex, failed.SlotIndex).Properties
				require.NotEmpty(t, model.GetNativeID(failed.StackIndex, failed.SlotIndex), "persisted create outcome must supply the injection key")
				op := Operation{Kind: tc.kind, StackIndex: 0, ResourceIDs: tc.ids, OnDependents: tc.onDependents, ApplyMode: "reconcile",
					Properties: defaultDestroyParentProps, ChildProperties: defaultDestroyChildProps,
					DrawnOutcomes: map[string]DrawnOutcome{outcomeKey(0, 0): {
						CRUDSteps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}},
					}, outcomeKey(failed.StackIndex, failed.SlotIndex): {
						CRUDSteps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}},
					}}}
				h.ExecuteOperation(t, &op, model)
				require.Len(t, model.AcceptedCommands, 1)
				require.Equal(t, StateExists, model.Resource(failed.StackIndex, failed.SlotIndex).State, "failed delete must preserve prediction")
				require.Equal(t, original, model.Resource(failed.StackIndex, failed.SlotIndex).Properties)
				require.Equal(t, StateExists, model.Resource(0, 0).State, "ancestor delete is blocked")
				cmd := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
				require.NotEqual(t, "Success", cmd.State, "dependent failure must actually reach the plugin")
				h.DrainPendingCommands(t, model, 30*time.Second)
				require.Equal(t, StateExists, model.Resource(failed.StackIndex, failed.SlotIndex).State)
				require.Equal(t, original, model.Resource(failed.StackIndex, failed.SlotIndex).Properties)
				require.Equal(t, StateNotExist, model.Resource(0, 4).State, "independent sibling delete succeeds")
				// The root's drawn error must not remain queued: its delete was
				// blocked, so no plugin operation could consume that response.
				op.DrawnOutcomes = nil
				h.ExecuteOperation(t, &op, model)
				require.Len(t, model.AcceptedCommands, 1)
				cmd = h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
				require.Equal(t, "Success", cmd.State, "blocked operations must not poison a later command")
				h.DrainPendingCommands(t, model, 30*time.Second)
				require.Equal(t, StateNotExist, model.Resource(0, 0).State)

			})
		})
	}
}

// Reconcile can update a cross-stack reference while deleting its provider.
// The provider's drawn delete failure must be consumed by that command.
func TestImplicitDeleteWithCrossStackDependentUsesDrawnOutcome(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()
		model := NewStateModel(2, 10)
		for si, ids := range [][]int{{0, 5}, {10}} {
			op := Operation{Kind: OpApply, StackIndex: si, ApplyMode: "reconcile", ResourceIDs: ids,
				Properties: defaultDestroyParentProps, ChildProperties: defaultDestroyChildProps}
			h.ExecuteOperation(t, &op, model)
			require.Len(t, model.AcceptedCommands, 1)
			cmd := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
			require.Equal(t, "Success", cmd.State)
			h.DrainPendingCommands(t, model, 30*time.Second)
		}
		op := Operation{Kind: OpApply, StackIndex: 0, ApplyMode: "reconcile", ResourceIDs: []int{5},
			Properties: defaultDestroyParentProps, ChildProperties: defaultDestroyChildProps,
			DrawnOutcomes: map[string]DrawnOutcome{outcomeKey(0, 0): {CRUDSteps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}}}}}
		h.ExecuteOperation(t, &op, model)
		require.Len(t, model.AcceptedCommands, 1)
		result := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
		require.Equal(t, "Failed", result.State)
		providerFailure := false
		for _, update := range result.ResourceUpdates {
			if update.StackName == "stack-0" && update.ResourceLabel == model.LabelForResource(0, 0) && update.Operation == "delete" {
				require.Equal(t, "Failed", update.State)
				require.Contains(t, update.ErrorMessage, "injected error")
				providerFailure = true
			}
		}
		require.True(t, providerFailure, "implicit provider delete must consume its drawn failure")
		h.DrainPendingCommands(t, model, 30*time.Second)
		destroy := Operation{Kind: OpDestroy, StackIndex: 0, ResourceIDs: []int{0}, OnDependents: "cascade"}
		h.ExecuteOperation(t, &destroy, model)
		require.Len(t, model.AcceptedCommands, 1)
		cmd := h.WaitForCommandDone(model.AcceptedCommands[0].CommandID, 30*time.Second)
		require.Equal(t, "Success", cmd.State, "a consumed reconcile error must not poison a later cascade")
		h.DrainPendingCommands(t, model, 30*time.Second)
	})
}
