// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatastore(t *testing.T) {
	// Verify we can actually connect to postgres with our test credentials
	connStr := "postgres://postgres:admin@localhost:5432/postgres"
	conn, err := pgx.Connect(context.Background(), connStr)
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	conn.Close(context.Background())

	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.PostgresDatastore,
			Postgres: pkgmodel.PostgresConfig{
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "admin",
				Database: fmt.Sprintf("test_%s", mksuid.New().String()),
			},
		}
		ds, err := postgres.NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("Failed to create Postgres datastore: %v", err)
		}
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				if d, ok := ds.(postgres.DatastorePostgres); ok {
					return d.CleanUp()
				}
				return nil
			},
		}
	})
}

// Latest-version subqueries (`r2.version > r1.version`) on the resources table
// depend on byte-order semantics because KSUIDs are base62 (mixed case) and only
// sort chronologically under byte ordering. Under the default Postgres collation,
// locale rules can put an uppercase-letter version before a chronologically-later
// lowercase-letter version, causing the "latest" filter to pick the wrong row.
// When the true latest is a delete and the locale-latest is an update, the
// resource leaks back through as "managed" even after destroy.
//
// Scenario: `update` row has version "2wQUVeqpc…" (uppercase 'U' at position 3),
// `delete` row has a chronologically-later version "2wQfTRyui…" (lowercase 'f').
// Byte order: delete > update (correct — delete is latest, exclude resource).
// Locale order: update > delete (wrong — update appears latest, resource leaks).
func TestLoadResourcesByStack_ExcludesDeletedResourceWhenVersionsMixCase(t *testing.T) {
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(ctx)

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.PostgresDatastore,
		Postgres: pkgmodel.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "admin",
			Database: fmt.Sprintf("test_collate_%s", mksuid.New().String()),
		},
	}
	ds, err := postgres.NewDatastorePostgresEnsureDatabase(ctx, cfg, "test")
	require.NoError(t, err)
	defer func() {
		if d, ok := ds.(postgres.DatastorePostgres); ok {
			_ = d.CleanUp()
		}
	}()

	// Open a second connection for direct INSERT to control the version column precisely.
	connStr := postgres.BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, cfg.Postgres.Database)
	conn, err := pgx.Connect(ctx, connStr)
	require.NoError(t, err)
	defer conn.Close(ctx) //nolint:errcheck

	// Create the stack row first so downstream code that joins against stacks works.
	_, err = conn.Exec(ctx, `INSERT INTO stacks (ksuid, version, label, description) VALUES ($1, $2, $3, $4)`,
		"stack-ksuid", "v1", "test-stack", "")
	require.NoError(t, err)

	// Earlier update row with an uppercase letter in the version's locale-significant position.
	updateVersion := "2wQUVeqpcVTSF4ROlhC9N4kMUPk"
	// Chronologically-later delete row with a lowercase letter. Byte order puts this after
	// updateVersion (correct); locale order puts it before (the bug).
	deleteVersion := "2wQfTRyuifVOB5LKSwee7d8aErH"

	uri := "formae://test-ksuid"
	data := json.RawMessage(`{"Schema":{},"Properties":{}}`)

	_, err = conn.Exec(ctx, `INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uri, updateVersion, "cmd-1", "update", "native-1", "test-stack", "type-1", "label-1", "", data, true, "test-ksuid")
	require.NoError(t, err)

	_, err = conn.Exec(ctx, `INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uri, deleteVersion, "cmd-2", "delete", "native-1", "test-stack", "type-1", "label-1", "", data, true, "test-ksuid")
	require.NoError(t, err)

	// Sanity check: under the default collation, 'U' > 'f' — the bug's precondition.
	var localeGT bool
	err = conn.QueryRow(ctx, `SELECT 'U' > 'f'`).Scan(&localeGT)
	require.NoError(t, err)
	require.True(t, localeGT, "test precondition: default collation must treat 'U' as greater than 'f'")

	results, err := ds.LoadResourcesByStack("test-stack")
	require.NoError(t, err)

	assert.Empty(t, results,
		"resource with a later delete row must be excluded from LoadResourcesByStack, "+
			"but uncollated version comparison picked the earlier update row as 'latest'")
}
