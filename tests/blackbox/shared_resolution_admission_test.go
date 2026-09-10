// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func changeResolutionCloud(t *testing.T, h *TestHarness, value string) {
	t.Helper()
	for id, entry := range h.GetCloudStateSnapshot(t) {
		var props map[string]any
		require.NoError(t, json.Unmarshal([]byte(entry.Properties), &props))
		props["Value"] = value
		raw, err := json.Marshal(props)
		require.NoError(t, err)
		h.PutCloudState(t, id, entry.ResourceType, string(raw))
	}
}
func reviewAbsorbAll(t *testing.T, h *TestHarness, f *pkgmodel.Forma) (pkgmodel.DriftResolution, *apimodel.SubmitCommandResponse) {
	t.Helper()
	_, err := h.client.ApplyForma(f, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
	var rejected *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
	require.ErrorAs(t, err, &rejected)
	resolution := pkgmodel.DriftResolution{ObservationID: rejected.Data.ObservationID}
	for _, stack := range rejected.Data.ModifiedStacks {
		for _, r := range stack.ModifiedResources {
			resolution.Decisions = append(resolution.Decisions, pkgmodel.DriftDecision{ResourceID: r.ResourceID, Action: "absorb"})
		}
	}
	preview, err := h.client.ApplyFormaWithResolution(f, pkgmodel.FormaApplyModeReconcile, true, clientID, resolution, "retain admission message")
	require.NoError(t, err)
	resolution.ReviewID = preview.Review.ReviewID
	resolution.IdempotencyKey = "admission-regression"
	return resolution, preview
}

func TestSharedResolutionBackgroundSynchronization(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		for _, changed := range []bool{false, true} {
			t.Run(fmt.Sprintf("changed_%t", changed), func(t *testing.T) {
				h := newTestHarness(t, 15*time.Second, true)
				defer h.Cleanup()
				f := SimpleForma(2)
				id := h.ApplyForma(f, pkgmodel.FormaApplyModeReconcile)
				require.Equal(t, "Success", h.WaitForCommandDone(id, 30*time.Second).State)
				changeResolutionCloud(t, h, "outside")
				baseline := h.SyncCommandBaseline()
				_, ok := h.WaitForSyncCommandAfter(baseline, 10*time.Second, 30*time.Second)
				require.True(t, ok)
				resolution, _ := reviewAbsorbAll(t, h, f)
				readBaseline := len(h.GetOperationLog(t))
				if changed {
					changeResolutionCloud(t, h, "changed again")
				}
				// No ForceSync: use the append-only provider log because unchanged
				// sync commands are deleted and may never be visible to a SQL poll.
				expected := "outside"
				if changed {
					expected = "changed again"
				}
				waitForPeriodicResolutionReads(t, h, readBaseline, expected)

				before := providerWrites(h.GetOperationLog(t))
				result, err := h.client.ApplyFormaWithResolution(f, pkgmodel.FormaApplyModeReconcile, false, clientID, resolution, "retain admission message")
				if changed {
					var rejected *apimodel.ErrorResponse[apimodel.DriftResolutionError]
					require.ErrorAs(t, err, &rejected)
					require.Equal(t, "stale-review", rejected.Data.Code)
				} else {
					require.NoError(t, err)
					require.Equal(t, "Success", h.WaitForCommandDone(result.CommandID, 30*time.Second).State)
				}
				require.Equal(t, before, providerWrites(h.GetOperationLog(t)))
			})
		}
	})
}

func TestSharedResolutionLargeReceiptRestart(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		f := SimpleForma(180)
		for i := range f.Resources {
			f.Resources[i].Label = fmt.Sprintf("resource-%04d", i)
			f.Resources[i].Properties = json.RawMessage(fmt.Sprintf(`{"Name":"resource-%04d","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`, i))
		}
		id := h.ApplyForma(f, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(id, 60*time.Second).State)
		changeResolutionCloud(t, h, "outside")
		baseline := h.SyncCommandBaseline()
		require.NoError(t, h.client.ForceSync())
		_, ok := h.WaitForSyncCommandAfter(baseline, 10*time.Second, 60*time.Second)
		require.True(t, ok)
		resolution, preview := reviewAbsorbAll(t, h, f)
		raw, err := json.Marshal(preview.Review)
		require.NoError(t, err)
		require.Greater(t, len(raw), datastore.MaxAdmissionReceiptBytes, "exercise real reviewed payload beyond receipt row budget")
		require.Len(t, resolution.Decisions, len(f.Resources))
		result, err := h.client.ApplyFormaWithResolution(f, pkgmodel.FormaApplyModeReconcile, false, clientID, resolution, "retain admission message")
		require.NoError(t, err)
		require.Equal(t, "Success", h.WaitForCommandDone(result.CommandID, 60*time.Second).State)
		require.Equal(t, preview.Review, result.Review)
		writes := providerWrites(h.GetOperationLog(t))
		require.Len(t, writes, len(f.Resources))
		h.cloudStateMirror = h.GetCloudStateSnapshot(t)
		h.KillAgent(t)
		h.RestartAgent(t, 15*time.Second)
		retry, err := h.client.ApplyFormaWithResolution(f, pkgmodel.FormaApplyModeReconcile, false, clientID, resolution, "retain admission message")
		require.NoError(t, err)
		require.Equal(t, result.CommandID, retry.CommandID)
		require.Equal(t, result.Review, retry.Review)
		require.Equal(t, "retain admission message", retry.Simulation.Command.Message)
		require.Equal(t, "Success", h.WaitForCommandDone(retry.CommandID, 5*time.Second).State)
		require.Empty(t, providerWrites(h.GetOperationLog(t)))
		desired, err := h.client.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Len(t, desired.Resources, len(f.Resources))
		for _, r := range desired.Resources {
			var props map[string]any
			require.NoError(t, json.Unmarshal(r.Properties, &props))
			require.Equal(t, "outside", props["Value"])
		}
		t.Logf("review bytes=%d resources=%d; durable replay retained review, delta, message, outcome and zero repeated writes", len(raw), len(f.Resources))
	})
}

func waitForPeriodicResolutionReads(t *testing.T, h *TestHarness, baseline int, expected string) {
	t.Helper()
	var readCount int
	require.Eventually(t, func() bool {
		entries := h.GetOperationLog(t)
		readIDs := map[string]bool{}
		for _, entry := range entries[baseline:] {
			if entry.Operation == "Read" {
				readIDs[entry.NativeID] = true
			}
		}
		readCount = len(readIDs)
		if readCount != 2 {
			return false
		}
		// All provider reads returned after the preview. Wait for their command's
		// persistence/completion too; deleted no-op commands count as completed.
		active, err := h.commandRowsFromDB("command = ? AND state NOT IN ('Success','Failed','Canceled')", "sync")
		if err != nil || len(active) != 0 {
			return false
		}
		inventory, err := h.client.ExtractResources("stack:default")
		if err != nil || len(inventory.Resources) != 2 {
			return false
		}
		for _, r := range inventory.Resources {
			var props map[string]any
			if json.Unmarshal(r.Properties, &props) != nil || props["Value"] != expected {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond, "periodic post-preview provider reads and their persisted completion must be observed")
	t.Logf("durable post-preview provider Read witness: %d distinct resources; sync completion and inventory=%s observed", readCount, expected)
}
