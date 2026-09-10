// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"database/sql"
	"fmt"
)

const StorageFormatVersion = 2
const MigrationHistoryV2 = "db_version_v2"

// StorageFormatTableQuery checks catalog metadata without provoking a missing
// column error. A v2 history and incompatible legacy sentinel are committed in
// one transaction, so neither is ever observable alone.
func StorageFormatTableQuery(dialect string) string {
	switch dialect {
	case "sqlite3":
		return "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'db_version_v2'"
	case "mssql", "sqlserver":
		return "SELECT COUNT(*) FROM sys.tables WHERE name = 'db_version_v2' AND schema_id = SCHEMA_ID()"
	default:
		return "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'db_version_v2'"
	}
}

func StorageFormatFenceStatements(dialect string) []string {
	rename := "ALTER TABLE db_version RENAME TO db_version_v2"
	if dialect == "mssql" || dialect == "sqlserver" {
		rename = "EXEC sp_rename 'db_version', 'db_version_v2'"
	}
	return []string{rename, "CREATE TABLE db_version (storage_format INTEGER NOT NULL)", "INSERT INTO db_version (storage_format) VALUES (2)"}
}

func storageMigrationTable(db *sql.DB, dialect string) (string, error) {
	var exists int
	if err := db.QueryRow(StorageFormatTableQuery(dialect)).Scan(&exists); err != nil {
		return "", err
	}
	if exists == 0 {
		return "db_version", nil
	}
	var version, count int
	if err := db.QueryRow("SELECT COUNT(*), COALESCE(MIN(storage_format), 0) FROM db_version").Scan(&count, &version); err != nil {
		return "", fmt.Errorf("read storage format fence: %w", err)
	}
	if count != 1 {
		return "", fmt.Errorf("invalid storage format marker: expected one row, got %d", count)
	}
	if version != StorageFormatVersion {
		return "", fmt.Errorf("unsupported datastore storage format %d (supported: %d)", version, StorageFormatVersion)
	}
	return MigrationHistoryV2, nil
}

func fenceStorageFormat(db *sql.DB, dialect string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range StorageFormatFenceStatements(dialect) {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("install storage format fence: %w", err)
		}
	}
	return tx.Commit()
}
