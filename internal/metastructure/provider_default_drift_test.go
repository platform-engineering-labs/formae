//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
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

func TestReconcileProviderDefaultPopulationDuringIndependentEdit(t *testing.T) {
	for _, timing := range []string{"on-create", "late-sync", "late-sync-with-tag"} {
		t.Run(timing, func(t *testing.T) {
			m, _, f, _ := scopedFixture(t)
			require.NoError(t, m.Datastore.StoreFormaCommand(&forma_command.FormaCommand{ID: "seed", StartTs: time.Now().Add(-time.Hour), ModifiedTs: time.Now().Add(-time.Hour), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess}, "seed"))
			r, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			r.Properties = json.RawMessage(`{"name":"before","metadata":{"environment":"dev"}}`)
			r.Schema = pkgmodel.Schema{Fields: []string{"name", "metadata", "defaultEncryptionScope", "denyEncryptionScopeOverride"}, Portable: true, Hints: map[string]pkgmodel.FieldHint{
				"defaultEncryptionScope":      {HasProviderDefault: true},
				"denyEncryptionScopeOverride": {HasProviderDefault: true},
			}}
			observed := json.RawMessage(`{"name":"before","metadata":{"environment":"dev"},"defaultEncryptionScope":"$account-encryption-key","denyEncryptionScopeOverride":false}`)
			if timing == "on-create" {
				r.Properties = observed
			}
			_, err = m.Datastore.StoreResource(r, "seed")
			require.NoError(t, err)
			r, err = m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			storeDesired(t, m.Datastore, *r, resource_update.OperationCreate, forma_command.CommandStateSuccess)
			commands, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			commands[0].ResourceUpdates[0].Version = r.Version
			require.NoError(t, m.Datastore.StoreFormaCommand(commands[0], commands[0].ID))
			f.Resources[0].Schema = r.Schema
			f.Resources[0].Properties = json.RawMessage(`{"name":"before","metadata":{"environment":"dev","app":"demo"}}`)
			if timing != "on-create" {
				if timing == "late-sync-with-tag" {
					observed = json.RawMessage(`{"name":"before","defaultEncryptionScope":"$account-encryption-key","denyEncryptionScopeOverride":false,"metadata":{"environment":"dev","oob":"drift"}}`)
				}
				r.Properties = observed
				cmd := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
				require.NoError(t, m.Datastore.StoreFormaCommand(cmd, cmd.ID))
				_, err = m.Datastore.StoreResource(r, cmd.ID)
				require.NoError(t, err)
			}
			preview, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "subject", "")
			if timing == "late-sync-with-tag" {
				var rejection apimodel.FormaReconcileRejectedError
				require.ErrorAs(t, err, &rejection)
				require.Len(t, rejection.ModifiedStacks["a"].ModifiedResources, 1)
				require.JSONEq(t, `[{"op":"add","path":"/metadata/oob","value":"drift"}]`, string(rejection.ModifiedStacks["a"].ModifiedResources[0].PatchDocument))
				for _, action := range []string{"absorb", "revert"} {
					resolved, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejection.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: action}}}}, "client", "subject", "")
					require.NoError(t, err, "filtered display must still support %s with the full observation", action)
					require.NotEmpty(t, resolved.Review.ReviewID)
				}
				return
			}
			require.NoError(t, err, "populating defaults must not require a drift decision")
			require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
			require.JSONEq(t, `[{"op":"add","path":"/metadata/app","value":"demo"}]`, string(preview.Simulation.Command.ResourceUpdates[0].PatchDocument))
		})
	}
}
