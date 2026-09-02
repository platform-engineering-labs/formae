// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The migration tombstones through the lease rather than through
// DeleteResource, because its writes have to be part of the migration. The two
// must therefore leave the row in the same state: these pin that equivalence, so
// the tombstone cannot drift into meaning something else.

func unmanagedResource(label, target string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Label:      label,
		Type:       "Test::Thing",
		Stack:      constants.UnmanagedStack,
		Target:     target,
		NativeID:   "native-" + label,
		Managed:    false,
		Properties: json.RawMessage(`{"a.b":"v","a":{"b":"v"}}`),
	}
}

func managedResource(label, target string) *pkgmodel.Resource {
	r := unmanagedResource(label, target)
	r.Stack = "default"
	r.Managed = true
	return r
}

func liveUnmanaged(t *testing.T, ds datastore.Datastore, target string) []*pkgmodel.Resource {
	t.Helper()
	live, err := ds.QueryResources(&datastore.ResourceQuery{
		Stack:   &datastore.QueryItem[string]{Item: constants.UnmanagedStack, Constraint: datastore.Required},
		Managed: &datastore.QueryItem[bool]{Item: false, Constraint: datastore.Required},
		Target:  &datastore.QueryItem[string]{Item: target, Constraint: datastore.Required},
	})
	require.NoError(t, err)
	return live
}

// The lease's tombstone must remove a row from live queries exactly as
// DeleteResource does.
func TestTombstoneResourcesMatchesDeleteResource(t *testing.T) {
	ctx := context.Background()

	deleteDir, leaseDir := t.TempDir(), t.TempDir()
	viaDelete := newFileDatastore(t, filepath.Join(deleteDir, "delete.db"))
	viaLease := newFileDatastore(t, filepath.Join(leaseDir, "lease.db"))

	for _, ds := range []datastore.Datastore{viaDelete, viaLease} {
		_, err := ds.StoreResource(unmanagedResource("thing", "prod"), "cmd-seed")
		require.NoError(t, err)
		require.Len(t, liveUnmanaged(t, ds, "prod"), 1, "the seeded row must be live")
	}

	// One row forgotten the ordinary way. Each datastore minted its own KSUID,
	// and the URI is derived from it, so each is looked up by its own row.
	deleteRows := liveUnmanaged(t, viaDelete, "prod")
	_, err := viaDelete.DeleteResource(deleteRows[0], "cmd-forget")
	require.NoError(t, err)

	// The same row forgotten through the lease.
	leaseRows := liveUnmanaged(t, viaLease, "prod")
	lease, err := capable(t, viaLease).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, lease.TombstoneResources(ctx, leaseRows, "cmd-forget"))
	require.NoError(t, lease.Release())

	assert.Empty(t, liveUnmanaged(t, viaDelete, "prod"), "DeleteResource removes the row from live queries")
	assert.Empty(t, liveUnmanaged(t, viaLease, "prod"), "the lease's tombstone must do the same")

	// And the row each appended is the same row, down to the columns that carry
	// meaning. Version and ksuid are excluded: the version is freshly minted
	// either way, and the ksuid is the identity being tombstoned, not an outcome.
	assert.Equal(t,
		newestRow(t, filepath.Join(deleteDir, "delete.db"), string(deleteRows[0].URI())),
		newestRow(t, filepath.Join(leaseDir, "lease.db"), string(leaseRows[0].URI())),
		"the lease must append the same tombstone DeleteResource does")
}

// newestRow reads the highest-versioned row for a URI, less the columns whose
// values are minted per write.
func newestRow(t *testing.T, path, uri string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	defer db.Close()

	var operation, data, stack, resourceType, label, target, nativeID, commandID string
	var managed int
	err = db.QueryRow(`
		SELECT operation, data, stack, type, label, target, native_id, command_id, managed
		FROM resources WHERE uri = ? ORDER BY version DESC LIMIT 1`, uri).
		Scan(&operation, &data, &stack, &resourceType, &label, &target, &nativeID, &commandID, &managed)
	require.NoError(t, err)

	return map[string]string{
		"operation":  operation,
		"data":       data,
		"stack":      stack,
		"type":       resourceType,
		"label":      label,
		"target":     target,
		"native_id":  nativeID,
		"command_id": commandID,
		"managed":    strconv.Itoa(managed),
	}
}

// Tombstoning twice is a no-op, which is what lets a migration that crashed
// part-way simply run again.
func TestTombstoneResourcesIsIdempotent(t *testing.T) {
	ctx := context.Background()
	ds := newFileDatastore(t, filepath.Join(t.TempDir(), "formae.db"))

	_, err := ds.StoreResource(unmanagedResource("thing", "prod"), "cmd-seed")
	require.NoError(t, err)
	rows := liveUnmanaged(t, ds, "prod")

	first, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, first.TombstoneResources(ctx, rows, "cmd-forget"))
	require.NoError(t, first.Release())

	second, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, second.TombstoneResources(ctx, rows, "cmd-forget"),
		"re-tombstoning an already-tombstoned row must be harmless")
	require.NoError(t, second.Release())

	assert.Empty(t, liveUnmanaged(t, ds, "prod"))
}

// Only the rows handed in are tombstoned. A managed row on the same target is
// untouched, and so is an unmanaged row on another target.
func TestTombstoneResourcesTouchesOnlyTheGivenRows(t *testing.T) {
	ctx := context.Background()
	ds := newFileDatastore(t, filepath.Join(t.TempDir(), "formae.db"))

	_, err := ds.StoreResource(unmanagedResource("target-row", "prod"), "cmd-seed")
	require.NoError(t, err)
	_, err = ds.StoreResource(managedResource("managed-row", "prod"), "cmd-seed")
	require.NoError(t, err)
	_, err = ds.StoreResource(unmanagedResource("other-target-row", "staging"), "cmd-seed")
	require.NoError(t, err)

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, lease.TombstoneResources(ctx, liveUnmanaged(t, ds, "prod"), "cmd-forget"))
	require.NoError(t, lease.Release())

	assert.Empty(t, liveUnmanaged(t, ds, "prod"), "the target's unmanaged rows are forgotten")
	assert.Len(t, liveUnmanaged(t, ds, "staging"), 1, "another target's rows are untouched")

	managed, err := ds.QueryResources(&datastore.ResourceQuery{
		Managed: &datastore.QueryItem[bool]{Item: true, Constraint: datastore.Required},
	})
	require.NoError(t, err)
	assert.Len(t, managed, 1, "a managed row on the same target is untouched")
}

// The tombstone is staged with the rest of the migration: nothing is visible to
// another connection until Release commits.
func TestTombstoneIsStagedUntilRelease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "formae.db")
	ds := newFileDatastore(t, path)

	_, err := ds.StoreResource(unmanagedResource("thing", "prod"), "cmd-seed")
	require.NoError(t, err)

	lease, err := capable(t, ds).AcquireDataMigrationLease(ctx)
	require.NoError(t, err)
	require.NoError(t, lease.TombstoneResources(ctx, liveUnmanaged(t, ds, "prod"), "cmd-forget"))

	assert.Len(t, liveUnmanaged(t, ds, "prod"), 1,
		"the ordinary pool must still see the pre-migration snapshot while the lease is held")

	require.NoError(t, lease.Release())
	assert.Empty(t, liveUnmanaged(t, ds, "prod"), "and the committed tombstone afterwards")
}
