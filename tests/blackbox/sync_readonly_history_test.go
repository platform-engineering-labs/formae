// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestSyncReadOnlyHistory_Deterministic(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 10*time.Second)
		defer h.Cleanup()
		h.SetStrictMode(true)

		commandID := h.ApplyForma(SimpleForma(2), pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(commandID, 30*time.Second).State)
		managed := managedInventoryByLabel(t, h)
		observed := managed["res-a"]
		drifted := managed["res-b"]

		baseline := h.captureHistorySnapshot(t)
		for _, revision := range []string{"revision-1", "revision-2"} {
			before, after := h.observeManagedCloudResource(t, nil, observed.NativeID, observed.Type, revision)
			require.Equal(t, before.LatestByNativeID[observed.NativeID], after.LatestByNativeID[observed.NativeID])
			require.Len(t, after.Commands, len(before.Commands), "observation-only sync command is pruned")
			require.Len(t, after.Updates, len(before.Updates), "observation-only resource update is pruned")
			require.Len(t, after.Versions, len(before.Versions), "observation refresh reuses its physical version")
		}
		afterObservations := h.captureHistorySnapshot(t)
		require.Len(t, afterObservations.Commands, len(baseline.Commands))
		require.Len(t, afterObservations.Updates, len(baseline.Updates))
		require.Len(t, afterObservations.Versions, len(baseline.Versions))

		h.KillAgent(t)
		h.RestartAgent(t, 30*time.Second)
		requireObservedRevision(t, h, observed.NativeID, "revision-2")
		_, afterRestartObservation := h.observeManagedCloudResource(t, nil, observed.NativeID, observed.Type, "revision-3")
		require.Len(t, afterRestartObservation.Commands, len(baseline.Commands))
		require.Len(t, afterRestartObservation.Updates, len(baseline.Updates))
		require.Len(t, afterRestartObservation.Versions, len(baseline.Versions))

		// A later user write must keep the fresh provider observation and become
		// the physical row's owner. A subsequent read-only refresh must preserve
		// that user attribution while updating inventory freshness.
		beforeUserApply := h.captureHistorySnapshot(t)
		updated := SimpleForma(2)
		updated.Resources[0].Properties = json.RawMessage(
			`{"Name":"res-a","Value":"user-write","SetTags":[],"EntityTags":[],"OrderedItems":[]}`)
		userApplyID := h.ApplyForma(updated, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(userApplyID, 30*time.Second).State)
		requireObservedRevision(t, h, observed.NativeID, "revision-3")
		afterUserApply := h.captureHistorySnapshot(t)
		userOwned := afterUserApply.LatestByNativeID[observed.NativeID]
		require.NotEqual(t, beforeUserApply.LatestByNativeID[observed.NativeID].Version, userOwned.Version)
		require.Equal(t, userApplyID, userOwned.CommandID)
		require.Equal(t, historyCommandRow{Command: "apply", State: "Success", Source: "user"},
			afterUserApply.Commands[userApplyID])

		_, afterUserRefresh := h.observeManagedCloudResource(t, nil, observed.NativeID, observed.Type, "revision-4")
		require.Equal(t, userOwned, afterUserRefresh.LatestByNativeID[observed.NativeID],
			"read-only refresh preserves the later user write's version and attribution")

		// One synchronizer batch now contains both an observation-only refresh
		// and a real writable drift. The selected observation must keep its row,
		// while the batch remains in history because the other resource mints a
		// physical version.
		beforeMixed := h.captureHistorySnapshot(t)
		h.putObservedRevisionWithRetry(t, observed.NativeID, observed.Type, "revision-mixed")
		driftProps := cloudPropertiesWith(t, h, drifted.NativeID, "Value", "writable-drift")
		h.putCloudStateWithRetry(t, drifted.NativeID, drifted.Type, driftProps)
		readBaseline := len(h.GetOperationLog(t))
		require.True(t, h.forceSyncAndAwait(t, nil, 10*time.Second))
		require.NotNil(t, h.waitForObservedInventory(t, observed.NativeID, "revision-mixed", 10*time.Second))
		require.True(t, h.waitForAbsorbedInventory(t, "managed:true", drifted.NativeID, driftProps, false, 10*time.Second))
		require.True(t, h.readObservedSince(t, readBaseline, observed.NativeID), "observation freshness needs a subsequent provider Read")
		afterMixed := h.captureHistorySnapshot(t)
		require.Equal(t, beforeMixed.LatestByNativeID[observed.NativeID], afterMixed.LatestByNativeID[observed.NativeID])
		require.Greater(t, len(afterMixed.Commands), len(beforeMixed.Commands))
		require.Greater(t, len(afterMixed.Updates), len(beforeMixed.Updates))
		require.Greater(t, len(afterMixed.Versions), len(beforeMixed.Versions))
		require.NoError(t, newSuccessfulSynchronizerCommandsHaveDurableEvents(beforeMixed, afterMixed))
		driftOwned := afterMixed.LatestByNativeID[drifted.NativeID]
		require.NotEqual(t, beforeMixed.LatestByNativeID[drifted.NativeID].Version, driftOwned.Version)
		require.Equal(t, historyCommandRow{Command: "sync", State: "Success", Source: "synchronizer"},
			afterMixed.Commands[driftOwned.CommandID])

		_, afterDriftRefresh := h.observeManagedCloudResource(
			t, nil, drifted.NativeID, drifted.Type, "revision-drift-refresh")
		require.Equal(t, driftOwned, afterDriftRefresh.LatestByNativeID[drifted.NativeID],
			"read-only refresh preserves the config-drift version and command owner")
		delete(h.cloudStateMirror, observed.NativeID)
		delete(h.cloudStateMirror, drifted.NativeID)

		// A real provider deletion after read-only divergence must mint a
		// tombstone and retain the sync command/update that owns it.
		beforeDelete := h.captureHistorySnapshot(t)
		h.deleteCloudStateWithRetry(t, observed.NativeID)
		require.True(t, h.forceSyncAndAwait(t, nil, 10*time.Second))
		require.True(t, h.waitForAbsorbedInventory(t, "managed:true", observed.NativeID, "", true, 10*time.Second))
		afterDelete := h.captureHistorySnapshot(t)
		require.Greater(t, len(afterDelete.Commands), len(beforeDelete.Commands))
		require.Greater(t, len(afterDelete.Updates), len(beforeDelete.Updates))
		require.Greater(t, len(afterDelete.Versions), len(beforeDelete.Versions))
		require.Equal(t, "delete", afterDelete.LatestByNativeID[observed.NativeID].Operation)
		require.NoError(t, newSuccessfulSynchronizerCommandsHaveDurableEvents(beforeDelete, afterDelete))

		// Filter eviction is also a deletion event. Discover an unmanaged row,
		// make its next Read match the fixture filter, and verify the retained
		// history is backed by a fresh tombstone.
		const filteredID = "history-filtered"
		h.PutCloudState(t, filteredID, "Test::Generic::Resource",
			`{"Name":"history-filtered","Value":"before","SetTags":[],"EntityTags":[],"OrderedItems":[]}`)
		require.NoError(t, h.client.ForceDiscover())
		require.True(t, h.waitForInventoryNativeID(t, "managed:false", filteredID, true, 30*time.Second))
		beforeFilter := h.captureHistorySnapshot(t)
		filteredProps := cloudPropertiesWith(t, h, filteredID, "ExcludeFromDiscovery", "true")
		h.putCloudStateWithRetry(t, filteredID, "Test::Generic::Resource", filteredProps)
		require.True(t, h.forceSyncAndAwait(t, nil, 10*time.Second))
		require.True(t, h.waitForInventoryNativeID(t, "managed:false", filteredID, false, 10*time.Second))
		afterFilter := h.captureHistorySnapshot(t)
		require.Greater(t, len(afterFilter.Commands), len(beforeFilter.Commands))
		require.Greater(t, len(afterFilter.Updates), len(beforeFilter.Updates))
		require.Greater(t, len(afterFilter.Versions), len(beforeFilter.Versions))
		require.Equal(t, "delete", afterFilter.LatestByNativeID[filteredID].Operation)
		require.NoError(t, newSuccessfulSynchronizerCommandsHaveDurableEvents(beforeFilter, afterFilter))
	})
}

func managedInventoryByLabel(t *testing.T, h *TestHarness) map[string]pkgmodel.Resource {
	t.Helper()
	managed, _, err := h.extractManagedAndUnmanagedInventory()
	require.NoError(t, err)
	result := make(map[string]pkgmodel.Resource, len(managed))
	for _, resource := range managed {
		result[resource.Label] = resource
	}
	return result
}

func requireObservedRevision(t *testing.T, h *TestHarness, nativeID, revision string) {
	t.Helper()
	resource := h.waitForObservedInventory(t, nativeID, revision, 10*time.Second)
	require.NotNil(t, resource)
	cloud := h.GetCloudStateSnapshot(t)[nativeID]
	var properties map[string]any
	require.NoError(t, json.Unmarshal([]byte(cloud.Properties), &properties))
	require.Equal(t, revision, properties["ObservedRevision"])
}
