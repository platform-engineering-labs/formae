//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestResolutionRevertReferenceExplicitClearVisibility(t *testing.T) {
	for _, tc := range []struct {
		name, source, visibility string
		conflict                 bool
	}{
		{name: "same-reference", source: "b", visibility: "Clear"},
		{name: "different-reference", source: "c", visibility: "Clear", conflict: true},
		{name: "changed-visibility", source: "b", visibility: "Opaque", conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, f, _ := scopedFixture(t)
			r, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			r.Properties = json.RawMessage(`{"name":{"$ref":"formae://b#/name","$value":"before"}}`)
			r.Version, err = m.Datastore.StoreResource(r, "before")
			require.NoError(t, err)
			storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
			commands, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			commands[0].ResourceUpdates[0].Version = r.Version
			require.NoError(t, m.Datastore.StoreFormaCommand(commands[0], commands[0].ID))
			sync := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now(), ModifiedTs: time.Now(), Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
			require.NoError(t, m.Datastore.StoreFormaCommand(sync, sync.ID))
			r.Properties = json.RawMessage(`{"name":"drifted"}`)
			_, err = m.Datastore.StoreResource(r, sync.ID)
			require.NoError(t, err)
			f.Resources[0] = *ownPlanningValue(r)
			f.Resources[0].Properties = json.RawMessage(fmt.Sprintf(`{"name":{"$res":true,"$stack":%q,"$label":%q,"$type":"Test::Resource","$property":"name","$visibility":%q}}`, tc.source, tc.source, tc.visibility))
			requestBefore := string(f.Resources[0].Properties)
			observation := observeResolution(t, m, f)
			opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: observation.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "revert"}}}}
			preview, err := m.ApplyForma(f, opts, "client", "subject", "")
			require.Equal(t, requestBefore, string(f.Resources[0].Properties))
			if tc.conflict {
				var conflict apimodel.DriftResolutionError
				require.ErrorAs(t, err, &conflict)
				require.Equal(t, "decision-edit-conflict", conflict.Code)
				require.Contains(t, conflict.Reason, "reverting")
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, preview.Review.ReviewID)
			require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
			require.JSONEq(t, `[{"op":"replace","path":"/name","value":"before"}]`, string(preview.Simulation.Command.ResourceUpdates[0].PatchDocument))
			opts.Simulate = false
			opts.Resolution.ReviewID = preview.Review.ReviewID
			plan, err := m.prepareGuardedApply(f, opts, "client", "subject", "")
			require.NoError(t, err)
			require.NoError(t, admitScopedPlan(t, m, plan))
		})
	}
}
