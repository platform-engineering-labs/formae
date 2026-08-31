// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/demula/mksuid/v2"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A one-time data migration repairs rows that an older build wrote wrongly. It
// runs at startup, before any actor, and it mutates rows — so two agents booting
// against one datastore must not run it at the same time, and it must not repeat
// work it has already done.
//
// Two mechanisms carry that. A lease serializes the migration datastore-wide, so
// only one process is scanning and writing at a time. Marker rows in
// data_migrations record what was decided per target, so a repaired target is
// never repaired twice and a finished migration stops costing a scan.

// DataMigrationOutcome is what a migration decided about one target. All three
// values mean "do not look at this incarnation again"; a target that is merely
// deferred gets no row at all and is retried on the next boot.
type DataMigrationOutcome string

const (
	// DataMigrationWiped: rows were tombstoned so they re-ingest clean.
	DataMigrationWiped DataMigrationOutcome = "WIPED"
	// DataMigrationClean: scanned, nothing to repair.
	DataMigrationClean DataMigrationOutcome = "CLEAN"
	// DataMigrationExcluded: repairable, but deliberately skipped because
	// repairing it automatically would break something else. Needs operator
	// action, which is why it is terminal rather than retried.
	DataMigrationExcluded DataMigrationOutcome = "PROCESSED-EXCLUDED"
	// DataMigrationCompleted marks the global completion row.
	DataMigrationCompleted DataMigrationOutcome = "COMPLETED"
)

// ErrDataMigrationLeaseUnavailable reports that the lease could not be taken
// within its wait budget, because another process holds it. The migration defers
// to the next boot rather than failing the boot: the cost of deferring is that
// the corruption persists, visibly, which is the safe direction.
var ErrDataMigrationLeaseUnavailable = errors.New("data migration lease is held by another process")

// sqlExecutor is the subset of database/sql the marker statements use. It is
// satisfied by *sql.DB, *sql.Conn and *sql.Tx, which is what lets a lease hand
// MarkerStore whichever of those it holds. It is deliberately not part of
// DataMigrationLease: Postgres is reached through pgx, whose handles have a
// different shape, so the lease exposes the OPERATIONS a migration performs
// rather than a SQL handle to perform them with.
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DataMigrationMarker identifies one target incarnation's marker row.
type DataMigrationMarker struct {
	TargetLabel   string
	IncarnationID string
}

// DataMigrationLease is a held datastore-wide lease on data migrations.
//
// Every WRITE a migration makes goes through the lease. On SQLite the lease is a
// transaction it commits on Release, so the whole migration lands atomically.
// Reads of the rows being repaired deliberately do NOT go through it: they use
// the ordinary datastore methods and see the last committed state, which is the
// pre-migration snapshot the scan and the guards want. Marker reads are the
// exception and go through the lease, since they have to observe this run's own
// writes — the ordinary pool would not see them.
type DataMigrationLease interface {
	// LoadMarkers returns the recorded outcome per target incarnation.
	LoadMarkers(ctx context.Context, migrationKey string) (map[DataMigrationMarker]DataMigrationOutcome, error)
	// UpsertMarker records one target incarnation's outcome.
	UpsertMarker(ctx context.Context, migrationKey, targetLabel, incarnationID string, outcome DataMigrationOutcome) error
	// HasCompletion reports whether the migration has finished for good.
	HasCompletion(ctx context.Context, migrationKey string) (bool, error)
	// WriteCompletion records that every current target incarnation has a
	// marker, so later boots can skip the scan entirely.
	WriteCompletion(ctx context.Context, migrationKey string) error
	// TombstoneResources appends a delete tombstone for each resource, exactly
	// as DeleteResource does, but through the lease so the writes are part of
	// the migration. It is DB-only: no plugin is ever called, so it can only
	// ever make formae forget a row, never destroy anything in a cloud.
	TombstoneResources(ctx context.Context, resources []*pkgmodel.Resource, commandID string) error
	// Release ends the lease, committing its writes where they were staged.
	Release() error
}

// DataMigrationCapable is implemented by the datastores that can hold a lease.
//
// It is a capability rather than a method on Datastore because holding a lease
// needs a session to pin, and not every backend has one: the Aurora Data API is
// stateless HTTP. A backend that cannot hold a lease does not run the migration
// at all; the corruption persists visibly and the documented manual remediation
// applies. Adding a backend here is what makes the migration run against it.
type DataMigrationCapable interface {
	AcquireDataMigrationLease(ctx context.Context) (DataMigrationLease, error)
}

// MarkerStore implements the marker helpers over one database/sql handle. The
// backends reached that way embed it in their lease, so the marker semantics
// have one implementation; only the placeholder syntax differs between them.
// Postgres, reached through pgx, implements the same semantics against its own
// handle.
type MarkerStore struct {
	Exec sqlExecutor
	// Placeholder renders the nth (1-based) bind placeholder for the dialect.
	Placeholder func(n int) string
}

// NewMarkerStore builds a MarkerStore over a database/sql handle. Backends pass
// the transaction or pinned connection their lease holds.
func NewMarkerStore(exec sqlExecutor, placeholder func(n int) string) MarkerStore {
	return MarkerStore{Exec: exec, Placeholder: placeholder}
}

// MarkerQueries names the statements the marker helpers run, so a backend that
// cannot use MarkerStore can implement the same semantics against the same SQL.
const (
	MarkerSelectSQL = `SELECT target_label, target_incarnation_id, outcome FROM data_migrations WHERE migration_key = %s`
	MarkerUpdateSQL = `UPDATE data_migrations SET outcome = %s WHERE migration_key = %s AND target_label = %s AND target_incarnation_id = %s`
	MarkerInsertSQL = `INSERT INTO data_migrations (migration_key, target_label, target_incarnation_id, outcome) VALUES (%s, %s, %s, %s)`
	MarkerCountSQL  = `SELECT COUNT(*) FROM data_migrations WHERE migration_key = %s AND target_label = %s AND target_incarnation_id = %s`
)

// CompletionRowKey returns the reserved label/incarnation pair the global
// completion row occupies. A real target label is never empty, so the pair
// cannot collide with one.
func CompletionRowKey() (label, incarnation string) {
	return completionRowLabel, completionRowIncarnation
}

// completionRow is the reserved key pair the global completion row occupies. A
// target label is never empty, so the pair cannot collide with a real target.
const completionRowLabel, completionRowIncarnation = "", ""

func (m MarkerStore) ph(n int) string {
	if m.Placeholder == nil {
		return "?"
	}
	return m.Placeholder(n)
}

func (m MarkerStore) LoadMarkers(ctx context.Context, migrationKey string) (map[DataMigrationMarker]DataMigrationOutcome, error) {
	query := fmt.Sprintf(MarkerSelectSQL, m.ph(1))
	rows, err := m.Exec.QueryContext(ctx, query, migrationKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load data migration markers: %w", err)
	}
	defer rows.Close()

	markers := map[DataMigrationMarker]DataMigrationOutcome{}
	for rows.Next() {
		var label, incarnation, outcome string
		if err := rows.Scan(&label, &incarnation, &outcome); err != nil {
			return nil, fmt.Errorf("failed to scan data migration marker: %w", err)
		}
		markers[DataMigrationMarker{TargetLabel: label, IncarnationID: incarnation}] = DataMigrationOutcome(outcome)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read data migration markers: %w", err)
	}
	return markers, nil
}

// UpsertMarker writes one marker, updating in place if it is already there.
// Update-then-insert rather than a dialect-specific upsert clause: the lease
// makes this the only writer, and the three dialects spell ON CONFLICT three
// different ways.
func (m MarkerStore) UpsertMarker(ctx context.Context, migrationKey, targetLabel, incarnationID string, outcome DataMigrationOutcome) error {
	update := fmt.Sprintf(MarkerUpdateSQL, m.ph(1), m.ph(2), m.ph(3), m.ph(4))
	result, err := m.Exec.ExecContext(ctx, update, string(outcome), migrationKey, targetLabel, incarnationID)
	if err != nil {
		return fmt.Errorf("failed to update data migration marker: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected > 0 {
		return nil
	}

	insert := fmt.Sprintf(MarkerInsertSQL, m.ph(1), m.ph(2), m.ph(3), m.ph(4))
	if _, err := m.Exec.ExecContext(ctx, insert, migrationKey, targetLabel, incarnationID, string(outcome)); err != nil {
		return fmt.Errorf("failed to insert data migration marker: %w", err)
	}
	return nil
}

func (m MarkerStore) HasCompletion(ctx context.Context, migrationKey string) (bool, error) {
	query := fmt.Sprintf(MarkerCountSQL, m.ph(1), m.ph(2), m.ph(3))
	var count int
	if err := m.Exec.QueryRowContext(ctx, query, migrationKey, completionRowLabel, completionRowIncarnation).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to read data migration completion: %w", err)
	}
	return count > 0, nil
}

func (m MarkerStore) WriteCompletion(ctx context.Context, migrationKey string) error {
	return m.UpsertMarker(ctx, migrationKey, completionRowLabel, completionRowIncarnation, DataMigrationCompleted)
}

// TombstoneSQL is the statement set a delete tombstone is written with, shared
// so every backend's lease appends the same row DeleteResource would.
//
// The two statements are what storeResource reduces to for a delete of a
// fully-loaded resource: its KSUID-lookup fallback cannot fire (the caller
// passes rows it just read), the reaped and incarnation guards exempt deletes,
// and the equality comparison always lands on a fresh version. What is left is
// "skip if the current row is already a tombstone, otherwise append one".
const (
	TombstoneLatestOperationSQL = `SELECT operation FROM resources WHERE uri = %s ORDER BY version DESC LIMIT 1`
	TombstoneInsertSQL          = `INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid, target_incarnation_id) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`
)

// TombstoneOperation is the operation a delete tombstone carries.
const TombstoneOperation = "delete"

// TombstoneResources appends a delete tombstone per resource through the
// MarkerStore's handle, skipping any row whose current version is already one.
func (m MarkerStore) TombstoneResources(ctx context.Context, resources []*pkgmodel.Resource, commandID string) error {
	for _, resource := range resources {
		tombstoned, err := m.currentlyTombstoned(ctx, resource)
		if err != nil {
			return err
		}
		if tombstoned {
			// Tombstoning twice is a harmless no-op, which is what lets a
			// migration that crashed part-way simply run again.
			continue
		}
		if err := m.appendTombstone(ctx, resource, commandID); err != nil {
			return err
		}
	}
	return nil
}

func (m MarkerStore) currentlyTombstoned(ctx context.Context, resource *pkgmodel.Resource) (bool, error) {
	var operation string
	err := m.Exec.QueryRowContext(ctx,
		fmt.Sprintf(TombstoneLatestOperationSQL, m.ph(1)), string(resource.URI())).Scan(&operation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read the current version of %s: %w", resource.Ksuid, err)
	}
	return operation == TombstoneOperation, nil
}

func (m MarkerStore) appendTombstone(ctx context.Context, resource *pkgmodel.Resource, commandID string) error {
	placeholders := make([]any, 13)
	for i := range placeholders {
		placeholders[i] = m.ph(i + 1)
	}
	query := fmt.Sprintf(TombstoneInsertSQL, placeholders...)

	if _, err := m.Exec.ExecContext(ctx, query,
		string(resource.URI()),
		mksuid.New().String(),
		commandID,
		TombstoneOperation,
		resource.NativeID,
		resource.Stack,
		resource.Type,
		resource.Label,
		resource.Target,
		// A tombstone carries no properties, exactly as DeleteResource writes it.
		"{}",
		BoolToInt(resource.Managed),
		resource.Ksuid,
		"",
	); err != nil {
		return fmt.Errorf("failed to tombstone resource %s: %w", resource.Ksuid, err)
	}
	return nil
}
