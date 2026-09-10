// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"github.com/stretchr/testify/require"
)

func TestResetAgentStateWithdrawsFailedDesiredCreate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		initial := SimpleForma(1)
		original := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)
		next := SimpleForma(2)
		next.Resources = next.Resources[1:]
		h.ProgramResponses(t, []testcontrol.PluginOpSequence{{MatchKey: next.Resources[0].Label, Operation: "Create", Steps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}}}})
		response, err := h.client.ApplyForma(next, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		require.NoError(t, err)
		failed := h.WaitForCommandDone(response.CommandID, 30*time.Second)
		require.Equal(t, "Failed", failed.State)
		inventory, err := h.client.ExtractResources("managed:true")
		require.NoError(t, err)
		require.True(t, inventory == nil || len(inventory.Resources) == 0, "old resource deleted and replacement create failed")
		desired, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Len(t, desired.Resources, 1, "failed create remains deliberate desired intent")

		h.ResetAgentState(t)
		response, err = h.client.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		require.NoError(t, err, "reset must leave a reusable stack label: %#v", err)
		require.Equal(t, "Success", h.WaitForCommandDone(response.CommandID, 30*time.Second).State)
		require.Equal(t, "Failed", h.WaitForCommandDone(failed.CommandID, 5*time.Second).State, "reset preserves original failed command history")
	})
}

func TestResetAgentStateWithdrawsFailedReferencedCreateBeforeDestroy(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		pool := NewResourcePool(5)
		initial := FormaFromPoolResources(pool, "default", "", []int{0}, defaultDestroyParentProps, defaultDestroyChildProps, nil, nil)
		original := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)

		desired := FormaFromPoolResources(pool, "default", "", []int{0, 1}, defaultDestroyParentProps, defaultDestroyChildProps, nil, nil)
		child := desired.Resources[1]
		h.ProgramResponses(t, []testcontrol.PluginOpSequence{{MatchKey: child.Label, Operation: "Create", Steps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}}}})
		response, err := h.client.ApplyForma(desired, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		require.NoError(t, err)
		failed := h.WaitForCommandDone(response.CommandID, 30*time.Second)
		require.Equal(t, "Failed", failed.State)

		inventory, err := h.client.ExtractResources("managed:true")
		require.NoError(t, err)
		require.Len(t, inventory.Resources, 1, "successful referenced producer remains live")
		remainingDesired, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Len(t, remainingDesired.Resources, 2, "complete desired state retains producer and failed consumer")

		beforeReset := len(h.GetOperationLog(t))
		h.ResetAgentState(t)
		var writes []string
		for _, entry := range h.GetOperationLog(t)[beforeReset:] {
			if entry.Operation == "Create" || entry.Operation == "Delete" {
				writes = append(writes, entry.Operation)
			}
		}
		require.NotEmpty(t, writes)
		require.Equal(t, []string{"Delete"}, writes, "reset withdraws the failed consumer and deletes only the live producer")
		require.Empty(t, h.GetCloudStateSnapshot(t))
		after := h.extractRemainingDesiredState(t)
		require.True(t, after == nil || len(after.Resources) == 0, "cleanup removes all surviving desired resources")

		next := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(next, 30*time.Second).State)
		require.Equal(t, "Failed", h.WaitForCommandDone(failed.CommandID, 5*time.Second).State, "reset preserves failed command history")
		require.Equal(t, "Success", h.WaitForCommandDone(original, 5*time.Second).State, "reset preserves successful command history")
	})
}

func TestResetAgentStateDestroysAlreadySettledDesiredState(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		initial := SimpleForma(1)
		original := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)

		beforeReset := len(h.GetOperationLog(t))
		h.ResetAgentState(t)
		var writes []string
		for _, entry := range h.GetOperationLog(t)[beforeReset:] {
			if entry.Operation == "Create" || entry.Operation == "Delete" {
				writes = append(writes, entry.Operation)
			}
		}
		require.Equal(t, []string{"Delete"}, writes, "settled desired state needs no recovery write before destroy")
		require.Empty(t, h.GetCloudStateSnapshot(t))
		require.Equal(t, "Success", h.WaitForCommandDone(original, 5*time.Second).State)
	})
}

func TestResetAgentStateAcceptsConfirmedSyncDeletion(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		initial := SimpleForma(1)
		original := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)
		snapshot := h.GetCloudStateSnapshot(t)
		require.Len(t, snapshot, 1)
		for id := range snapshot {
			h.DeleteCloudState(t, id)
		}
		baseline := h.SyncCommandBaseline()
		require.NoError(t, h.client.ForceSync())
		_, ok := h.WaitForSyncCommandAfter(baseline, 10*time.Second, 30*time.Second)
		require.True(t, ok)
		require.Eventually(t, func() bool {
			inventory, err := h.client.ListResourceSummaries("stack:default")
			return err == nil && len(inventory) == 0
		}, 5*time.Second, 20*time.Millisecond)
		desired, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Len(t, desired.Resources, 1, "sync deletion retains the resource's desired declaration")
		_, err = h.client.ApplyForma(desired, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
		var rejected *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		require.ErrorAs(t, err, &rejected, "ordinary soft reconcile must still reject confirmed drift")
		modifications := rejected.Data.ModifiedStacks["default"].ModifiedResources
		require.Len(t, modifications, 1)
		require.Equal(t, "delete", modifications[0].Operation)

		beforeReset := len(h.GetOperationLog(t))
		h.ResetAgentState(t)
		var writes []string
		for _, entry := range h.GetOperationLog(t)[beforeReset:] {
			if entry.Operation == "Create" || entry.Operation == "Delete" {
				writes = append(writes, entry.Operation)
			}
		}
		require.Empty(t, writes, "reset accepts confirmed deletion without recreating cloud resources")
		require.Empty(t, h.GetCloudStateSnapshot(t))
		response, err := h.client.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		require.NoError(t, err, "the next ordinary setup must succeed without force")
		require.Equal(t, "Success", h.WaitForCommandDone(response.CommandID, 30*time.Second).State)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 5*time.Second).State, "reset preserves original command history")
	})
}

func TestResetAgentStateWithdrawsOrphanedFailedCreate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		pool := NewResourcePool(5)
		initial := FormaFromPoolResources(pool, "default", "", []int{0}, defaultDestroyParentProps, defaultDestroyChildProps, nil, nil)
		original := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)

		desired := FormaFromPoolResources(pool, "default", "", []int{0, 1}, defaultDestroyParentProps, defaultDestroyChildProps, nil, nil)
		child := desired.Resources[1]
		h.ProgramResponses(t, []testcontrol.PluginOpSequence{{MatchKey: child.Label, Operation: "Create", Steps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}}}})
		response, err := h.client.ApplyForma(desired, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		require.NoError(t, err)
		failed := h.WaitForCommandDone(response.CommandID, 30*time.Second)
		require.Equal(t, "Failed", failed.State)

		inventory, err := h.client.ExtractResources("managed:true")
		require.NoError(t, err)
		require.Len(t, inventory.Resources, 1, "successful referenced producer remains live")
		remainingDesired, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Len(t, remainingDesired.Resources, 2, "complete desired state retains producer and failed consumer")

		destroyed, err := h.client.DestroyForma(initial, false, "cascade", clientID)
		require.NoError(t, err)
		require.Equal(t, "Success", h.WaitForCommandDone(destroyed.CommandID, 30*time.Second).State)
		inventory, err = h.client.ExtractResources("managed:true")
		require.NoError(t, err)
		require.True(t, inventory == nil || len(inventory.Resources) == 0)
		repair, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err, "failed desired intent must remain inspectable")
		require.Len(t, repair.Resources, 1)
		require.Equal(t, child.Label, repair.Resources[0].Label)
		require.NotEmpty(t, repair.Extraction.Diagnostics, "broken original dependency must be reported")
		require.Contains(t, string(repair.Resources[0].Properties), "$ref", "retain original identity instead of rebinding")

		before := len(h.GetOperationLog(t))
		h.ResetAgentState(t)
		after := h.extractRemainingDesiredState(t)
		require.True(t, after == nil || len(after.Resources) == 0)
		for _, op := range h.GetOperationLog(t)[before:] {
			require.NotContains(t, []string{"Create", "Update", "Delete"}, op.Operation, "reset must withdraw orphaned intent without provider writes")
		}
		require.Equal(t, "Failed", h.WaitForCommandDone(failed.CommandID, 5*time.Second).State)
	})
}
