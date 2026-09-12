// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestDesiredRepairAfterDestroyOfFailedCreateDependency(t *testing.T) {
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

		// User explicitly abandons the failed child in the complete declaration.
		before := len(h.GetOperationLog(t))
		repair.Resources = nil
		repair.Extraction = nil
		resolved, err := h.client.ApplyForma(repair, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		require.NoError(t, err)
		require.NotEmpty(t, resolved.CommandID, "zero cloud work still records withdrawn intent")
		require.Equal(t, "Success", h.WaitForCommandDone(resolved.CommandID, 30*time.Second).State)
		after, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Empty(t, after.Resources)
		require.Empty(t, after.Extraction.Diagnostics)
		for _, op := range h.GetOperationLog(t)[before:] {
			require.NotContains(t, []string{"Create", "Update", "Delete"}, op.Operation, "withdrawal must not dispatch provider writes")
		}
		require.Equal(t, "Failed", h.WaitForCommandDone(failed.CommandID, 5*time.Second).State)
	})
}
