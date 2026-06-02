// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package mssql_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

// testMSSQLBaseDSN points at the local SQL Server test container.
// encrypt=disable: the container uses a self-signed cert.
const testMSSQLBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

func TestMSSQLMigrationsApply(t *testing.T) {
	master, err := sql.Open("sqlserver", testMSSQLBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	defer func() { _ = master.Close() }()
	if err := master.Ping(); err != nil {
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_mig_%d", time.Now().UnixNano())
	if _, err := master.Exec(fmt.Sprintf("CREATE DATABASE [%s]", dbName)); err != nil {
		t.Fatalf("create test db: %v", err)
	}
	defer func() {
		// Force-close other sessions then drop, so a lingering pooled conn
		// doesn't block the drop.
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
	}()

	db, err := sql.Open("sqlserver", testMSSQLBaseDSN+"&database="+dbName)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	if err := datastore.RunMigrations(db, "mssql"); err != nil {
		_ = db.Close()
		t.Fatalf("RunMigrations(mssql) failed: %v", err)
	}
	_ = db.Close()

	// Re-open to sanity-check the resulting schema independent of goose state.
	check, err := sql.Open("sqlserver", testMSSQLBaseDSN+"&database="+dbName)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = check.Close() }()

	for _, table := range []string{"forma_commands", "resources", "targets", "resource_updates", "stacks", "policies", "stack_policies"} {
		var n int
		if err := check.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n); err != nil {
			t.Errorf("expected table %q to exist after migrations: %v", table, err)
		}
	}
}
