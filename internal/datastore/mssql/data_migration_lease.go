// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

// dataMigrationLockName is the application-lock resource data migrations
// serialize on. Application locks are named per database, so the name only has
// to be stable and distinct from any other use.
const dataMigrationLockName = "formae_data_migration"

// dataMigrationLockTimeoutMs is how long sp_getapplock waits for the lock before
// reporting that it could not be taken. Exhausting it defers the migration to
// the next boot rather than failing this one.
const dataMigrationLockTimeoutMs = 5000

// mssqlDataMigrationLease holds a session-scoped application lock for the whole
// migration, which is what excludes another booting agent.
//
// The lock is session-scoped rather than transaction-scoped so the migration can
// call helpers that manage transactions of their own, and it is taken on a
// PINNED connection: the pool holds several, so a pooled connection would not
// guarantee that the release reaches the session that took the lock.
type mssqlDataMigrationLease struct {
	datastore.MarkerStore
	conn *sql.Conn
}

// AcquireDataMigrationLease pins a connection and takes the application lock.
func (d *DatastoreMSSQL) AcquireDataMigrationLease(ctx context.Context) (datastore.DataMigrationLease, error) {
	conn, err := d.conn.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to pin the data migration lease connection: %w", err)
	}

	// sp_getapplock returns >= 0 when the lock was granted and < 0 when it was
	// not: -1 timed out, -3 deadlock victim, -999 bad parameter.
	var result int
	err = conn.QueryRowContext(ctx,
		`DECLARE @result int;
		 EXEC @result = sp_getapplock
		     @Resource = @p1,
		     @LockMode = 'Exclusive',
		     @LockOwner = 'Session',
		     @LockTimeout = @p2;
		 SELECT @result;`,
		dataMigrationLockName, dataMigrationLockTimeoutMs).Scan(&result)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to acquire the data migration lease: %w", err)
	}
	if result < 0 {
		conn.Close()
		return nil, fmt.Errorf("%w: sp_getapplock returned %d", datastore.ErrDataMigrationLeaseUnavailable, result)
	}

	return &mssqlDataMigrationLease{
		MarkerStore: datastore.NewMarkerStore(conn, func(n int) string { return fmt.Sprintf("@p%d", n) }),
		conn:        conn,
	}, nil
}

// Release drops the application lock on the session that took it and returns the
// connection to the pool. There is no transaction to commit: each statement
// committed as it ran, and the lock is what kept the migration alone.
func (l *mssqlDataMigrationLease) Release() error {
	ctx := context.Background()
	_, releaseErr := l.conn.ExecContext(ctx,
		`EXEC sp_releaseapplock @Resource = @p1, @LockOwner = 'Session';`,
		dataMigrationLockName)
	closeErr := l.conn.Close()

	if releaseErr != nil {
		return fmt.Errorf("failed to release the data migration lease: %w", releaseErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close the data migration lease connection: %w", closeErr)
	}
	return nil
}
