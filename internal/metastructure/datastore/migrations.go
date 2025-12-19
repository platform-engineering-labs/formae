// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

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

	// Check current version to determine if migrations are needed
	currentVersion, err := goose.GetDBVersion(db)
	if err != nil {
		// If we can't get version, proceed with migrations anyway
		currentVersion = 0
	}

	// Get the highest available migration version
	migrations, err := goose.CollectMigrations(migrationsDir, 0, goose.MaxVersion)
	if err != nil {
		slog.Error("Failed to collect migrations", "error", err)
		return err
	}

	var targetVersion int64
	if len(migrations) > 0 {
		targetVersion = migrations[len(migrations)-1].Version
	}

	// Only show migration warning if there are pending migrations
	if currentVersion < targetVersion {
		slog.Warn("Database migrations are starting. This may take a while depending on your dataset size. Please do not exit the application.",
			"currentVersion", currentVersion,
			"targetVersion", targetVersion)
	}

	startTime := time.Now()
	if err := goose.Up(db, migrationsDir); err != nil {
		slog.Error("Failed to run migrations", "error", err)
		return err
	}

	duration := time.Since(startTime)
	if currentVersion < targetVersion {
		slog.Info("Database migrations completed successfully",
			"previousVersion", currentVersion,
			"currentVersion", targetVersion,
			"duration", duration.Round(time.Millisecond).String())
	}

	return nil
}
