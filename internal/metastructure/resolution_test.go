//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func resolutionFixture(t *testing.T) (*Metastructure, *pkgmodel.Forma) {
	t.Helper()
	m, _, f, _ := scopedFixture(t)
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	commands[0].ResourceUpdates[0].Version = r.Version
	require.NoError(t, m.Datastore.StoreFormaCommand(commands[0], commands[0].ID))
	f.Resources[0].Properties = append(json.RawMessage(nil), r.Properties...)
	r.Properties = []byte(`{"name":"drifted"}`)
	syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(syncCommand, syncCommand.ID))
	_, err = m.Datastore.StoreResource(r, syncCommand.ID)
	require.NoError(t, err)
	return m, f
}
func TestResolutionObservationPublicIdentity(t *testing.T) {
	m, f := resolutionFixture(t)
	_, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "subject", "")
	var rejected apimodel.FormaReconcileRejectedError
	require.ErrorAs(t, err, &rejected)
	raw, err := json.Marshal(rejected)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.NotEmpty(t, payload["ObservationID"], "a rejection must supply a reusable observation identity")
	mods := payload["ModifiedStacks"].(map[string]any)["a"].(map[string]any)["ModifiedResources"].([]any)
	require.Equal(t, "a", mods[0].(map[string]any)["ResourceID"])
	require.NotEmpty(t, mods[0].(map[string]any)["ObservedVersion"])
}
func observeResolution(t *testing.T, m *Metastructure, f *pkgmodel.Forma) apimodel.FormaReconcileRejectedError {
	t.Helper()
	_, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "subject", "")
	var rejected apimodel.FormaReconcileRejectedError
	require.ErrorAs(t, err, &rejected)
	return rejected
}
func TestResolutionAbsorbPreviewAndChoices(t *testing.T) {
	m, f := resolutionFixture(t)
	rejected := observeResolution(t, m, f)
	for _, tc := range []struct {
		name      string
		decisions []pkgmodel.DriftDecision
		valid     bool
	}{
		{"absorb", []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}, true},
		{"revert", []pkgmodel.DriftDecision{{ResourceID: "a", Action: "revert"}}, true},
		{"missing", nil, false},
		{"duplicate", []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}, {ResourceID: "a", Action: "revert"}}, false},
		{"unknown", []pkgmodel.DriftDecision{{ResourceID: "other", Action: "absorb"}}, false},
		{"skip", []pkgmodel.DriftDecision{{ResourceID: "a", Action: "skip"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: tc.decisions}}, "client", "subject", "")
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, res.Review)
			require.NotEmpty(t, res.Review.ReviewID)
			require.Len(t, res.Simulation.Command.ResourceUpdates, 1)
			if tc.name == "absorb" {
				require.Equal(t, "accept", res.Simulation.Command.ResourceUpdates[0].Operation)
			} else {
				require.Equal(t, "update", res.Simulation.Command.ResourceUpdates[0].Operation)
			}
		})
	}
}
func TestResolutionReviewStableAndBound(t *testing.T) {
	m, f := resolutionFixture(t)
	// New identities minted during planning must not make every review stale.
	addition := f.Resources[0]
	addition.Label = "new"
	addition.Ksuid = ""
	addition.Properties = []byte(`{"name":"new","other":"same"}`)
	f.Resources = append(f.Resources, addition)
	rejected := observeResolution(t, m, f)
	opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}
	preview, err := m.ApplyForma(f, opts, "client", "subject", "old name")
	require.NoError(t, err)
	reordered := ownPlanningValue(f)
	reordered.Resources[1].Properties = []byte(`{"other":"same","name":"new"}`)
	second, err := m.ApplyForma(reordered, opts, "different client", "subject", "new name")
	require.NoError(t, err)
	require.Equal(t, preview.Review.ReviewID, second.Review.ReviewID)
	opts.Resolution.ReviewID = preview.Review.ReviewID
	opts.Message = "editable confirmation message"
	_, err = m.ApplyForma(f, opts, "client", "subject", "")
	require.NoError(t, err)
	changed := ownPlanningValue(f)
	changed.Resources[1].Properties = []byte(`{"name":"changed"}`)
	_, err = m.ApplyForma(changed, opts, "client", "subject", "")
	require.Error(t, err)
	// A different physical observation invalidates the pin.
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	r.Properties = []byte(`{"name":"later drift"}`)
	_, err = m.Datastore.StoreResource(r, "later")
	require.NoError(t, err)
	_, err = m.ApplyForma(f, opts, "client", "subject", "")
	require.Error(t, err)
}
func TestResolutionReceiptPersistsAndPublicRetry(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/resolution.db"
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
		require.NoError(t, err)
		var calls atomic.Int64
		overrides := &plugin.ResourcePluginOverrides{Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
			calls.Add(1)
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: r.Label, ResourceProperties: []byte(`{"foo":"bar"}`)}}, nil
		}, Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
			calls.Add(1)
			return &resource.ReadResult{ResourceType: r.ResourceType, Properties: `{"foo":"bar"}`}, nil
		}}
		m := startScopedActor(t, ds, path, overrides)
		f := scopedActorForma()
		initial, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "before")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(initial.CommandID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		rows, err := ds.LoadResourcesByStack("scope")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
		require.NoError(t, ds.StoreFormaCommand(syncCommand, syncCommand.ID))
		rows[0].Properties = []byte(`{"foo":"absorbed"}`)
		_, err = ds.StoreResource(rows[0], syncCommand.ID)
		require.NoError(t, err)
		rejected := observeResolution(t, m, f)
		opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: rows[0].Ksuid, Action: "absorb"}}}}
		preview, err := m.ApplyForma(f, opts, "client", "subject", "before")
		require.NoError(t, err)
		require.Equal(t, syncCommand.ID, preview.Review.Observations[0].ObservedCommandID)
		versions, err := ds.LoadAllResourceVersions()
		require.NoError(t, err)
		before := calls.Load()
		opts.Simulate = false
		opts.Message = "final message"
		opts.Resolution.ReviewID = preview.Review.ReviewID
		opts.Resolution.IdempotencyKey = "durable-key"
		accepted, err := m.ApplyForma(f, opts, "client", "subject", "before")
		require.NoError(t, err)
		require.NotNil(t, accepted.Review)
		stored, err := ds.GetFormaCommandByCommandID(accepted.CommandID)
		require.NoError(t, err)
		require.Equal(t, forma_command.CommandStateSuccess, stored.State)
		require.Equal(t, preview.Review, stored.Resolution)
		afterVersions, err := ds.LoadAllResourceVersions()
		require.NoError(t, err)
		require.Equal(t, versions, afterVersions)
		require.Equal(t, before, calls.Load())
		retry, err := m.ApplyForma(f, opts, "another-client", "subject", "renamed display")
		require.NoError(t, err)
		require.Equal(t, accepted.CommandID, retry.CommandID)
		require.Equal(t, "before", retry.Simulation.Command.SubjectName)
		opts.Message = "changed payload"
		_, err = m.ApplyForma(f, opts, "client", "subject", "")
		require.ErrorIs(t, err, datastore.ErrAdmissionConflict)
		opts.Message = "final message"
		m.Stop(true)
		reopened, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "restart")
		require.NoError(t, err)
		restarted := startScopedActor(t, reopened, path, overrides)
		retry, err = restarted.ApplyForma(f, opts, "different-client", "subject", "later")
		require.NoError(t, err)
		require.Equal(t, accepted.CommandID, retry.CommandID)
		require.Equal(t, accepted.Review, retry.Review)
		require.Equal(t, before, calls.Load())
		desired, err := reopened.GetResourcesAtLastReconcile("scope")
		require.NoError(t, err)
		require.Len(t, desired, 1)
		require.JSONEq(t, `{"foo":"absorbed"}`, string(desired[0].Properties))
	})
}
func TestResolutionConfirmedLastDeletionAndConflict(t *testing.T) {
	m, f := resolutionFixture(t)
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	command := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(command, command.ID))
	_, err = m.Datastore.DeleteResource(r, command.ID)
	require.NoError(t, err)
	rejected := observeResolution(t, m, f)
	require.Equal(t, "delete", rejected.ModifiedStacks["a"].ModifiedResources[0].Operation)
	opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}
	revertOptions := ownPlanningValue(opts)
	revertOptions.Resolution.Decisions[0].Action = "revert"
	reverted, err := m.ApplyForma(f, revertOptions, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, reverted.Simulation.Command.ResourceUpdates, 1)
	require.Equal(t, "create", reverted.Simulation.Command.ResourceUpdates[0].Operation)
	plan, err := m.prepareGuardedApply(f, opts, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, plan.Command.ResourceUpdates, 1)
	require.Equal(t, resource_update.OperationAcceptDelete, plan.Command.ResourceUpdates[0].Operation)
	require.NoError(t, admitScopedPlan(t, m, plan))
	desired, err := m.Datastore.GetResourcesAtLastReconcile("a")
	require.NoError(t, err)
	require.Empty(t, desired)
	stack, err := m.Datastore.GetStackByLabel("a")
	require.NoError(t, err)
	require.NotNil(t, stack)
}
func TestResolutionThreeWayEditsAndUnsafeReference(t *testing.T) {
	for _, action := range []string{"absorb", "revert"} {
		for _, tc := range []struct {
			name                  string
			before, live, request string
			allow                 bool
		}{
			{"independent", `{"name":"before","extra":"old"}`, `{"name":"drifted","extra":"old"}`, `{"name":"before","extra":"new"}`, true},
			{"overlap", `{"name":"before"}`, `{"name":"drifted"}`, `{"name":"edited"}`, false},
			{"required-independent", `{"name":"before","extra":"old"}`, `{"name":"drifted","extra":"old"}`, `{"name":"before","extra":"new"}`, true},
			{"coowned-independent", `{"name":"before","tags":{"owned":"mine"}}`, `{"name":"drifted","tags":{"owned":"mine","external":"drifted"}}`, `{"name":"before","tags":{"owned":"edited"}}`, true},
			{"reference-overlap", `{"name":{"$ref":"formae://b#/name","$value":"before"}}`, `{"name":"drifted"}`, `{"name":{"$ref":"formae://c#/name"}}`, false},
			{"reference-equivalent", `{"name":"before","extra":{"$ref":"formae://b#/name","$value":"before"}}`, `{"name":"drifted","extra":{"$ref":"formae://b#/name","$value":"before"}}`, `{"name":"before","extra":{"$res":true,"$stack":"b","$label":"b","$type":"Test::Resource","$property":"name"}}`, true},
			{"format-equivalent", `{"name":"before","extra":"{\"a\":1,\"b\":2}"}`, `{"name":"drifted","extra":"{\"a\":3,\"b\":2}"}`, `{"name":"before","extra":"{\"b\":2,\"a\":1}"}`, true},
			{"set-equivalent", `{"name":"before","extra":["a","b"]}`, `{"name":"drifted","extra":["a","changed"]}`, `{"name":"before","extra":["b","a"]}`, true},
			{"array-overlap", `{"name":"before","extra":["a","b"]}`, `{"name":"drifted","extra":["drift","b"]}`, `{"name":"before","extra":["b","a"]}`, false},
			{"new-generator-independent", `{"name":"before","extra":"old"}`, `{"name":"drifted","extra":"old"}`, `{"name":"before","extra":{"$gen":true,"$label":"new","$stack":"a","$output":"value","$visibility":"Opaque"}}`, true},
			{"reference", `{"name":{"$ref":"formae://b#/name"}}`, `{"name":"unrepresentable"}`, `{"name":{"$ref":"formae://b#/name"}}`, false},
		} {
			if action == "absorb" && (tc.name == "format-equivalent" || tc.name == "set-equivalent") {
				continue
			}
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				m, _, f, _ := scopedFixture(t)
				r, err := m.Datastore.LoadResourceById("a")
				require.NoError(t, err)
				r.Schema.Fields = []string{"name", "extra", "tags"}
				if tc.name == "required-independent" {
					r.Schema.Hints = map[string]pkgmodel.FieldHint{"extra": {RequiredOnUpdate: true, EdgeKind: pkgmodel.EdgeKindDefault}}
				}
				if tc.name == "coowned-independent" {
					r.Schema.Hints = map[string]pkgmodel.FieldHint{"tags": {CoOwned: &pkgmodel.CoOwnership{}, EdgeKind: pkgmodel.EdgeKindDefault}}
					r.OwnedMembers = pkgmodel.OwnedMembers{"tags": {Rule: "Mapping", Members: []string{"owned"}}}
				}
				if tc.name == "format-equivalent" {
					r.Schema.Hints = map[string]pkgmodel.FieldHint{"extra": {Format: "json", EdgeKind: pkgmodel.EdgeKindDefault}}
				}
				if tc.name == "set-equivalent" {
					r.Schema.Hints = map[string]pkgmodel.FieldHint{"extra": {UpdateMethod: pkgmodel.FieldUpdateMethodSet, EdgeKind: pkgmodel.EdgeKindDefault}}
				}
				if tc.name == "array-overlap" {
					r.Schema.Hints = map[string]pkgmodel.FieldHint{"extra": {UpdateMethod: pkgmodel.FieldUpdateMethodArray, EdgeKind: pkgmodel.EdgeKindDefault}}
				}
				r.Properties = []byte(tc.before)
				r.Version, err = m.Datastore.StoreResource(r, "before")
				require.NoError(t, err)
				storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
				cmds, err := m.Datastore.LoadFormaCommands()
				require.NoError(t, err)
				baseline := cmds[0]
				for _, c := range cmds {
					if c.Source == forma_command.SourceUser && c.StartTs.After(baseline.StartTs) {
						baseline = c
					}
				}
				baseline.ResourceUpdates[0].Version = r.Version
				require.NoError(t, m.Datastore.StoreFormaCommand(baseline, baseline.ID))
				sync := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
				require.NoError(t, m.Datastore.StoreFormaCommand(sync, sync.ID))
				r.Properties = []byte(tc.live)
				_, err = m.Datastore.StoreResource(r, sync.ID)
				require.NoError(t, err)
				f.Resources[0] = *ownPlanningValue(r)
				f.Resources[0].Properties = []byte(tc.request)
				if tc.name == "new-generator-independent" {
					f.Generators = []json.RawMessage{json.RawMessage(`{"Type":"password","Label":"new","Stack":"a","Length":24,"Lowercase":true}`)}
				}
				rejected := observeResolution(t, m, f)
				preview, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: action}}}}, "client", "subject", "")
				if tc.allow || (action == "revert" && tc.name == "reference") {
					require.NoError(t, err)
					if action == "revert" && tc.name == "independent" {
						require.Contains(t, string(preview.Simulation.Command.ResourceUpdates[0].PatchDocument), `"value":"before"`)
						require.Contains(t, string(preview.Simulation.Command.ResourceUpdates[0].PatchDocument), `"value":"new"`)
					}
				} else {
					require.Error(t, err)
					if action == "revert" {
						var conflict apimodel.DriftResolutionError
						require.ErrorAs(t, err, &conflict)
						require.Equal(t, "decision-edit-conflict", conflict.Code)
						require.Equal(t, "a", conflict.ResourceID)
						require.Contains(t, conflict.Reason, "reverting")
					}
				}
			})
		}
	}
}
func TestResolutionDesiredDeltaUsesRecordedContribution(t *testing.T) {
	m, f := resolutionFixture(t)
	rejected := observeResolution(t, m, f)
	opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}
	plan, err := m.prepareGuardedApply(f, opts, "client", "subject", "")
	require.NoError(t, err)
	require.NoError(t, admitScopedPlan(t, m, plan))
	live, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	live.Properties = []byte(`{"name":"new unaccepted drift"}`)
	_, err = m.Datastore.StoreResource(live, "later")
	require.NoError(t, err)
	extractor, ok := any(m).(interface {
		ExtractCommandDesiredDelta(string) (*apimodel.CommandDesiredDelta, error)
	})
	require.True(t, ok)
	delta, err := extractor.ExtractCommandDesiredDelta(plan.Command.ID)
	require.NoError(t, err)
	require.True(t, delta.Partial)
	require.Equal(t, plan.Command.ID, delta.CommandID)
	require.Equal(t, plan.Command.Resolution, delta.Resolution)
	require.Len(t, delta.Forma.Resources, 1)
	require.JSONEq(t, `{"name":"drifted"}`, string(delta.Forma.Resources[0].Properties))
	require.Empty(t, delta.Forma.Extraction.CompleteStacks)
}
func TestResolutionFailedCreateRetryKeepsDesiredIdentity(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/failed.db"
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
		require.NoError(t, err)
		var fail atomic.Bool
		fail.Store(true)
		overrides := &plugin.ResourcePluginOverrides{Delete: func(r *resource.DeleteRequest) (*resource.DeleteResult, error) {
			return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess, NativeID: r.NativeID}}, nil
		}, Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
			status := resource.OperationStatusSuccess
			if fail.Load() {
				status = resource.OperationStatusFailure
			}
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: status, NativeID: r.Label, ResourceProperties: []byte(`{"foo":"bar"}`)}}, nil
		}, Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
			return &resource.ReadResult{ResourceType: r.ResourceType, Properties: `{"foo":"bar"}`}, nil
		}}
		m := startScopedActor(t, ds, path, overrides)
		f := scopedActorForma()
		first, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(first.CommandID)
			return e == nil && c.State == forma_command.CommandStateFailed
		}, 5*time.Second, 10*time.Millisecond)
		before, err := m.ExtractDesiredStacks("stack:scope")
		require.NoError(t, err)
		require.Len(t, before.Resources, 1)
		firstID := before.Resources[0].Ksuid
		// Evaluated declarations do not carry internal KSUIDs. Use the original
		// equivalent declaration to model that exact public authoring boundary.
		retryInput := ownPlanningValue(before)
		for i := range retryInput.Resources {
			retryInput.Resources[i].Ksuid = ""
		}
		fail.Store(false)
		second, err := m.ApplyForma(retryInput, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(second.CommandID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		after, err := m.ExtractDesiredStacks("stack:scope")
		require.NoError(t, err)
		require.Len(t, after.Resources, 1, "failed desired creation must not remain alongside a new retry identity")
		require.Equal(t, firstID, after.Resources[0].Ksuid)
		// A separate undeclared stack is outside this empty-stack reconciliation.
		other := scopedActorForma()
		other.Stacks[0].Label = "untouched"
		other.Resources[0].Stack = "untouched"
		other.Resources[0].Label = "other"
		otherCommand, e := m.ApplyForma(other, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, e)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(otherCommand.CommandID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		// Existing patch is the scoped maintenance path for a generator-only
		// declaration. It must retain the managed resource it does not repeat.
		generator := json.RawMessage(`{"Type":"password","Label":"retained","Stack":"scope","Length":24,"Lowercase":true}`)
		generatorOnly := &pkgmodel.Forma{Stacks: f.Stacks, Generators: []json.RawMessage{generator}}
		maintenance, e := m.ApplyForma(generatorOnly, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "client", "subject", "")
		require.NoError(t, e)
		maintenanceCommand, e := ds.GetFormaCommandByCommandID(maintenance.CommandID)
		require.NoError(t, e)
		require.Equal(t, forma_command.CommandStateSuccess, maintenanceCommand.State)
		maintained, e := ds.LoadResourcesByStack("scope")
		require.NoError(t, e)
		require.Len(t, maintained, 1)
		removed := ownPlanningValue(f)
		removed.Generators = []json.RawMessage{generator}
		removed.Resources = nil
		deletion, e := m.ApplyForma(removed, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, e)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(deletion.CommandID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		remaining, e := ds.GetResourcesAtLastReconcile("scope")
		require.NoError(t, e)
		require.Empty(t, remaining)
		retained, e := ds.LoadGeneratorsByStack("scope")
		require.NoError(t, e)
		require.Len(t, retained, 1)
		untouched, e := ds.LoadResourcesByStack("untouched")
		require.NoError(t, e)
		require.Len(t, untouched, 1)
	})
}

func TestResolutionFailedCreateOmissionRequiresRecovery(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/failed.db"
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
		require.NoError(t, err)
		var fail atomic.Bool
		fail.Store(true)
		overrides := &plugin.ResourcePluginOverrides{Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
			status := resource.OperationStatusSuccess
			if fail.Load() {
				status = resource.OperationStatusFailure
			}
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: status, NativeID: r.Label, ResourceProperties: []byte(`{"foo":"bar"}`)}}, nil
		}, Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
			return &resource.ReadResult{ResourceType: r.ResourceType, Properties: `{"foo":"bar"}`}, nil
		}}
		m := startScopedActor(t, ds, path, overrides)
		f := scopedActorForma()
		first, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(first.CommandID)
			return e == nil && c.State == forma_command.CommandStateFailed
		}, 5*time.Second, 10*time.Millisecond)

		omitted := ownPlanningValue(f)
		omitted.Resources = nil
		for _, force := range []bool{false, true} {
			_, err = m.ApplyForma(omitted, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: force}, "client", "subject", "")
			var refused apimodel.DriftResolutionError
			require.ErrorAs(t, err, &refused)
			require.Equal(t, "desired-intent-unavailable", refused.Code)
			require.Equal(t, first.CommandID, refused.CommandID)
		}
		remaining, err := m.ExtractDesiredStacks("stack:scope")
		require.NoError(t, err)
		require.Len(t, remaining.Resources, 1, "failed intent remains available for recovery")
	})
}
func TestResolutionLegacySourceDeletionRecordsAcceptance(t *testing.T) {
	m, f := resolutionFixture(t)
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	command := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(command, command.ID))
	_, err = m.Datastore.DeleteResource(r, command.ID)
	require.NoError(t, err)
	f.Resources = nil
	plan, err := m.prepareGuardedApply(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, plan.Command.ResourceUpdates, 1)
	require.Equal(t, resource_update.OperationAcceptDelete, plan.Command.ResourceUpdates[0].Operation)
	require.NoError(t, admitScopedPlan(t, m, plan))
	desired, err := m.Datastore.GetResourcesAtLastReconcile("a")
	require.NoError(t, err)
	require.Empty(t, desired)
}

func TestResolutionSymbolicArrayDoesNotReattachByIndex(t *testing.T) {
	prior := pkgmodel.Resource{Properties: []byte(`{"items":[{"name":"a","value":{"$ref":"source#/value"}},{"name":"b","value":"same"}]}`), Schema: pkgmodel.Schema{Fields: []string{"items"}}}
	adopted, err := absorbDeclarationProperties(prior, []byte(`{"items":[{"name":"b","value":"same"},{"name":"a","value":"same"}]}`))
	require.NoError(t, err)
	// Keep the declaration as a whole for normal planning to verify. Position
	// alone does not prove that a provider array element has the same identity.
	require.JSONEq(t, string(prior.Properties), string(adopted))
}

func TestResolutionAbsorbOwnedDeletionIsMetadataOnly(t *testing.T) {
	m, f := resolutionFixture(t)
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	r.Schema = pkgmodel.Schema{Fields: []string{"tags"}, Hints: map[string]pkgmodel.FieldHint{"tags": {CoOwned: &pkgmodel.CoOwnership{}}}}
	r.Properties = []byte(`{"tags":{"owned":"value","external":"value"}}`)
	r.OwnedMembers = pkgmodel.OwnedMembers{"tags": {Rule: "Mapping", Members: []string{"owned"}}}
	_, err = m.Datastore.StoreResource(r, "seed")
	require.NoError(t, err)
	r, err = m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
	f.Resources[0] = *ownPlanningValue(r)
	f.Resources[0].Properties = []byte(`{"tags":{"owned":"value"}}`)
	syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(syncCommand, syncCommand.ID))
	r.Properties = []byte(`{"tags":{"external":"value"}}`)
	_, err = m.Datastore.StoreResource(r, syncCommand.ID)
	require.NoError(t, err)
	rejected := observeResolution(t, m, f)
	preview, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
	require.Equal(t, "accept", preview.Simulation.Command.ResourceUpdates[0].Operation)
}

func TestResolutionMixedExecutionAndTerminalIntent(t *testing.T) {
	for _, failAddition := range []bool{false, true} {
		t.Run(fmt.Sprint("failed-addition=", failAddition), func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				path := t.TempDir() + "/mixed.db"
				ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}, "test")
				require.NoError(t, err)
				var reverted, created atomic.Int64
				var drifted atomic.Bool
				overrides := &plugin.ResourcePluginOverrides{
					Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
						status := resource.OperationStatusSuccess
						if r.Label == "addition" {
							created.Add(1)
							if failAddition {
								status = resource.OperationStatusFailure
							}
						}
						return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: status, NativeID: r.Label, ResourceProperties: r.Properties}}, nil
					},
					Update: func(r *resource.UpdateRequest) (*resource.UpdateResult, error) {
						require.Equal(t, "reverted", r.NativeID, "absorb must not call the provider")
						reverted.Add(1)
						return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: r.NativeID, ResourceProperties: []byte(`{"foo":"bar"}`)}}, nil
					},
					Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
						props := `{"foo":"bar"}`
						if drifted.Load() && (r.NativeID == "absorbed" || (r.NativeID == "reverted" && reverted.Load() == 0)) {
							props = `{"foo":"drifted"}`
						}
						return &resource.ReadResult{ResourceType: r.ResourceType, Properties: props}, nil
					},
				}
				m := startScopedActor(t, ds, path, overrides)
				f := scopedActorForma()
				f.Resources[0].Label = "absorbed"
				second := f.Resources[0]
				second.Label = "reverted"
				f.Resources = append(f.Resources, second)
				initial, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
				require.NoError(t, err)
				require.Eventually(t, func() bool {
					c, e := ds.GetFormaCommandByCommandID(initial.CommandID)
					return e == nil && c.State == forma_command.CommandStateSuccess
				}, 5*time.Second, 10*time.Millisecond)
				rows, err := ds.LoadResourcesByStack("scope")
				require.NoError(t, err)
				syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
				require.NoError(t, ds.StoreFormaCommand(syncCommand, syncCommand.ID))
				decisions := []pkgmodel.DriftDecision{}
				for _, row := range rows {
					row.Properties = []byte(`{"foo":"drifted"}`)
					_, err = ds.StoreResource(row, syncCommand.ID)
					require.NoError(t, err)
					action := "revert"
					if row.Label == "absorbed" {
						action = "absorb"
					}
					decisions = append(decisions, pkgmodel.DriftDecision{ResourceID: row.Ksuid, Action: action})
				}
				drifted.Store(true)
				addition := second
				addition.Label = "addition"
				f.Resources = append(f.Resources, addition)
				rejected := observeResolution(t, m, f)
				opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: decisions}}
				preview, err := m.ApplyForma(f, opts, "client", "subject", "")
				require.NoError(t, err)
				require.Len(t, preview.Simulation.Command.ResourceUpdates, 3)
				opts.Simulate = false
				opts.Resolution.ReviewID = preview.Review.ReviewID
				opts.Resolution.IdempotencyKey = "mixed"
				submitted, err := m.ApplyForma(f, opts, "client", "subject", "")
				require.NoError(t, err)
				t.Cleanup(func() {
					if t.Failed() {
						c, _ := ds.GetFormaCommandByCommandID(submitted.CommandID)
						raw, _ := json.Marshal(c)
						t.Log(string(raw))
					}
				})
				expected := forma_command.CommandStateSuccess
				if failAddition {
					expected = forma_command.CommandStateFailed
				}
				require.Eventually(t, func() bool {
					c, e := ds.GetFormaCommandByCommandID(submitted.CommandID)
					return e == nil && c.State == expected
				}, 5*time.Second, 10*time.Millisecond)
				require.EqualValues(t, 1, reverted.Load())
				require.EqualValues(t, 1, created.Load())
				desired, err := m.ExtractDesiredStacks("stack:scope")
				require.NoError(t, err)
				require.Len(t, desired.Resources, 3, "Failed still contributes submitted desired intent")
				for _, r := range desired.Resources {
					value := `{"foo":"bar"}`
					if r.Label == "absorbed" {
						value = `{"foo":"drifted"}`
					}
					require.JSONEq(t, value, string(r.Properties))
				}
				delta, err := m.ExtractCommandDesiredDelta(submitted.CommandID)
				require.NoError(t, err)
				require.Len(t, delta.Forma.Resources, 3)
				replay, err := m.ApplyForma(f, opts, "another-client", "subject", "updated display")
				require.NoError(t, err)
				require.Equal(t, submitted.CommandID, replay.CommandID)
				require.EqualValues(t, 1, reverted.Load())
				require.EqualValues(t, 1, created.Load())
			})
		})
	}
}

func TestResolutionDesiredDeltaReplacementIsNotFinalDeletion(t *testing.T) {
	m, f := resolutionFixture(t)
	rejected := observeResolution(t, m, f)
	plan, err := m.prepareGuardedApply(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}, "client", "subject", "")
	require.NoError(t, err)
	require.NoError(t, admitScopedPlan(t, m, plan))
	command, err := m.Datastore.GetFormaCommandByCommandID(plan.Command.ID)
	require.NoError(t, err)
	// A replace can persist delete/create contributions for the same identity.
	removed := command.ResourceUpdates[0]
	removed.Operation = resource_update.OperationDelete
	command.ResourceUpdates[0].Operation = resource_update.OperationCreate
	command.ResourceUpdates = append([]resource_update.ResourceUpdate{removed}, command.ResourceUpdates...)
	require.NoError(t, m.Datastore.StoreFormaCommand(command, command.ID))
	delta, err := m.ExtractCommandDesiredDelta(command.ID)
	require.NoError(t, err)
	require.Len(t, delta.Forma.Resources, 1)
	require.Empty(t, delta.DeletedResources)
}

func TestResolutionAbsorbRetainsVisibleReferenceConvergence(t *testing.T) {
	m, _, f, _ := scopedFixture(t)
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	r.Schema.Fields = []string{"name", "extra"}
	r.Properties = []byte(`{"name":{"$ref":"formae://b#/name","$value":"before"},"extra":"old"}`)
	r.Version, err = m.Datastore.StoreResource(r, "before")
	require.NoError(t, err)
	storeDesired(t, m.Datastore, *r, resource_update.OperationUpdate, forma_command.CommandStateSuccess)
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	commands[0].ResourceUpdates[0].Version = r.Version
	require.NoError(t, m.Datastore.StoreFormaCommand(commands[0], commands[0].ID))
	f.Resources[0] = *ownPlanningValue(r)
	f.Resources[0].Properties = []byte(`{"name":{"$ref":"formae://b#/name"},"extra":"old"}`)
	syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(syncCommand, syncCommand.ID))
	r.Properties = []byte(`{"name":{"$ref":"formae://b#/name","$value":"before"},"extra":"drifted"}`)
	_, err = m.Datastore.StoreResource(r, syncCommand.ID)
	require.NoError(t, err)
	source, err := m.Datastore.LoadResourceById("b")
	require.NoError(t, err)
	source.Properties = []byte(`{"name":"source moved"}`)
	_, err = m.Datastore.StoreResource(source, syncCommand.ID)
	require.NoError(t, err)
	rejected := observeResolution(t, m, f)
	opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}
	plan, err := m.prepareGuardedApply(f, opts, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, plan.Command.ResourceUpdates, 1)
	u := plan.Command.ResourceUpdates[0]
	require.True(t, u.ConvergenceOnly(), "source propagation still requires ordinary provider work")
	require.Equal(t, resource_update.OperationUpdate, u.Operation)
	require.Equal(t, "update", plan.Response.Simulation.Command.ResourceUpdates[0].Operation)
	require.Contains(t, string(u.DesiredState.Properties), `"extra":"drifted"`)
}

func TestResolutionUnacceptedCreationChoices(t *testing.T) {
	m, _, f, _ := scopedFixture(t)
	f.Resources = nil // complete previous desired declaration has no resource
	stack, err := m.Datastore.GetStackByLabel("a")
	require.NoError(t, err)
	baseline := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}}
	require.NoError(t, m.Datastore.StoreFormaCommand(baseline, baseline.ID))
	r, err := m.Datastore.LoadResourceById("a")
	require.NoError(t, err)
	syncCommand := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateSuccess}
	require.NoError(t, m.Datastore.StoreFormaCommand(syncCommand, syncCommand.ID))
	r.Properties = []byte(`{"name":"unaccepted creation"}`)
	_, err = m.Datastore.StoreResource(r, syncCommand.ID)
	require.NoError(t, err)
	rejected := observeResolution(t, m, f)
	require.Equal(t, "create", rejected.ModifiedStacks["a"].ModifiedResources[0].Operation)
	for action, operation := range map[string]string{"absorb": "accept", "revert": "delete"} {
		preview, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: action}}}}, "client", "subject", "")
		require.NoError(t, err)
		require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
		require.Equal(t, operation, preview.Simulation.Command.ResourceUpdates[0].Operation)
	}
}
