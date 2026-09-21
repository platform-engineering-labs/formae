// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func multiFieldRevertFixture(t *testing.T) (*Metastructure, *pkgmodel.Forma, *config.FormaCommandConfig) {
	t.Helper()
	m, _, f, _ := scopedFixture(t)
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	r.Properties = json.RawMessage(`{"name":"before","alpha":"one","beta":"two","gamma":"three","stable":"same"}`)
	r.Schema.Fields = []string{"name", "alpha", "beta", "gamma", "stable"}
	_, err = m.Datastore.StoreResource(r, "seed-expanded")
	require.NoError(t, err)
	r, err = m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	commands[0].ResourceUpdates[0].Version = r.Version
	require.NoError(t, m.Datastore.StoreFormaCommand(commands[0], commands[0].ID))
	f.Resources[0].Properties = append(json.RawMessage(nil), r.Properties...)
	f.Resources[0].Schema = r.Schema
	r.Properties = json.RawMessage(`{"name":"drifted","alpha":"changed-one","beta":"changed-two","gamma":"changed-three","stable":"same"}`)
	syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(syncCommand, syncCommand.ID))
	_, err = m.Datastore.StoreResource(r, syncCommand.ID)
	require.NoError(t, err)
	observation := observeResolution(t, m, f)
	opts := &config.FormaCommandConfig{
		Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true,
		Resolution: &pkgmodel.DriftResolution{
			ObservationID: observation.ObservationID,
			Decisions:     []pkgmodel.DriftDecision{{ResourceID: "a", Action: "revert"}},
		},
	}
	return m, f, opts
}

func TestResolutionRevertReviewStableAcrossRepeatedPlanning(t *testing.T) {
	m, f, opts := multiFieldRevertFixture(t)
	first, err := m.ApplyForma(f, opts, "client", "subject", "")
	require.NoError(t, err)
	for i := 0; i < 30; i++ {
		next, err := m.ApplyForma(f, opts, "client", "subject", "")
		require.NoError(t, err)
		if first.Review.ReviewID != next.Review.ReviewID {
			t.Logf("first patch: %s", first.Simulation.Command.ResourceUpdates[0].PatchDocument)
			t.Logf("next patch: %s", next.Simulation.Command.ResourceUpdates[0].PatchDocument)
		}
		require.Equal(t, first.Review.ReviewID, next.Review.ReviewID, "unchanged declaration and datastore must retain the same review")
	}
}

func TestResolutionRevertReviewAdmitsOnlyReviewedPatch(t *testing.T) {
	m, f, opts := multiFieldRevertFixture(t)
	preview, err := m.ApplyForma(f, opts, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
	reviewedPatch := preview.Simulation.Command.ResourceUpdates[0].PatchDocument

	changed := ownPlanningValue(f)
	changed.Resources[0].Properties = json.RawMessage(`{"name":"before","alpha":"one","beta":"two","gamma":"three","stable":"edited"}`)
	changedOpts := ownPlanningValue(opts)
	changedOpts.Resolution.ObservationID = observeResolution(t, m, changed).ObservationID
	changedPreview, err := m.ApplyForma(changed, changedOpts, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, changedPreview.Simulation.Command.ResourceUpdates, 1)
	changedPatch := changedPreview.Simulation.Command.ResourceUpdates[0].PatchDocument
	require.Contains(t, string(changedPatch), `"value":"edited"`)
	require.NotEqual(t, reviewedPatch, changedPatch)
	require.NotEqual(t, preview.Review.ReviewID, changedPreview.Review.ReviewID)

	opts.Simulate = false
	opts.Resolution.ReviewID = preview.Review.ReviewID
	changedOpts.Simulate = false
	changedOpts.Resolution.ReviewID = preview.Review.ReviewID
	changedOpts.Resolution.IdempotencyKey = "changed-patch"
	commandsBefore, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	_, err = m.ApplyForma(changed, changedOpts, "client", "subject", "")
	var rejected apimodel.DriftResolutionError
	require.ErrorAs(t, err, &rejected)
	require.Equal(t, "stale-review", rejected.Code)
	require.Contains(t, rejected.Reason, "final plan")
	commandsAfter, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	require.Len(t, commandsAfter, len(commandsBefore))
	principal := fmt.Sprintf("subject:%x", sha256.Sum256([]byte(`["formae-apply-v1","subject"]`)))
	receipt, err := m.Datastore.(datastore.CommandAdmitter).LookupCommandAdmission(principal, changedOpts.Resolution.IdempotencyKey)
	require.NoError(t, err)
	require.Nil(t, receipt)

	plan, err := m.prepareGuardedApply(f, opts, "client", "subject", "")
	require.NoError(t, err, "the unchanged reviewed patch must reach admission")
	require.NoError(t, admitScopedPlan(t, m, plan))
	stored, err := m.Datastore.GetFormaCommandByCommandID(plan.Command.ID)
	require.NoError(t, err)
	require.Equal(t, preview.Review.ReviewID, stored.Resolution.ReviewID)
	require.Len(t, stored.ResourceUpdates, 1)
	require.Equal(t, reviewedPatch, stored.ResourceUpdates[0].DesiredState.PatchDocument)
}
