//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestResolutionRevertOpaqueLiteral(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("original-secret")
	stored := fmt.Sprintf(`{"$visibility":"Opaque","$value":%q,"$hashed":true}`, digest)
	drifted := fmt.Sprintf(`{"$visibility":"Opaque","$value":%q,"$hashed":true}`, pkgmodel.ComputeValueHash("observed-secret"))
	for _, tc := range []struct {
		name, requested string
		independent     bool
		conflict        bool
		unavailable     bool
	}{
		{name: "original-plaintext", requested: `"original-secret"`},
		{name: "original-envelope", requested: `{"$visibility":"Opaque","$value":"original-secret","$strategy":"Update"}`},
		{name: "different-plaintext", requested: `"different-secret"`, conflict: true},
		{name: "digest-unavailable", requested: stored, unavailable: true},
		{name: "independent-with-unchanged-digest", requested: stored, independent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, f, _ := scopedFixture(t)
			r, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			r.Schema.Fields = []string{"name", "SecretString", "extra"}
			r.Schema.Hints = map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true, EdgeKind: pkgmodel.EdgeKindDefault}}
			r.Properties = json.RawMessage(fmt.Sprintf(`{"name":"before","SecretString":%s,"extra":"old"}`, stored))
			r.Version, err = m.Datastore.StoreResource(r, "before")
			require.NoError(t, err)
			storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
			commands, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			commands[0].ResourceUpdates[0].Version = r.Version
			require.NoError(t, m.Datastore.StoreFormaCommand(commands[0], commands[0].ID))
			sync := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
			require.NoError(t, m.Datastore.StoreFormaCommand(sync, sync.ID))
			liveSecret, extra := drifted, "old"
			if tc.independent {
				liveSecret, extra = stored, "new"
			}
			r.Properties = json.RawMessage(fmt.Sprintf(`{"name":"drifted","SecretString":%s,"extra":"old"}`, liveSecret))
			_, err = m.Datastore.StoreResource(r, sync.ID)
			require.NoError(t, err)
			f.Resources[0] = *ownPlanningValue(r)
			f.Resources[0].Properties = json.RawMessage(fmt.Sprintf(`{"name":"before","SecretString":%s,"extra":%q}`, tc.requested, extra))
			requestBefore := string(f.Resources[0].Properties)
			if tc.unavailable {
				// A digest-only write is refused before an observation can even
				// be issued; the public resolution path retains the same guard.
				for _, resolution := range []*pkgmodel.DriftResolution{nil, {ObservationID: "unavailable-input", Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "revert"}}}} {
					_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: resolution}, "client", "subject", "")
					require.ErrorIs(t, err, resolver.ErrHashedValueNotWritable)
				}
				return
			}
			observed := observeResolution(t, m, f)
			opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: observed.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "revert"}}}}
			preview, err := m.ApplyForma(f, opts, "client", "subject", "")
			require.Equal(t, requestBefore, string(f.Resources[0].Properties), "comparison must not mutate the supplied write input")
			if tc.conflict {
				var conflict apimodel.DriftResolutionError
				require.ErrorAs(t, err, &conflict)
				require.Equal(t, "decision-edit-conflict", conflict.Code)
				return
			}
			require.NoError(t, err)
			require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
			require.Equal(t, "update", preview.Simulation.Command.ResourceUpdates[0].Operation)
			plan, err := m.prepareGuardedApply(f, opts, "client", "subject", "")
			require.NoError(t, err)
			patch := string(plan.Command.ResourceUpdates[0].DesiredState.PatchDocument)
			require.NotContains(t, patch, digest, "a digest must never become a provider patch value")
			if tc.independent {
				require.NotContains(t, patch, "SecretString")
				require.Contains(t, patch, `"value":"new"`)
			} else {
				require.Contains(t, patch, `"value":"original-secret"`)
			}
		})
	}
}

func TestResolutionOpaqueLiteralExecutionAndTerminalHashing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/opaque.db"
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
		require.NoError(t, err)
		props := json.RawMessage(`{"foo":"bar","SecretString":"original-secret"}`)
		providerInput := make(chan json.RawMessage, 1)
		release := make(chan struct{})
		var released sync.Once
		var liveDrift atomic.Bool
		unblock := func() { released.Do(func() { close(release) }) }
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: r.Label, ResourceProperties: props}}, nil
			},
			Update: func(r *resource.UpdateRequest) (*resource.UpdateResult, error) {
				providerInput <- json.RawMessage(*r.PatchDocument)
				<-release
				liveDrift.Store(false)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: r.NativeID, ResourceProperties: props}}, nil
			},
			Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
				if liveDrift.Load() {
					return &resource.ReadResult{ResourceType: r.ResourceType, Properties: `{"foo":"bar","SecretString":"observed-secret"}`}, nil
				}
				return &resource.ReadResult{ResourceType: r.ResourceType, Properties: string(props)}, nil
			},
		}
		m := startScopedActor(t, ds, path, overrides)
		t.Cleanup(unblock) // release a blocked plugin before the actor cleanup
		f := scopedActorForma()
		f.Resources[0].Properties = props
		f.Resources[0].Schema.Fields = []string{"foo", "SecretString"}
		f.Resources[0].Schema.Hints = map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true, EdgeKind: pkgmodel.EdgeKindDefault}}
		first, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		waitForTerminal := func(id string) *forma_command.FormaCommand {
			require.Eventually(t, func() bool {
				c, e := ds.GetFormaCommandByCommandID(id)
				return e == nil && c.State == forma_command.CommandStateSuccess
			}, 5*time.Second, 10*time.Millisecond)
			c, e := ds.GetFormaCommandByCommandID(id)
			require.NoError(t, e)
			return c
		}
		assertTerminalHashed := func(c *forma_command.FormaCommand) {
			raw, e := json.Marshal(c)
			require.NoError(t, e)
			require.NotContains(t, string(raw), "original-secret", "terminal command must sanitize desired properties, patches and progress")
			require.NotContains(t, string(raw), "observed-secret", "read/actual values must remain sanitized")
			require.Contains(t, string(raw), pkgmodel.ComputeValueHash("original-secret"))
		}
		assertTerminalHashed(waitForTerminal(first.CommandID))
		rows, err := ds.LoadResourcesByStack("scope")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		r := rows[0]
		syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
		require.NoError(t, ds.StoreFormaCommand(syncCommand, syncCommand.ID))
		r.Properties = json.RawMessage(fmt.Sprintf(`{"foo":"bar","SecretString":{"$visibility":"Opaque","$strategy":"Update","$value":%q,"$hashed":true}}`, pkgmodel.ComputeValueHash("observed-secret")))
		_, err = ds.StoreResource(r, syncCommand.ID)
		require.NoError(t, err)
		liveDrift.Store(true)
		observed := observeResolution(t, m, f)
		opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: observed.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: r.Ksuid, Action: "revert"}}}}
		preview, err := m.ApplyForma(f, opts, "client", "subject", "")
		require.NoError(t, err)
		opts.Simulate = false
		opts.Resolution.ReviewID = preview.Review.ReviewID
		opts.Resolution.IdempotencyKey = "opaque-restoration"
		submitted, err := m.ApplyForma(f, opts, "client", "subject", "")
		require.NoError(t, err)
		select {
		case patch := <-providerInput:
			require.Contains(t, string(patch), `"value":"original-secret"`)
			require.NotContains(t, string(patch), pkgmodel.ComputeValueHash("original-secret"))
		case <-time.After(5 * time.Second):
			t.Fatal("provider update did not start")
		}
		pending, err := ds.GetFormaCommandByCommandID(submitted.CommandID)
		require.NoError(t, err)
		require.False(t, pending.IsInFinalState())
		require.Contains(t, string(pending.ResourceUpdates[0].DesiredState.Properties), "original-secret", "existing pending execution input is retained for resume")
		unblock()
		assertTerminalHashed(waitForTerminal(submitted.CommandID))
		actual, err := ds.LoadResourceById(r.Ksuid)
		require.NoError(t, err)
		require.NotContains(t, string(actual.Properties), "original-secret")
		require.Contains(t, string(actual.Properties), pkgmodel.ComputeValueHash("original-secret"))
	})
}
