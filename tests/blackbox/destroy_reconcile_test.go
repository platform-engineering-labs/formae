// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"resty.dev/v3"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Run this deterministic acceptance check explicitly alongside the property
// target: a successful first apply must not hide rejection after reset.
func TestDestroyThenSoftReconcile(t *testing.T) {
	for _, scenario := range []string{"whole-stack", "empty-stack", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				h := NewTestHarness(t, 15*time.Second)
				defer h.Cleanup()
				forma := SimpleForma(2)
				forma.Stacks[0].Label = "destroy-recreate"
				for i := range forma.Resources {
					forma.Resources[i].Stack = "destroy-recreate"
				}
				apply := func(f *pkgmodel.Forma) string {
					t.Helper()
					response, err := h.client.ApplyForma(f, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
					require.NoError(t, err, "soft reconcile must accept explicit destroy intent: %#v", err)
					if response.Simulation.ChangesRequired {
						require.Equal(t, "Success", h.WaitForCommandDone(response.CommandID, 30*time.Second).State)
					}
					return response.CommandID
				}
				originalCommand := apply(forma)
				stacks, err := h.client.ListStacks()
				require.NoError(t, err)
				var originalID string
				for _, stack := range stacks {
					if stack.Label == "destroy-recreate" {
						originalID = stack.ID
					}
				}
				require.NotEmpty(t, originalID)
				destruction := &pkgmodel.Forma{Stacks: forma.Stacks, Resources: forma.Resources}
				if scenario == "partial" {
					destruction = &pkgmodel.Forma{Resources: forma.Resources[:1]}
				}
				destroyed, err := h.client.DestroyForma(destruction, false, "abort", clientID)
				require.NoError(t, err)
				require.True(t, destroyed.Simulation.ChangesRequired)
				require.Equal(t, "Success", h.WaitForCommandDone(destroyed.CommandID, 30*time.Second).State)
				stacks, err = h.client.ListStacks()
				require.NoError(t, err)
				var survivingID string
				for _, stack := range stacks {
					if stack.Label == "destroy-recreate" {
						survivingID = stack.ID
					}
				}
				if scenario == "partial" {
					require.Equal(t, originalID, survivingID)
				} else {
					require.Empty(t, survivingID, "terminal whole-stack destroy must actually remove stack")
				}
				expectedRemaining := 0
				if scenario == "partial" {
					expectedRemaining = 1
				}
				require.Len(t, h.GetCloudStateSnapshot(t), expectedRemaining)
				next := forma
				if scenario == "empty-stack" {
					next = &pkgmodel.Forma{Stacks: forma.Stacks}
				}
				if scenario == "partial" {
					next = &pkgmodel.Forma{Stacks: forma.Stacks, Resources: forma.Resources[1:], Targets: forma.Targets}
				}
				if scenario == "empty-stack" {
					for _, simulate := range []bool{true, false} {
						_, err = h.client.ApplyForma(next, pkgmodel.FormaApplyModeReconcile, simulate, clientID, false)
						var empty *apimodel.ErrorResponse[apimodel.FormaEmptyStackRejectedError]
						require.ErrorAs(t, err, &empty, "new empty stack must reach ordinary validation, not stale review")
						require.Equal(t, []string{"destroy-recreate"}, empty.Data.EmptyStacks)
					}
					stacks, err = h.client.ListStacks()
					require.NoError(t, err)
					for _, stack := range stacks {
						require.NotEqual(t, "destroy-recreate", stack.Label)
					}
					require.Empty(t, h.GetCloudStateSnapshot(t))
					next = forma
				}
				simulated, err := h.client.ApplyForma(next, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
				require.NoError(t, err, "simulation after destroy must accept: %#v", err)
				require.NotNil(t, simulated)
				apply(next)
				stacks, err = h.client.ListStacks()
				require.NoError(t, err)
				var recreatedID string
				for _, stack := range stacks {
					if stack.Label == "destroy-recreate" {
						recreatedID = stack.ID
					}
				}
				require.NotEmpty(t, recreatedID)
				if scenario == "partial" {
					require.Equal(t, originalID, recreatedID)
				} else {
					require.NotEqual(t, originalID, recreatedID, "reused label must have a new incarnation")
				}
				// Reintroduce the removed declarations, including after an empty-stack apply.
				apply(forma)
				require.Len(t, h.GetCloudStateSnapshot(t), 2)
				require.Equal(t, "Success", h.WaitForCommandDone(originalCommand, 5*time.Second).State, "history remains readable")
				require.Equal(t, "Success", h.WaitForCommandDone(destroyed.CommandID, 5*time.Second).State)
			})
		})
	}
}

// Sync starts with read operations, so discovering last-resource deletion must
// preserve the stack and its declaration for a central absorb/revert workflow.
func TestLastResourceSyncDeletionRetainsDesiredReview(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		forma := SimpleForma(1)
		original := h.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile)
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
		requireSharedCapabilities(t, h)
		extract := func() *pkgmodel.Forma {
			t.Helper()
			desired, err := h.client.ExtractDesiredStacks("stack:default")
			require.NoError(t, err)
			require.NotNil(t, desired.Extraction)
			require.Len(t, desired.Extraction.CompleteStacks, 1)
			return desired
		}
		desired := extract()
		require.Len(t, desired.Resources, 1, "last sync deletion must preserve durable desired declaration")
		_, err := h.client.ApplyForma(desired, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
		var rejected *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		require.ErrorAs(t, err, &rejected)
		modifications := rejected.Data.ModifiedStacks["default"].ModifiedResources
		require.Len(t, modifications, 1)
		require.Equal(t, "delete", modifications[0].Operation)
		resolution := pkgmodel.DriftResolution{ObservationID: rejected.Data.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: modifications[0].ResourceID, Action: "revert"}}}
		submit := func(simulate bool) *apimodel.SubmitCommandResponse {
			t.Helper()
			result, err := h.client.ApplyFormaWithResolution(desired, pkgmodel.FormaApplyModeReconcile, simulate, clientID, resolution)
			require.NoError(t, err)
			return result
		}
		reverted := submit(true)
		require.Len(t, reverted.Simulation.Command.ResourceUpdates, 1)
		require.Equal(t, "create", reverted.Simulation.Command.ResourceUpdates[0].Operation)
		resolution.Decisions[0].Action = "absorb"
		absorbed := submit(true)
		require.NotNil(t, absorbed.Review)
		resolution.ReviewID = absorbed.Review.ReviewID
		resolution.IdempotencyKey = "last-sync-delete"
		accepted := submit(false)
		require.Equal(t, "Success", h.WaitForCommandDone(accepted.CommandID, 30*time.Second).State)
		require.Empty(t, extract().Resources, "absorbed deletion yields a complete empty desired stack")
	})
}

func TestLastResourcePatchReplacementFailureRetainsDesired(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		forma := SimpleForma(1)
		forma.Resources[0].Schema.Hints = map[string]pkgmodel.FieldHint{"Value": {CreateOnly: true}}
		original := h.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)
		forma.Resources[0].Properties = bytes.ReplaceAll(forma.Resources[0].Properties, []byte(`"v1"`), []byte(`"v2"`))
		h.ProgramResponses(t, []testcontrol.PluginOpSequence{{MatchKey: "res-a", Operation: "Create", Steps: []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}}}})
		preview, err := h.client.ApplyForma(forma, pkgmodel.FormaApplyModePatch, true, clientID, false)
		require.NoError(t, err)
		require.Len(t, preview.Simulation.Command.ResourceUpdates, 2)
		require.Equal(t, "delete", preview.Simulation.Command.ResourceUpdates[0].Operation)
		require.Equal(t, "create", preview.Simulation.Command.ResourceUpdates[1].Operation)
		response, err := h.client.ApplyForma(forma, pkgmodel.FormaApplyModePatch, false, clientID, false)
		require.NoError(t, err)
		require.Equal(t, "Failed", h.WaitForCommandDone(response.CommandID, 30*time.Second).State)
		require.Empty(t, h.GetCloudStateSnapshot(t), "replacement delete succeeded before create failed")
		httpClient := resty.New().SetTimeout(10 * time.Second)
		defer func() { _ = httpClient.Close() }()
		var desired pkgmodel.Forma
		got, err := httpClient.R().SetQueryParams(map[string]string{"query": "stack:default", "state": "desired"}).SetResult(&desired).Get(fmt.Sprintf("http://localhost:%d/api/v1/resources", h.port))
		require.NoError(t, err)
		defer func() { _ = got.Body.Close() }()
		require.Equal(t, 200, got.StatusCode(), got.String())
		require.Len(t, desired.Resources, 1, "provisional failed patch must not erase prior durable intent")
		_, err = h.client.ApplyForma(&desired, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
		var rejected *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		require.ErrorAs(t, err, &rejected)
		modifications := rejected.Data.ModifiedStacks["default"].ModifiedResources
		require.Len(t, modifications, 1)
		require.Equal(t, "delete", modifications[0].Operation)
		resolution := pkgmodel.DriftResolution{ObservationID: rejected.Data.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: modifications[0].ResourceID, Action: "revert"}}}
		for _, simulate := range []bool{true, false} {
			raw, err := json.Marshal(desired)
			require.NoError(t, err)
			controls, err := json.Marshal(resolution)
			require.NoError(t, err)
			var result apimodel.SubmitCommandResponse
			response, err := httpClient.R().SetResult(&result).SetHeader("Client-ID", clientID).
				SetFormData(map[string]string{"command": "apply", "mode": "reconcile", "simulate": fmt.Sprint(simulate), "resolution": string(controls)}).
				SetFileReader("file", "forma.json", bytes.NewReader(raw)).Post(fmt.Sprintf("http://localhost:%d/api/v1/commands", h.port))
			require.NoError(t, err)
			require.Contains(t, []int{200, 202}, response.StatusCode(), response.String())
			require.NoError(t, response.Body.Close())
			if simulate {
				require.NotNil(t, result.Review)
				require.Len(t, result.Simulation.Command.ResourceUpdates, 1)
				require.Equal(t, "create", result.Simulation.Command.ResourceUpdates[0].Operation)
				resolution.ReviewID = result.Review.ReviewID
				resolution.IdempotencyKey = "failed-patch-revert"
			} else {
				require.Equal(t, "Success", h.WaitForCommandDone(result.CommandID, 30*time.Second).State)
			}
		}
		restored := h.GetCloudStateSnapshot(t)
		require.Len(t, restored, 1)

	})
}
