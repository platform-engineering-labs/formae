// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

// leaseBusyRetries and leaseBusyBackoff bound how long a boot waits for another
// process to finish its migration. Exhausting them defers the migration to the
// next boot rather than failing the boot.
const (
	leaseBusyRetries = 10
	leaseBusyBackoff = 500 * time.Millisecond
	leaseBusyTimeout = "5000"
)

// sqliteDataMigrationLease holds a write transaction on the database file for
// the whole migration, which is what excludes every other process.
//
// The connection comes from a SEPARATE single-connection *sql.DB opened on the
// same file, never from the datastore's own pool. That pool is capped at one
// connection, so pinning it would starve every ordinary read inside Go's
// database/sql — the wait happens before SQLite is ever reached, so the busy
// timeout would not even apply. With its own handle, WAL does what is wanted:
// this transaction holds the write lock while the datastore's pool keeps serving
// reads of the last committed state, which is the pre-migration snapshot the
// scan and guards are looking for.
//
// The transaction is the executor, so every marker row and tombstone the
// migration writes commits together on Release.
type sqliteDataMigrationLease struct {
	datastore.MarkerStore
	db   *sql.DB
	conn *sql.Conn
}

// privateInMemoryDataMigrationLease is the lease for a database no other process
// can reach.
//
// A private in-memory database lives inside this process, so there is no second
// agent to exclude and nothing for a lease to serialize against. It also cannot
// be opened a second time: another handle on ":memory:" is a different, empty
// database rather than another connection to this one. So the marker statements
// run on the datastore's own pool, and the migration's writes commit as they go
// instead of landing together at Release — which costs nothing here, since a
// crash takes the whole database with it either way.
type privateInMemoryDataMigrationLease struct {
	datastore.MarkerStore
}

func (l *privateInMemoryDataMigrationLease) Release() error { return nil }

// isPrivateInMemory reports whether a DSN names an in-memory database that no
// other connection can join. A shared-cache in-memory DSN is excluded: a second
// handle on it does reach the same database.
func isPrivateInMemory(dsn string) bool {
	if !strings.HasPrefix(dsn, ":memory:") && !strings.HasPrefix(dsn, "file::memory:") {
		return false
	}
	return !strings.Contains(dsn, "cache=shared")
}

// AcquireDataMigrationLease opens the lease's own connection and takes SQLite's
// write lock with BEGIN IMMEDIATE, retrying while another process holds it.
func (d DatastoreSQLite) AcquireDataMigrationLease(ctx context.Context) (datastore.DataMigrationLease, error) {
	if d.dsn == "" {
		return nil, fmt.Errorf("cannot acquire data migration lease: datastore has no file path")
	}

	if isPrivateInMemory(d.dsn) {
		return &privateInMemoryDataMigrationLease{
			MarkerStore: datastore.NewMarkerStore(d.conn, func(int) string { return "?" }),
		}, nil
	}

	db, err := sql.Open(sqliteOtelDriverName, d.dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open the data migration lease connection: %w", err)
	}
	// One connection, so the transaction below always lands on the same session
	// and the lock it takes is the one Release commits.
	db.SetMaxOpenConns(1)

	// WAL is a property of the file, already set by the datastore's own
	// connection; the busy timeout is per-connection and has to be set here.
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout="+leaseBusyTimeout); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to set the lease connection's busy timeout: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to pin the data migration lease connection: %w", err)
	}

	if err := beginImmediate(ctx, conn); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, err
	}

	return &sqliteDataMigrationLease{
		MarkerStore: datastore.NewMarkerStore(conn, func(int) string { return "?" }),
		db:          db,
		conn:        conn,
	}, nil
}

// beginImmediate takes SQLite's write lock up front rather than on the first
// write, so two boots cannot both get through their scans before either writes.
//
// The transaction is driven with explicit statements on the pinned connection
// rather than through BeginTx, which issues a deferred BEGIN of its own and
// would leave no room for the IMMEDIATE one.
func beginImmediate(ctx context.Context, conn *sql.Conn) error {
	var lastErr error
	for attempt := range leaseBusyRetries {
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isBusy(lastErr) {
			return fmt.Errorf("failed to acquire the data migration lease: %w", lastErr)
		}

		slog.Debug("data migration lease is busy, retrying",
			"attempt", attempt+1, "of", leaseBusyRetries)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(leaseBusyBackoff):
		}
	}
	return fmt.Errorf("%w: %v", datastore.ErrDataMigrationLeaseUnavailable, lastErr)
}

// isBusy reports whether an error is SQLite refusing because the database is
// locked, matched on the message because the driver is reached through an
// OpenTelemetry wrapper that does not preserve the concrete error type.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// Release commits everything the migration staged and closes the lease's own
// connection. A failure to commit rolls the whole migration back, which leaves
// the corruption in place to be repaired on the next boot.
func (l *sqliteDataMigrationLease) Release() error {
	ctx := context.Background()
	_, commitErr := l.conn.ExecContext(ctx, "COMMIT")
	if commitErr != nil {
		_, _ = l.conn.ExecContext(ctx, "ROLLBACK")
	}
	connErr := l.conn.Close()
	dbErr := l.db.Close()

	if commitErr != nil {
		return fmt.Errorf("failed to commit the data migration: %w", commitErr)
	}
	if err := errors.Join(connErr, dbErr); err != nil {
		return fmt.Errorf("failed to close the data migration lease connection: %w", err)
	}
	return nil
}
