// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package migration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The migration forgets unmanaged rows so discovery re-ingests them without the
// dot-exploded duplicates an older build wrote. What it must get right is WHEN
// it acts: once per target incarnation, never where re-ingest would dangle a
// reference, and never where replaying an unfinished command would restore what
// it just forgot.

const (
	corruptedProps = `{"metadata":{"annotations":{` +
		`"objectset.rio.cattle.io/applied":"v",` +
		`"objectset":{"rio":{"cattle":{"io/applied":"v"}}}}}}`
	cleanProps = `{"metadata":{"annotations":{"objectset.rio.cattle.io/applied":"v"}}}`
)

// newFileBackedDatastore builds a datastore on a real file: the lease opens its
// own connection to the same database, which an in-memory one cannot share.
func newFileBackedDatastore(t *testing.T, path string) datastore.Datastore {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: path},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	t.Cleanup(ds.Close)
	return ds
}

// storeTarget creates a target and returns the incarnation the datastore minted
// for it, which is what the migration keys its marker on.
func storeTarget(t *testing.T, ds datastore.Datastore, label string) string {
	t.Helper()
	_, err := ds.CreateTarget(&pkgmodel.Target{
		Label:     label,
		Namespace: "test",
		Config:    json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	return incarnationOfTarget(t, ds, label)
}

// recreateTarget deletes and re-creates a target under the same label, which is
// how a fresh incarnation comes about in practice.
func recreateTarget(t *testing.T, ds datastore.Datastore, label string) string {
	t.Helper()
	_, err := ds.DeleteTarget(label)
	require.NoError(t, err)
	return storeTarget(t, ds, label)
}

func incarnationOfTarget(t *testing.T, ds datastore.Datastore, label string) string {
	t.Helper()
	target, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, target)
	require.NotNil(t, target.Health)
	return target.Health.IncarnationID
}

func storeUnmanaged(t *testing.T, ds datastore.Datastore, label, target, props string) *pkgmodel.Resource {
	t.Helper()
	resource := &pkgmodel.Resource{
		Label:      label,
		Type:       "Test::Thing",
		Stack:      constants.UnmanagedStack,
		Target:     target,
		NativeID:   "native-" + label,
		Managed:    false,
		Properties: json.RawMessage(props),
	}
	_, err := ds.StoreResource(resource, "cmd-seed")
	require.NoError(t, err)

	stored := unmanagedOn(t, ds, target)
	for _, row := range stored {
		if row.Label == label {
			return row
		}
	}
	t.Fatalf("stored resource %q was not found on target %q", label, target)
	return nil
}

func unmanagedOn(t *testing.T, ds datastore.Datastore, target string) []*pkgmodel.Resource {
	t.Helper()
	rows, err := ds.QueryResources(&datastore.ResourceQuery{
		Stack:   &datastore.QueryItem[string]{Item: constants.UnmanagedStack, Constraint: datastore.Required},
		Managed: &datastore.QueryItem[bool]{Item: false, Constraint: datastore.Required},
		Target:  &datastore.QueryItem[string]{Item: target, Constraint: datastore.Required},
	})
	require.NoError(t, err)
	return rows
}

func markersOf(t *testing.T, ds datastore.Datastore) map[datastore.DataMigrationMarker]datastore.DataMigrationOutcome {
	t.Helper()
	lease, err := ds.(datastore.DataMigrationCapable).AcquireDataMigrationLease(context.Background())
	require.NoError(t, err)
	defer func() { require.NoError(t, lease.Release()) }()
	markers, err := lease.LoadMarkers(context.Background(), DottedKeyReingestKey)
	require.NoError(t, err)
	return markers
}

func markerFor(label, incarnation string) datastore.DataMigrationMarker {
	return datastore.DataMigrationMarker{TargetLabel: label, IncarnationID: incarnation}
}

// A target carrying the corruption is forgotten, and the outcome is recorded.
func TestReingest_WipesACorruptedTarget(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)
	storeUnmanaged(t, ds, "innocent", "prod", cleanProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Empty(t, unmanagedOn(t, ds, "prod"),
		"one corrupted row forgets every unmanaged row on the target, so the family re-mints together")
	assert.Equal(t, datastore.DataMigrationWiped, markersOf(t, ds)[markerFor("prod", incarnation)])
}

// A target with nothing to repair is recorded as scanned, so it is not scanned
// again.
func TestReingest_MarksACleanTarget(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "innocent", "prod", cleanProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Len(t, unmanagedOn(t, ds, "prod"), 1, "a clean row is left alone")
	assert.Equal(t, datastore.DataMigrationClean, markersOf(t, ds)[markerFor("prod", incarnation)])
}

// Only the corrupted target is touched.
func TestReingest_LeavesOtherTargetsAlone(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	storeTarget(t, ds, "prod")
	storeTarget(t, ds, "staging")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)
	storeUnmanaged(t, ds, "elsewhere", "staging", cleanProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Empty(t, unmanagedOn(t, ds, "prod"))
	assert.Len(t, unmanagedOn(t, ds, "staging"), 1, "another target's rows are untouched")
}

// Managed rows are never forgotten: they carry user declarations, and the query
// the migration scans with cannot reach them.
func TestReingest_NeverTouchesManagedRows(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)

	managed := &pkgmodel.Resource{
		Label: "declared", Type: "Test::Thing", Stack: "default", Target: "prod",
		NativeID: "native-declared", Managed: true,
		Properties: json.RawMessage(corruptedProps),
	}
	_, err := ds.StoreResource(managed, "cmd-seed")
	require.NoError(t, err)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	live, err := ds.QueryResources(&datastore.ResourceQuery{
		Managed: &datastore.QueryItem[bool]{Item: true, Constraint: datastore.Required},
	})
	require.NoError(t, err)
	assert.Len(t, live, 1, "a managed row carrying the same shape is left for an operator to decide about")
}

// Once a target is decided, it is never revisited: a legitimately-matching shape
// is forgotten at most once, however many times the agent boots.
func TestReingest_WipesAtMostOncePerIncarnation(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))
	require.Empty(t, unmanagedOn(t, ds, "prod"))

	// Discovery re-ingests the row, and it legitimately matches the predicate
	// again. The marker is what stops a second wipe.
	storeUnmanaged(t, ds, "reingested", "prod", corruptedProps)
	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Len(t, unmanagedOn(t, ds, "prod"), 1,
		"a decided target is never rescanned, so the re-ingested row survives")
}

// A label is reused when a target is deleted and re-created, so a marker keyed
// on the label alone would wrongly skip the new target. Keyed on the incarnation
// it does not: the new incarnation is scanned on its own merits.
//
// This only arises while the migration is still open, which a neighbour held
// back by an in-flight command arranges here. Once the migration completes
// nothing is scanned again at all, and nothing needs to be: a target minted
// afterwards was written by a build that no longer explodes dotted keys.
func TestReingest_RescansANewIncarnationOfTheSameLabel(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "innocent", "prod", cleanProps)

	// A neighbour that cannot be decided keeps the migration open.
	storeTarget(t, ds, "held-back")
	storeUnmanaged(t, ds, "blocked", "held-back", corruptedProps)
	seedIncompleteCommandTouching(t, ds, "held-back")

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))
	require.Equal(t, datastore.DataMigrationClean, markersOf(t, ds)[markerFor("prod", incarnation)])

	// Same label, fresh incarnation, now carrying the corruption.
	second := recreateTarget(t, ds, "prod")
	require.NotEqual(t, incarnation, second, "re-creating a target must mint a new incarnation")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Empty(t, unmanagedOn(t, ds, "prod"), "the new incarnation is scanned on its own merits")
	assert.Equal(t, datastore.DataMigrationWiped, markersOf(t, ds)[markerFor("prod", second)])
	assert.Equal(t, datastore.DataMigrationClean, markersOf(t, ds)[markerFor("prod", incarnation)],
		"the stale marker is inert, not removed")
}

// Crashing between the wipe and its marker leaves the tombstones in place and no
// marker, so the next run simply repeats the wipe — which is a no-op.
func TestReingest_RerunAfterACrashIsHarmless(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	rows := []*pkgmodel.Resource{storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)}

	// The wipe half of a run that died before recording anything.
	lease, err := ds.(datastore.DataMigrationCapable).AcquireDataMigrationLease(context.Background())
	require.NoError(t, err)
	require.NoError(t, lease.TombstoneResources(context.Background(), rows, reingestCommandID))
	require.NoError(t, lease.Release())

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds),
		"a re-run over already-tombstoned rows must succeed")
	assert.Empty(t, unmanagedOn(t, ds, "prod"))
	assert.Equal(t, datastore.DataMigrationClean, markersOf(t, ds)[markerFor("prod", incarnation)],
		"nothing is left to repair, so the second run records the target as clean")
}

// Once every current incarnation is decided, the completion row is written and
// later boots stop scanning entirely.
func TestReingest_StopsScanningOnceComplete(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "innocent", "prod", cleanProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	lease, err := ds.(datastore.DataMigrationCapable).AcquireDataMigrationLease(context.Background())
	require.NoError(t, err)
	done, err := lease.HasCompletion(context.Background(), DottedKeyReingestKey)
	require.NoError(t, err)
	require.NoError(t, lease.Release())
	require.True(t, done, "every current incarnation is decided, so the migration is finished")

	// Final state cannot tell "skipped" from "scanned and found nothing", so the
	// scan is injected and asked whether it ran.
	scans := &countingScanner{inner: ds}
	require.NoError(t, reingestCorruptedUnmanagedRows(ds, scans))
	assert.Zero(t, scans.calls, "a completed migration must not scan again")
}

// A deferred target leaves the migration unfinished, so no completion row is
// written and the next boot tries again.
func TestReingest_DoesNotCompleteWhileATargetIsDeferred(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)
	seedIncompleteCommandTouching(t, ds, "prod")

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Len(t, unmanagedOn(t, ds, "prod"), 1, "a command-touched target is deferred, not wiped")
	assert.NotContains(t, markersOf(t, ds), markerFor("prod", incarnation),
		"a deferral records nothing, so the next boot revisits the target")

	lease, err := ds.(datastore.DataMigrationCapable).AcquireDataMigrationLease(context.Background())
	require.NoError(t, err)
	done, err := lease.HasCompletion(context.Background(), DottedKeyReingestKey)
	require.NoError(t, err)
	require.NoError(t, lease.Release())
	assert.False(t, done, "the migration is not finished while a target is still deferred")
}

// Re-ingest mints new KSUIDs, so a target whose unmanaged rows are referenced
// from outside the family is never forgotten automatically: doing so would leave
// the reference pointing at nothing. The exclusion is terminal — an operator has
// to decide what to do — so it is recorded rather than retried forever.
func TestReingest_ExcludesATargetReferencedFromOutside(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	corrupted := storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)

	// A managed declaration pointing at the unmanaged row.
	_, err := ds.StoreResource(&pkgmodel.Resource{
		Label: "consumer", Type: "Test::Thing", Stack: "default", Target: "prod",
		NativeID: "native-consumer", Managed: true,
		Properties: json.RawMessage(
			`{"Upstream":{"$ref":"formae://` + corrupted.Ksuid + `#/Arn"}}`),
	}, "cmd-seed")
	require.NoError(t, err)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Len(t, unmanagedOn(t, ds, "prod"), 1,
		"a referenced row is left in place rather than re-minted under a new identity")
	assert.Equal(t, datastore.DataMigrationExcluded, markersOf(t, ds)[markerFor("prod", incarnation)])
}

// An exclusion counts as decided, so it does not hold the migration open
// forever: the completion row is written with an excluded target present.
func TestReingest_CompletesWithAnExcludedTargetPresent(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	storeTarget(t, ds, "prod")
	corrupted := storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)
	_, err := ds.StoreResource(&pkgmodel.Resource{
		Label: "consumer", Type: "Test::Thing", Stack: "default", Target: "prod",
		NativeID: "native-consumer", Managed: true,
		Properties: json.RawMessage(
			`{"Upstream":{"$ref":"formae://` + corrupted.Ksuid + `#/Arn"}}`),
	}, "cmd-seed")
	require.NoError(t, err)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	lease, err := ds.(datastore.DataMigrationCapable).AcquireDataMigrationLease(context.Background())
	require.NoError(t, err)
	done, err := lease.HasCompletion(context.Background(), DottedKeyReingestKey)
	require.NoError(t, err)
	require.NoError(t, lease.Release())
	assert.True(t, done, "an exclusion is a decision, so it does not block completion")
}

// References BETWEEN the target's own unmanaged rows do not exclude it: the
// whole family is re-minted together, so nothing is left dangling.
func TestReingest_WipesDespiteReferencesWithinTheFamily(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	parent := storeUnmanaged(t, ds, "parent", "prod", corruptedProps)
	storeUnmanaged(t, ds, "child", "prod",
		`{"Parent":{"$ref":"formae://`+parent.Ksuid+`#/Arn"}}`)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	assert.Empty(t, unmanagedOn(t, ds, "prod"),
		"discovery's own parent links are re-minted with the family, so they do not block the repair")
	assert.Equal(t, datastore.DataMigrationWiped, markersOf(t, ds)[markerFor("prod", incarnation)])
}

// A deferral is transient, not a refusal: once the command that caused it
// finishes, the next boot repairs the target.
func TestReingest_DefersThenWipesOnALaterBoot(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	incarnation := storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)
	seedIncompleteCommandTouching(t, ds, "prod")

	// Boot one: the command is still in flight.
	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))
	require.Len(t, unmanagedOn(t, ds, "prod"), 1, "the target is deferred, not wiped")
	require.NotContains(t, markersOf(t, ds), markerFor("prod", incarnation))

	// The command reaches a terminal state.
	completeSeededCommand(t, ds)

	// Boot two: nothing to replay, so the repair goes ahead.
	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))
	assert.Empty(t, unmanagedOn(t, ds, "prod"), "the deferral drains once the command completes")
	assert.Equal(t, datastore.DataMigrationWiped, markersOf(t, ds)[markerFor("prod", incarnation)])
}

// While a command against the target is still in flight, the migration leaves
// the rows alone — so replaying that command at startup has nothing to restore.
// The guard is what prevents the resurrection, not any ordering after it.
func TestReingest_LeavesNothingForCommandReplayToResurrect(t *testing.T) {
	ds := newFileBackedDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	storeTarget(t, ds, "prod")
	before := storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)
	seedIncompleteCommandTouching(t, ds, "prod")

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds))

	after := unmanagedOn(t, ds, "prod")
	require.Len(t, after, 1)
	assert.Equal(t, before.Ksuid, after[0].Ksuid,
		"the row keeps its identity: there is no wipe for a replayed command to undo")
}

// completeSeededCommand moves the seeded in-flight command to a terminal state,
// which is what drains a deferral.
func completeSeededCommand(t *testing.T, ds datastore.Datastore) {
	t.Helper()
	commands, err := ds.LoadIncompleteFormaCommands()
	require.NoError(t, err)
	require.NotEmpty(t, commands, "expected the seeded command to be in flight")
	for _, command := range commands {
		require.NoError(t, ds.UpdateFormaCommandProgress(
			command.ID, forma_command.CommandStateSuccess, util.TimeNow()))
	}
	remaining, err := ds.LoadIncompleteFormaCommands()
	require.NoError(t, err)
	require.Empty(t, remaining, "the command must no longer be in flight")
}

// seedIncompleteCommandTouching persists an unfinished command that acts on the
// given target, which is the state startup replays.
func seedIncompleteCommandTouching(t *testing.T, ds datastore.Datastore, targetLabel string) {
	t.Helper()
	cmd := &forma_command.FormaCommand{
		ID:         "cmd-in-flight",
		Command:    pkgmodel.CommandApply,
		State:      forma_command.CommandStateInProgress,
		Config:     config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		StartTs:    util.TimeNow(),
		ModifiedTs: util.TimeNow(),
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState:   pkgmodel.Resource{Label: "pending", Type: "Test::Thing", Stack: "default", Ksuid: util.NewID()},
				ResourceTarget: pkgmodel.Target{Label: targetLabel},
				Operation:      resource_update.OperationRead,
				State:          resource_update.ResourceUpdateStateInProgress,
				StackLabel:     "default",
			},
		},
	}
	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))
}

// countingScanner records whether the migration scanned at all.
type countingScanner struct {
	inner datastore.Datastore
	calls int
}

func (s *countingScanner) QueryResources(query *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
	s.calls++
	return s.inner.QueryResources(query)
}

// An in-memory datastore is a supported configuration, and the migration runs
// on every boot, so it has to work there. A second handle opened on ":memory:"
// is a different, empty database — not another connection to this one — so a
// lease taken that way would query a table that does not exist.
func TestReingest_WorksOnAnInMemoryDatastore(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	t.Cleanup(ds.Close)

	storeTarget(t, ds, "prod")
	storeUnmanaged(t, ds, "corrupted", "prod", corruptedProps)

	require.NoError(t, ReingestCorruptedUnmanagedRows(ds),
		"the migration must not fail the boot on an in-memory datastore")
	assert.Empty(t, unmanagedOn(t, ds, "prod"), "and it must still do its work")
}
