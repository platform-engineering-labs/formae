// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package datastore

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/microsoft/go-mssqldb"
	"github.com/stretchr/testify/require"
)

func TestStorageFormatFenceRollbackAndRestart(t *testing.T) {
	cases := []struct{ name, driver, dsn, dialect string }{
		{"sqlite", "sqlite3", ":memory:", "sqlite3"},
		{"postgres", "pgx", os.Getenv("FORMAE_TEST_STORAGE_FENCE_POSTGRES_DSN"), "postgres"},
		{"mssql", "sqlserver", os.Getenv("FORMAE_TEST_STORAGE_FENCE_MSSQL_DSN"), "sqlserver"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dsn == "" {
				t.Skip("isolated storage fence test DSN not configured")
			}
			db, err := sql.Open(tc.driver, tc.dsn)
			require.NoError(t, err)
			defer func(cleanup func() error) { _ = cleanup() }(db.Close)
			db.SetMaxOpenConns(1)
			testStorageFormatFence(t, db, tc.dialect)
		})
	}
}

func testStorageFormatFence(t *testing.T, db *sql.DB, dialect string) {
	var err error

	require.NoError(t, RunMigrations(db, dialect))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM db_version_v2").Scan(&count))
	require.Positive(t, count)
	// Recreate the post-migrations/pre-fence crash boundary.
	_, err = db.Exec("DROP TABLE db_version")
	require.NoError(t, err)
	rename := "ALTER TABLE db_version_v2 RENAME TO db_version"
	if dialect == "sqlserver" {
		rename = "EXEC sp_rename 'db_version_v2', 'db_version'"
	}
	_, err = db.Exec(rename)
	require.NoError(t, err)
	for prefix := 1; prefix <= len(StorageFormatFenceStatements(dialect)); prefix++ {
		tx, err := db.Begin()
		require.NoError(t, err)
		for _, stmt := range StorageFormatFenceStatements(dialect)[:prefix] {
			_, err := tx.Exec(stmt)
			require.NoError(t, err)
		}
		require.NoError(t, tx.Rollback())
		table, err := storageMigrationTable(db, dialect)
		require.NoError(t, err)
		require.Equal(t, "db_version", table)
		var preserved int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM db_version").Scan(&preserved))
		require.Equal(t, count, preserved)
	}
	require.NoError(t, RunMigrations(db, dialect))
	require.NoError(t, RunMigrations(db, dialect))
	var preserved int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM db_version_v2").Scan(&preserved))
	require.Equal(t, count, preserved)
	_, err = db.Exec("INSERT INTO db_version(storage_format) VALUES (2)")
	require.NoError(t, err)
	require.ErrorContains(t, RunMigrations(db, dialect), "invalid storage format marker")
	_, err = db.Exec("DELETE FROM db_version")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO db_version(storage_format) VALUES (999)")
	require.NoError(t, err)
	require.ErrorContains(t, RunMigrations(db, dialect), "unsupported datastore storage format")
	_, err = db.Exec("UPDATE db_version SET storage_format = 2")
	require.NoError(t, err)
}
