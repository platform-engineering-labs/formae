//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestScopedApplyOwnsRequest(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
	f := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "a"}}, Targets: []pkgmodel.Target{{Label: "t", Namespace: "test", Config: json.RawMessage(`{}`)}}, Resources: []pkgmodel.Resource{{Stack: "a", Target: "t", Label: "r", Type: "test/Resource", Properties: json.RawMessage(`{"name":"x"}`), Schema: pkgmodel.Schema{Fields: []string{"name"}, Hints: map[string]pkgmodel.FieldHint{"name": {CoOwned: &pkgmodel.CoOwnership{SystemPatterns: []string{"system"}}}}}}}}
	before, _ := json.Marshal(f)
	options := &config.FormaCommandConfig{Simulate: true, Mode: pkgmodel.FormaApplyModeReconcile}
	_, err := m.ApplyForma(f, options, "client", "subject", "")
	require.NoError(t, err)
	after, _ := json.Marshal(f)
	require.JSONEq(t, string(before), string(after), "planning must not assign IDs or translate caller-owned resources")
	require.Equal(t, pkgmodel.FormaApplyModeReconcile, options.Mode)
	emptyOptions := &config.FormaCommandConfig{Simulate: true}
	_, err = m.ApplyForma(&pkgmodel.Forma{}, emptyOptions, "client", "subject", "")
	require.NoError(t, err)
	require.Empty(t, emptyOptions.Mode)
}

func TestScopedApplyCertifiesReadInterval(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	f := &pkgmodel.Forma{}
	scope := newPlanningDatastore(ds, f)
	_, err := scope.certify(func() error {
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: "concurrent"}, "writer")
		return err
	})
	require.ErrorIs(t, err, datastore.ErrStaleAdmission)
	commands, err := ds.LoadFormaCommands()
	require.NoError(t, err)
	require.Empty(t, commands)
}

func scopedFixture(t *testing.T) (*Metastructure, datastore.Datastore, *pkgmodel.Forma, *config.FormaCommandConfig) {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: t.TempDir() + "/scope.db"}}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	t.Cleanup(func() { ds.Close() })
	writer, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "writer")
	require.NoError(t, err)
	t.Cleanup(func() { writer.Close() })
	for _, label := range []string{"a", "b", "c", "unrelated"} {
		_, err = ds.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
	}
	_, err = ds.CreateTarget(&pkgmodel.Target{Label: "t", Namespace: "test", Config: []byte(`{}`)})
	require.NoError(t, err)
	for _, label := range []string{"a", "b", "c", "unrelated"} {
		_, err = ds.StoreResource(&pkgmodel.Resource{Ksuid: label, Stack: label, Target: "t", Label: label, Type: "Test::Resource", Managed: true, Properties: []byte(`{"name":"before"}`), Schema: pkgmodel.Schema{Fields: []string{"name"}, Portable: true}}, "seed")
		require.NoError(t, err)
	}
	f := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "a"}}, Resources: []pkgmodel.Resource{{Stack: "a", Target: "t", Label: "a", Type: "Test::Resource", Managed: true, Properties: []byte(`{"name":"after"}`), Schema: pkgmodel.Schema{Fields: []string{"name"}, Portable: true}}}}
	return &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}, writer, f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: true}
}
func scopedWrite(t *testing.T, writer datastore.Datastore, id string) {
	t.Helper()
	r, e := writer.LoadResourceById(id)
	require.NoError(t, e)
	require.NotNil(t, r)
	r.Properties = []byte(`{"name":"concurrent"}`)
	_, e = writer.StoreResource(r, "independent-writer")
	require.NoError(t, e)
}
func admitScopedPlan(t *testing.T, m *Metastructure, plan *guardedApplyPlan) error {
	t.Helper()
	_, e := m.Datastore.(datastore.CommandAdmitter).AdmitFormaCommand(plan.Command, datastore.CommandAdmission{Guards: plan.Guards, PrincipalScope: "test", IdempotencyKey: util.NewID(), RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{"ok":true}`)})
	return e
}
func TestScopedApplySelectiveAdmission(t *testing.T) {
	for _, id := range []string{"a", "unrelated"} {
		t.Run(id, func(t *testing.T) {
			m, writer, f, options := scopedFixture(t)
			plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
			require.NoError(t, err)
			require.Less(t, len(plan.Guards), 20)
			scopedWrite(t, writer, id)
			err = admitScopedPlan(t, m, plan)
			if id == "a" {
				require.ErrorIs(t, err, datastore.ErrStaleAdmission)
				commands, e := writer.LoadFormaCommands()
				require.NoError(t, e)
				require.Empty(t, commands)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// A real independent writer commits after the planner has loaded its index.
type scopedReadBarrier struct {
	datastore.Datastore
	datastore.CommandAdmitter
	datastore.AdmissionScopeResolver
	datastore.AdmissionPredicateResolver
	datastore.PolicyIdentityReader
	datastore.CommandTargetIdentityWriter
	datastore.ResourceObservationReader
	afterRead func()
}

func (d *scopedReadBarrier) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	rows, e := d.Datastore.LoadAllResourcesByStack()
	if e == nil && d.afterRead != nil {
		f := d.afterRead
		d.afterRead = nil
		f()
	}
	return rows, e
}
func withScopedBarrier(ds datastore.Datastore, f func()) *scopedReadBarrier {
	return &scopedReadBarrier{Datastore: ds, CommandAdmitter: ds.(datastore.CommandAdmitter), AdmissionScopeResolver: ds.(datastore.AdmissionScopeResolver), AdmissionPredicateResolver: ds.(datastore.AdmissionPredicateResolver), PolicyIdentityReader: ds.(datastore.PolicyIdentityReader), CommandTargetIdentityWriter: ds.(datastore.CommandTargetIdentityWriter), ResourceObservationReader: ds.(datastore.ResourceObservationReader), afterRead: f}
}
func TestScopedApplyRejectsIndependentWriterDuringPlannerRead(t *testing.T) {
	m, writer, f, options := scopedFixture(t)
	m.Datastore = withScopedBarrier(m.Datastore, func() { scopedWrite(t, writer, "a") })
	_, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.ErrorIs(t, err, datastore.ErrStaleAdmission)
	commands, err := writer.LoadFormaCommands()
	require.NoError(t, err)
	require.Empty(t, commands)
}
func TestScopedApplyReferenceSchemaAndAbsenceGuards(t *testing.T) {
	for _, change := range []string{"source-value", "source-schema", "unrelated", "missing-ksuid"} {
		t.Run(change, func(t *testing.T) {
			m, writer, f, options := scopedFixture(t)
			f.Resources[0].Properties = []byte(`{"name":{"$ref":"formae://b#/name"}}`)
			if change == "missing-ksuid" {
				// Target config comparison treats an absent source as dangling, a
				// successful lookup whose later appearance must invalidate the plan.
				f.Resources = nil
				f.Stacks = nil
				existing, e := writer.LoadTarget("t")
				require.NoError(t, e)
				existing.Config = []byte(`{"credential":{"$ref":"formae://missing#/name"}}`)
				_, e = writer.UpdateTarget(existing)
				require.NoError(t, e)
				f.Targets = []pkgmodel.Target{*existing}
				f.Targets[0].Config = []byte(`{"credential":{"$ref":"formae://missing#/name"},"other":true}`)
			}
			plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
			require.NoError(t, err)
			switch change {
			case "source-value":
				scopedWrite(t, writer, "b")
			case "source-schema":
				r, e := writer.LoadResourceById("b")
				require.NoError(t, e)
				r.Schema.Hints = map[string]pkgmodel.FieldHint{"name": {Opaque: true}}
				observation, e := writer.(datastore.ResourceObservationReader).GetResourceObservation("b")
				require.NoError(t, e)
				require.NotEmpty(t, observation.Version)
				require.NoError(t, writer.UpdateResourceVersionData(string(r.URI()), observation.Version, r))
				got, e := writer.LoadResourceById("b")
				require.NoError(t, e)
				require.True(t, got.Schema.Hints["name"].Opaque)
			case "unrelated":
				scopedWrite(t, writer, "unrelated")
			case "missing-ksuid":
				_, e := writer.StoreResource(&pkgmodel.Resource{Ksuid: "missing", Stack: "virtual", Target: "t", Label: "missing", Type: "Test::Resource", Managed: true, Properties: []byte(`{"name":"appeared"}`)}, "writer")
				require.NoError(t, e)
			}
			err = admitScopedPlan(t, m, plan)
			if change == "unrelated" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, datastore.ErrStaleAdmission)
			}
		})
	}
}
func TestScopedApplyPrivateNestedOwnership(t *testing.T) {
	f := &pkgmodel.Forma{Properties: map[string]pkgmodel.Prop{"nested": {Value: map[string]any{"list": []any{map[string]any{"x": "original"}}}}}, Resources: []pkgmodel.Resource{{Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"x": {CoOwned: &pkgmodel.CoOwnership{SystemPatterns: []string{"original"}}}}}}}}
	clone := ownPlanningValue(f)
	f.Resources[0].Schema.Hints["x"].CoOwned.SystemPatterns[0] = "changed"
	f.Properties["nested"].Value.(map[string]any)["list"].([]any)[0].(map[string]any)["x"] = "changed"
	require.Equal(t, "original", clone.Resources[0].Schema.Hints["x"].CoOwned.SystemPatterns[0])
	require.Contains(t, fmt.Sprint(clone.Properties["nested"].Value), "original")
	generator := &pkgmodel.PasswordGenerator{ID: "identity", StackID: "incarnation", Rotation: &pkgmodel.RotationSpec{EverySeconds: 60}}
	copied := ownPlanningValue[pkgmodel.Generator](generator)
	generator.Rotation.EverySeconds = 1
	require.Equal(t, "identity", copied.GetID())
	require.Equal(t, "incarnation", copied.GetStackID())
	require.Equal(t, 60, copied.GetRotation().EverySeconds)
}

func TestScopedApplyRestartsWholePlanOnNewReferenceScope(t *testing.T) {
	m, writer, f, options := scopedFixture(t)
	f.Resources[0].Properties = []byte(`{"name":{"$ref":"formae://b#/name"}}`)
	m.Datastore = withScopedBarrier(m.Datastore, func() { scopedWrite(t, writer, "b") })
	plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
	encoded, err := json.Marshal(plan.Command)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "concurrent", "the first, uncertified candidate must have been discarded")
	require.NoError(t, admitScopedPlan(t, m, plan))
}
func TestScopedApplyIndirectAndOpaqueReferences(t *testing.T) {
	for _, opaque := range []bool{false, true} {
		t.Run(fmt.Sprint(opaque), func(t *testing.T) {
			m, writer, f, options := scopedFixture(t)
			b, err := writer.LoadResourceById("b")
			require.NoError(t, err)
			if opaque {
				b.Properties = []byte(`{"name":{"$value":"digest","$hashed":true}}`)
				b.Schema.Hints = map[string]pkgmodel.FieldHint{"name": {Opaque: true}}
				_, err = writer.StoreResource(b, "opaque")
				require.NoError(t, err)
			} else {
				b.Properties = []byte(`{"name":{"$ref":"formae://c#/name"}}`)
				f.Stacks = append(f.Stacks, pkgmodel.Stack{Label: "b"})
				f.Resources = append(f.Resources, *b)
			}
			f.Resources[0].Properties = []byte(`{"name":{"$ref":"formae://b#/name"}}`)
			plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
			require.NoError(t, err)
			if opaque {
				scopedWrite(t, writer, "b")
			} else {
				scopedWrite(t, writer, "c")
			}
			require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission)
		})
	}
}
func TestScopedApplyTargetInventoryPredicates(t *testing.T) {
	for _, tc := range []struct {
		name, target, stack string
		empty               bool
	}{{"same-target-virtual", "t", "virtual", false}, {"other-target", "u", "virtual", false}, {"zero-members", "empty", "virtual", true}} {
		t.Run(tc.name, func(t *testing.T) {
			m, writer, f, options := scopedFixture(t)
			_, err := writer.CreateTarget(&pkgmodel.Target{Label: "u", Namespace: "test", Config: []byte(`{}`)})
			require.NoError(t, err)
			selected := "t"
			if tc.empty {
				selected = "empty"
				_, err = writer.CreateTarget(&pkgmodel.Target{Label: selected, Namespace: "test", Config: []byte(`{}`)})
				require.NoError(t, err)
			}
			target, err := writer.LoadTarget(selected)
			require.NoError(t, err)
			target.Config = []byte(`{"region":"old"}`)
			target.ConfigSchema = pkgmodel.ConfigSchema{Hints: map[string]pkgmodel.ConfigFieldHint{"region": {CreateOnly: true}}}
			_, err = writer.UpdateTarget(target)
			require.NoError(t, err)
			target.Config = []byte(`{"region":"new"}`)
			f.Targets = []pkgmodel.Target{*target}
			if tc.empty {
				f.Resources = nil
				f.Stacks = nil
			}
			plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
			require.NoError(t, err)
			_, err = writer.StoreResource(&pkgmodel.Resource{Ksuid: "new-member", Stack: tc.stack, Target: tc.target, Managed: true, Label: "new-member", Type: "Test::Resource", Properties: []byte(`{"name":"new"}`)}, "writer")
			require.NoError(t, err)
			err = admitScopedPlan(t, m, plan)
			if tc.name == "other-target" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, datastore.ErrStaleAdmission)
			}
		})
	}
}
func TestScopedApplyNoOpAndVirtualUnmanagedAbsence(t *testing.T) {
	for _, noop := range []bool{false, true} {
		t.Run(fmt.Sprint(noop), func(t *testing.T) {
			m, writer, f, options := scopedFixture(t)
			if noop {
				f.Resources[0].Properties = []byte(`{"name":"before"}`)
			} else {
				f.Resources[0].Label = "new-resource"
			}
			plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
			require.NoError(t, err)
			if noop {
				require.False(t, plan.Command.HasChanges())
				scopedWrite(t, writer, "a")
			} else {
				_, err = writer.StoreResource(&pkgmodel.Resource{Ksuid: "import", Stack: "$unmanaged", Target: "t", Label: "new-resource", Type: "Test::Resource", Properties: []byte(`{"name":"found"}`)}, "writer")
				require.NoError(t, err)
			}
			require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission)
		})
	}
}

func TestScopedApplyGuardCountTracksClosureNotInstallation(t *testing.T) {
	m, writer, f, options := scopedFixture(t)
	before, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
	for i := 0; i < 80; i++ {
		label := fmt.Sprintf("other-%d", i)
		_, err = writer.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
		_, err = writer.StoreResource(&pkgmodel.Resource{Ksuid: label, Stack: label, Target: "t", Label: label, Type: "Test::Resource", Managed: true, Properties: []byte(`{"name":"unrelated"}`)}, "seed")
		require.NoError(t, err)
	}
	after, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, after.Guards, len(before.Guards))
	scopedWrite(t, writer, "other-79")
	require.NoError(t, admitScopedPlan(t, m, after))
}

func TestScopedApplySupportedScale(t *testing.T) {
	m, writer, f, options := scopedFixture(t)
	const count = 20000
	resources := make([]pkgmodel.Resource, count-1)
	for i := range resources {
		resources[i] = pkgmodel.Resource{Ksuid: fmt.Sprintf("scale-%05d", i), Stack: "a", Target: "t", Label: fmt.Sprintf("scale-%05d", i), Type: "Test::Resource", Managed: true, Properties: json.RawMessage(`{"name":"before"}`), Schema: pkgmodel.Schema{Fields: []string{"name"}, Portable: true}}
	}
	_, err := writer.BulkStoreResources(resources, "seed")
	require.NoError(t, err)
	for _, r := range resources {
		r.Properties = json.RawMessage(`{"name":"after"}`)
		f.Resources = append(f.Resources, r)
	}
	plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, plan.Command.ResourceUpdates, count)
	require.Greater(t, len(plan.Guards), count, "retain global identity predicates as well as stack/domain guards")
	// Invalidation must include a resource beyond the former boundary.
	scopedWrite(t, writer, resources[len(resources)-1].Ksuid)
	require.ErrorIs(t, admitScopedPlan(t, m, plan), datastore.ErrStaleAdmission)
	plan, err = m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
	require.NoError(t, admitScopedPlan(t, m, plan))
	stored, err := m.Datastore.GetFormaCommandByCommandID(plan.Command.ID)
	require.NoError(t, err)
	require.Len(t, stored.ResourceUpdates, count)
	t.Logf("admitted %d existing resources with %d guards; last-resource writer rejected stale plan", count, len(plan.Guards))
}

func TestScopedApplyReadProgressDuringCertification(t *testing.T) {
	m, writer, f, options := scopedFixture(t)
	stack, err := writer.GetStackByLabel("a")
	require.NoError(t, err)
	c := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandSync, Source: forma_command.SourceSynchronizer, State: forma_command.CommandStateNotStarted, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}}
	require.NoError(t, writer.StoreFormaCommand(c, c.ID))
	progress := false
	m.Datastore = withScopedBarrier(m.Datastore, func() {
		require.NoError(t, writer.UpdateFormaCommandProgress(c.ID, forma_command.CommandStateInProgress, time.Now()))
		progress = true
	})
	plan, err := m.prepareGuardedApply(f, options, "client", "subject", "")
	require.NoError(t, err)
	require.True(t, progress)
	require.NoError(t, admitScopedPlan(t, m, plan))
}
