// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"github.com/stretchr/testify/require"
)

func requireSharedCapabilities(t *testing.T, h *TestHarness) {
	t.Helper()
	stats, err := h.client.Stats()
	require.NoError(t, err)
	for _, capability := range []string{"command-metadata", "shared-drift-resolution", "desired-stack-extraction"} {
		require.Contains(t, stats.Capabilities, capability)
	}
}

func TestSharedResolutionCapabilities(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		requireSharedCapabilities(t, h)
	})
}

func providerWrites(entries []testcontrol.OperationLogEntry) []testcontrol.OperationLogEntry {
	var writes []testcontrol.OperationLogEntry
	for _, entry := range entries {
		if entry.Operation == "Create" || entry.Operation == "Update" || entry.Operation == "Delete" {
			writes = append(writes, entry)
		}
	}
	return writes
}

func requireResourceValues(t *testing.T, resources []pkgmodel.Resource, values map[string]string) {
	t.Helper()
	require.Len(t, resources, len(values))
	for _, resource := range resources {
		var props map[string]any
		require.NoError(t, json.Unmarshal(resource.Properties, &props))
		require.Contains(t, values, resource.Label)
		require.Equal(t, values[resource.Label], props["Value"], resource.Label)
	}
}

func TestSharedResolutionRealAgentRestart(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		requireSharedCapabilities(t, h)
		original := h.ApplyForma(SimpleForma(2), pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)
		for id, entry := range h.GetCloudStateSnapshot(t) {
			var props map[string]any
			require.NoError(t, json.Unmarshal([]byte(entry.Properties), &props))
			props["Value"] = "outside"
			raw, err := json.Marshal(props)
			require.NoError(t, err)
			h.PutCloudState(t, id, entry.ResourceType, string(raw))
		}
		baseline := h.SyncCommandBaseline()
		require.NoError(t, h.client.ForceSync())
		_, ok := h.WaitForSyncCommandAfter(baseline, 10*time.Second, 30*time.Second)
		require.True(t, ok)
		fresh := api.NewClient(&pkgmodel.ClassicConnection{URL: "http://localhost", Port: h.port}, nil, nil)
		desired, err := fresh.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		require.Len(t, desired.Extraction.CompleteStacks, 1)
		requireResourceValues(t, desired.Resources, map[string]string{"res-a": "v1", "res-b": "v1"})
		desired.Resources = append(desired.Resources, SimpleForma(3).Resources[2])
		_, err = fresh.ApplyForma(desired, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
		var rejected *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		require.ErrorAs(t, err, &rejected)
		resolution := pkgmodel.DriftResolution{ObservationID: rejected.Data.ObservationID}
		for _, resource := range rejected.Data.ModifiedStacks["default"].ModifiedResources {
			action := "revert"
			if resource.Label == "res-a" {
				action = "absorb"
			}
			resolution.Decisions = append(resolution.Decisions, pkgmodel.DriftDecision{ResourceID: resource.ResourceID, Action: action})
		}
		require.Len(t, resolution.Decisions, 2)
		preview, err := fresh.ApplyFormaWithResolution(desired, pkgmodel.FormaApplyModeReconcile, true, clientID, resolution)
		require.NoError(t, err)
		require.NotNil(t, preview.Review)
		operations := map[string]string{}
		for _, update := range preview.Simulation.Command.ResourceUpdates {
			operations[update.ResourceLabel] = update.Operation
		}
		require.Equal(t, map[string]string{"res-a": "accept", "res-b": "update", "res-c": "create"}, operations)
		resolution.ReviewID = preview.Review.ReviewID
		resolution.IdempotencyKey = "external-mixed-retry"
		accepted, err := fresh.ApplyFormaWithResolution(desired, pkgmodel.FormaApplyModeReconcile, false, clientID, resolution)
		require.NoError(t, err)
		require.Equal(t, "Success", h.WaitForCommandDone(accepted.CommandID, 30*time.Second).State)
		values := map[string]string{"res-a": "outside", "res-b": "v1", "res-c": "v1"}
		central, err := fresh.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		requireResourceValues(t, central.Resources, values)
		inventory, err := fresh.ExtractResources("stack:default")
		require.NoError(t, err)
		requireResourceValues(t, inventory.Resources, values)
		cloud := h.GetCloudStateSnapshot(t)
		require.Len(t, cloud, 3)
		for _, entry := range cloud {
			var props map[string]any
			require.NoError(t, json.Unmarshal([]byte(entry.Properties), &props))
			require.Equal(t, values[props["Name"].(string)], props["Value"])
		}
		beforeRestart := providerWrites(h.GetOperationLog(t))
		require.Len(t, beforeRestart, 4, "two initial creates, one revert update, one new create")
		witness, err := json.Marshal(beforeRestart)
		require.NoError(t, err)
		t.Logf("provider writes retained before restart: %s", witness)
		// The test provider is in-memory. RestartAgent reconstructs its fixture;
		// retain the actual write witness and replace the stale OOB mirror first.
		h.cloudStateMirror = cloud
		h.KillAgent(t)
		h.RestartAgent(t, 15*time.Second)
		fresh = api.NewClient(&pkgmodel.ClassicConnection{URL: "http://localhost", Port: h.port}, nil, nil)
		retry, err := fresh.ApplyFormaWithResolution(desired, pkgmodel.FormaApplyModeReconcile, false, clientID, resolution)
		require.NoError(t, err)
		require.Equal(t, accepted.CommandID, retry.CommandID)
		require.Equal(t, "Success", h.WaitForCommandDone(retry.CommandID, 5*time.Second).State)
		require.Empty(t, providerWrites(h.GetOperationLog(t)), "retry must not issue duplicate provider writes")
		central, err = fresh.ExtractDesiredStacks("stack:default")
		require.NoError(t, err)
		requireResourceValues(t, central.Resources, values)
		t.Logf("immutable retry kept command %s; provider fixture was reconstructed, not persisted", retry.CommandID)
	})
}
