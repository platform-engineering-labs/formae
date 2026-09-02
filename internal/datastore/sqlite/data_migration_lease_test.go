// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const testMigrationKey = "test-migration"

// newFileDatastore builds a datastore on a real file. The lease opens its own
// connection to the same file, so an in-memory database would not do: each
// handle would get its own empty database.
func newFileDatastore(t *testing.T, path string) datastore.Datastore {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: path},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	return ds
}

func capable(t *testing.T, ds datastore.Datastore) datastore.DataMigrationCapable {
	t.Helper()
	c, ok := ds.(datastore.DataMigrationCapable)
	require.True(t, ok, "the SQLite datastore must be able to hold a data migration lease")
	return c
}

func TestSQLiteDatastoreIsDataMigrationCapable(t *testing.T) {
	ds := newFileDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	capable(t, ds)
}

func TestMarkerUpsertIsIdempotent(t *testing.T) {
	ds := newFileDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	ctx := context.Background()

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)

	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationClean))
	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationWiped))

	markers, err := lease.LoadMarkers(ctx, testMigrationKey)
	require.NoError(t, err)
	require.Len(t, markers, 1, "the second write must update in place, not add a row")
	assert.Equal(t, datastore.DataMigrationWiped,
		markers[datastore.DataMigrationMarker{TargetLabel: "prod", IncarnationID: "inc-1"}])

	require.NoError(t, lease.Release())
}

// Markers are keyed by incarnation, so re-creating a target under the same label
// is a distinct row and the new incarnation is scanned again.
func TestMarkersAreKeyedByIncarnation(t *testing.T) {
	ds := newFileDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	ctx := context.Background()

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)

	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationWiped))
	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-2", datastore.DataMigrationClean))

	markers, err := lease.LoadMarkers(ctx, testMigrationKey)
	require.NoError(t, err)
	assert.Len(t, markers, 2, "a re-created target is a separate incarnation")

	require.NoError(t, lease.Release())
}

// Markers of one migration must not be visible to another.
func TestMarkersAreScopedToTheirMigration(t *testing.T) {
	ds := newFileDatastore(t, filepath.Join(t.TempDir(), "formae.db"))
	ctx := context.Background()

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)

	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationWiped))
	require.NoError(t, lease.UpsertMarker(ctx, "other-migration", "prod", "inc-1", datastore.DataMigrationClean))

	markers, err := lease.LoadMarkers(ctx, testMigrationKey)
	require.NoError(t, err)
	assert.Len(t, markers, 1)

	require.NoError(t, lease.Release())
}

func TestCompletionRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formae.db")
	ds := newFileDatastore(t, path)
	ctx := context.Background()

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)

	done, err := lease.HasCompletion(ctx, testMigrationKey)
	require.NoError(t, err)
	assert.False(t, done, "a fresh datastore has not completed the migration")

	require.NoError(t, lease.WriteCompletion(ctx, testMigrationKey))
	done, err = lease.HasCompletion(ctx, testMigrationKey)
	require.NoError(t, err)
	assert.True(t, done)

	// The completion row must not be mistaken for a target's marker.
	markers, err := lease.LoadMarkers(ctx, testMigrationKey)
	require.NoError(t, err)
	label, incarnation := datastore.CompletionRowKey()
	assert.Equal(t, datastore.DataMigrationCompleted,
		markers[datastore.DataMigrationMarker{TargetLabel: label, IncarnationID: incarnation}])

	require.NoError(t, lease.Release())

	// And it survives the release, so a later boot sees it.
	second, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	done, err = second.HasCompletion(ctx, testMigrationKey)
	require.NoError(t, err)
	assert.True(t, done, "the completion must be committed on Release")
	require.NoError(t, second.Release())
}

// Nothing the lease wrote is visible until Release commits it, which is why
// marker reads have to go through the lease rather than the ordinary pool.
func TestLeaseWritesAreStagedUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formae.db")
	dsA := newFileDatastore(t, path)
	dsB := newFileDatastore(t, path)
	ctx := context.Background()

	lease, err := capable(t, dsA).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationWiped))

	// A second process cannot take the lease while the first holds it, so it
	// cannot scan or wipe concurrently.
	_, err = capable(t, dsB).AcquireDataMigrationLease(ctx)
	require.Error(t, err, "a second boot must not acquire the lease while it is held")
	assert.True(t, errors.Is(err, datastore.ErrDataMigrationLeaseUnavailable),
		"exhausting the wait must defer the migration, not fail the boot: %v", err)

	require.NoError(t, lease.Release())

	// Once released, the second process takes it and sees the committed marker.
	after, err := capable(t, dsB).AcquireDataMigrationLease(ctx)
	require.NoError(t, err, "the lease must be available once released")
	markers, err := after.LoadMarkers(ctx, testMigrationKey)
	require.NoError(t, err)
	assert.Len(t, markers, 1, "the first run's markers are visible after its commit")
	require.NoError(t, after.Release())
}

// The lease must not be taken from the datastore's own pool: that pool holds a
// single connection, so pinning it would starve every ordinary read for the
// whole migration. This is the regression that catches a shared-pool pin.
func TestOrdinaryReadsSucceedWhileTheLeaseIsHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formae.db")
	ds := newFileDatastore(t, path)
	ctx := context.Background()

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationWiped))

	// An ordinary read through the datastore's pool, while the lease holds an
	// uncommitted write transaction on the same file.
	done := make(chan error, 1)
	go func() {
		_, err := ds.LoadAllTargets()
		done <- err
	}()

	select {
	case err := <-done:
		assert.NoError(t, err, "an ordinary read must not be blocked by the lease")
	case <-ctx.Done():
		t.Fatal("context cancelled")
	}

	require.NoError(t, lease.Release())
}
