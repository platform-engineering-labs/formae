// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
)

//go:embed migrations_sqlite/*.sql
var embedMigrationsSQLite embed.FS

//go:embed migrations_postgres/*.sql
var embedMigrationsPostgres embed.FS

func runMigrations(db *sql.DB, dialect string) error {
	var migrationsFS embed.FS
	var migrationsDir string

	switch dialect {
	case "sqlite3":
		migrationsFS = embedMigrationsSQLite
		migrationsDir = "migrations_sqlite"
	case "postgres":
		migrationsFS = embedMigrationsPostgres
		migrationsDir = "migrations_postgres"
	default:
		return fmt.Errorf("unsupported dialect: %s", dialect)
	}

	goose.SetBaseFS(migrationsFS)
	goose.SetTableName("db_version")

	if err := goose.SetDialect(dialect); err != nil {
		slog.Error("Failed to set goose dialect", "dialect", dialect, "error", err)
		return err
	}

	if err := goose.Up(db, migrationsDir); err != nil {
		slog.Error("Failed to run migrations", "error", err)
		return err
	}

	slog.Debug("Database migrations completed successfully")
	return nil
}
