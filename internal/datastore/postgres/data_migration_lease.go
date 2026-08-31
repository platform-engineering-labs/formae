// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// dataMigrationLockID is the advisory-lock key data migrations serialize on.
// Advisory locks share one namespace per database, so the value only has to be
// stable and not collide with another use; formae takes no other advisory lock.
const dataMigrationLockID int64 = 7671000

const (
	leaseBusyRetries = 10
	leaseBusyBackoff = 500 * time.Millisecond
)

// postgresDataMigrationLease holds a session-scoped advisory lock for the whole
// migration, which is what excludes another booting agent.
//
// The lock is session-scoped rather than transaction-scoped so the migration can
// call helpers that manage transactions of their own, and it is taken on a
// connection ACQUIRED OUT OF THE POOL and held: a pooled connection would not
// guarantee that the unlock reaches the session that took the lock.
type postgresDataMigrationLease struct {
	conn *pgxpool.Conn
}

// AcquireDataMigrationLease pins a connection and takes the advisory lock,
// retrying while another process holds it.
func (d DatastorePostgres) AcquireDataMigrationLease(ctx context.Context) (datastore.DataMigrationLease, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to pin the data migration lease connection: %w", err)
	}

	for attempt := range leaseBusyRetries {
		var acquired bool
		// try_ rather than the blocking form, so exhausting the wait defers the
		// migration to the next boot instead of holding up the boot.
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", dataMigrationLockID).Scan(&acquired); err != nil {
			conn.Release()
			return nil, fmt.Errorf("failed to acquire the data migration lease: %w", err)
		}
		if acquired {
			return &postgresDataMigrationLease{conn: conn}, nil
		}

		slog.Debug("data migration lease is busy, retrying",
			"attempt", attempt+1, "of", leaseBusyRetries)
		select {
		case <-ctx.Done():
			conn.Release()
			return nil, ctx.Err()
		case <-time.After(leaseBusyBackoff):
		}
	}

	conn.Release()
	return nil, datastore.ErrDataMigrationLeaseUnavailable
}

func (l *postgresDataMigrationLease) LoadMarkers(ctx context.Context, migrationKey string) (map[datastore.DataMigrationMarker]datastore.DataMigrationOutcome, error) {
	rows, err := l.conn.Query(ctx, fmt.Sprintf(datastore.MarkerSelectSQL, "$1"), migrationKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load data migration markers: %w", err)
	}
	defer rows.Close()

	markers := map[datastore.DataMigrationMarker]datastore.DataMigrationOutcome{}
	for rows.Next() {
		var label, incarnation, outcome string
		if err := rows.Scan(&label, &incarnation, &outcome); err != nil {
			return nil, fmt.Errorf("failed to scan data migration marker: %w", err)
		}
		markers[datastore.DataMigrationMarker{TargetLabel: label, IncarnationID: incarnation}] =
			datastore.DataMigrationOutcome(outcome)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read data migration markers: %w", err)
	}
	return markers, nil
}

// UpsertMarker updates in place, inserting only when there was nothing to
// update. The lease makes this the only writer, so the two statements need no
// dialect-specific upsert clause between them.
func (l *postgresDataMigrationLease) UpsertMarker(ctx context.Context, migrationKey, targetLabel, incarnationID string, outcome datastore.DataMigrationOutcome) error {
	tag, err := l.conn.Exec(ctx,
		fmt.Sprintf(datastore.MarkerUpdateSQL, "$1", "$2", "$3", "$4"),
		string(outcome), migrationKey, targetLabel, incarnationID)
	if err != nil {
		return fmt.Errorf("failed to update data migration marker: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	if _, err := l.conn.Exec(ctx,
		fmt.Sprintf(datastore.MarkerInsertSQL, "$1", "$2", "$3", "$4"),
		migrationKey, targetLabel, incarnationID, string(outcome)); err != nil {
		return fmt.Errorf("failed to insert data migration marker: %w", err)
	}
	return nil
}

func (l *postgresDataMigrationLease) HasCompletion(ctx context.Context, migrationKey string) (bool, error) {
	label, incarnation := datastore.CompletionRowKey()
	var count int
	if err := l.conn.QueryRow(ctx,
		fmt.Sprintf(datastore.MarkerCountSQL, "$1", "$2", "$3"),
		migrationKey, label, incarnation).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to read data migration completion: %w", err)
	}
	return count > 0, nil
}

func (l *postgresDataMigrationLease) WriteCompletion(ctx context.Context, migrationKey string) error {
	label, incarnation := datastore.CompletionRowKey()
	return l.UpsertMarker(ctx, migrationKey, label, incarnation, datastore.DataMigrationCompleted)
}

// TombstoneResources appends a delete tombstone per resource, skipping any row
// whose current version is already one. Same statements as MarkerStore's, run
// against the pgx handle this lease holds.
func (l *postgresDataMigrationLease) TombstoneResources(ctx context.Context, resources []*pkgmodel.Resource, commandID string) error {
	for _, resource := range resources {
		var operation string
		err := l.conn.QueryRow(ctx,
			fmt.Sprintf(datastore.TombstoneLatestOperationSQL, "$1"), string(resource.URI())).Scan(&operation)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No row at all: nothing has been stored, so nothing to forget.
		case err != nil:
			return fmt.Errorf("failed to read the current version of %s: %w", resource.Ksuid, err)
		case operation == datastore.TombstoneOperation:
			// Tombstoning twice is a harmless no-op, which is what lets a
			// migration that crashed part-way simply run again.
			continue
		}

		placeholders := make([]any, 13)
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		if _, err := l.conn.Exec(ctx, fmt.Sprintf(datastore.TombstoneInsertSQL, placeholders...),
			string(resource.URI()),
			mksuid.New().String(),
			commandID,
			datastore.TombstoneOperation,
			resource.NativeID,
			resource.Stack,
			resource.Type,
			resource.Label,
			resource.Target,
			"{}",
			datastore.BoolToInt(resource.Managed),
			resource.Ksuid,
			"",
		); err != nil {
			return fmt.Errorf("failed to tombstone resource %s: %w", resource.Ksuid, err)
		}
	}
	return nil
}

// Release drops the advisory lock on the session that took it and returns the
// connection to the pool. Unlike SQLite there is no transaction to commit: each
// statement committed as it ran, and the lock is what kept the migration alone.
func (l *postgresDataMigrationLease) Release() error {
	ctx := context.Background()
	_, unlockErr := l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", dataMigrationLockID)
	l.conn.Release()
	if unlockErr != nil {
		return fmt.Errorf("failed to release the data migration lease: %w", unlockErr)
	}
	return nil
}
