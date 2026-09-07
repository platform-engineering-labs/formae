// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

const testMigrationKey = "test-migration"

func TestPostgresDataMigrationLease(t *testing.T) {
	ds, cleanup := newTestDatastore(t)
	defer cleanup()
	ctx := context.Background()

	capable, ok := any(ds).(datastore.DataMigrationCapable)
	require.True(t, ok, "the Postgres datastore must be able to hold a data migration lease")

	lease, err := capable.AcquireDataMigrationLease(ctx)
	require.NoError(t, err)

	t.Run("markers upsert idempotently", func(t *testing.T) {
		require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationClean))
		require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-1", datastore.DataMigrationWiped))

		markers, err := lease.LoadMarkers(ctx, testMigrationKey)
		require.NoError(t, err)
		require.Len(t, markers, 1, "the second write must update in place")
		assert.Equal(t, datastore.DataMigrationWiped,
			markers[datastore.DataMigrationMarker{TargetLabel: "prod", IncarnationID: "inc-1"}])
	})

	t.Run("markers are keyed by incarnation", func(t *testing.T) {
		require.NoError(t, lease.UpsertMarker(ctx, testMigrationKey, "prod", "inc-2", datastore.DataMigrationClean))
		markers, err := lease.LoadMarkers(ctx, testMigrationKey)
		require.NoError(t, err)
		assert.Len(t, markers, 2, "a re-created target is a separate incarnation")
	})

	t.Run("completion is recorded and read back", func(t *testing.T) {
		done, err := lease.HasCompletion(ctx, testMigrationKey)
		require.NoError(t, err)
		assert.False(t, done)

		require.NoError(t, lease.WriteCompletion(ctx, testMigrationKey))
		done, err = lease.HasCompletion(ctx, testMigrationKey)
		require.NoError(t, err)
		assert.True(t, done)
	})

	require.NoError(t, lease.Release())

	t.Run("the lease is reacquirable once released", func(t *testing.T) {
		again, err := capable.AcquireDataMigrationLease(ctx)
		require.NoError(t, err)
		require.NoError(t, again.Release())
	})
}

// A second boot must not run the migration while the first holds the lease.
// Exhausting the wait defers the migration rather than failing the boot.
func TestPostgresDataMigrationLeaseExcludesASecondHolder(t *testing.T) {
	ds, cleanup := newTestDatastore(t)
	defer cleanup()
	ctx := context.Background()

	capable := any(ds).(datastore.DataMigrationCapable)

	held, err := capable.AcquireDataMigrationLease(ctx)
	require.NoError(t, err)

	_, err = capable.AcquireDataMigrationLease(ctx)
	require.Error(t, err, "a second holder must not acquire the lease while it is held")
	assert.True(t, errors.Is(err, datastore.ErrDataMigrationLeaseUnavailable),
		"exhausting the wait must defer the migration, not fail the boot: %v", err)

	require.NoError(t, held.Release())

	after, err := capable.AcquireDataMigrationLease(ctx)
	require.NoError(t, err, "the lease must be available once released")
	require.NoError(t, after.Release())
}
