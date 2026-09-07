// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package mssql_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const stacksTestBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

// newStacksTestDS creates a brand-new database on the local test container and
// returns a connected DatastoreMSSQL plus a cleanup func. The DB name is
// stacks-specific so this test never collides with the FormaCommand fixtures.
func newStacksTestDS(t *testing.T) (datastore.Datastore, func()) {
	t.Helper()

	master, err := sql.Open("sqlserver", stacksTestBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	if err := master.Ping(); err != nil {
		_ = master.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_stacks_%d", time.Now().UnixNano())
	if _, err := master.Exec(fmt.Sprintf("CREATE DATABASE [%s]", dbName)); err != nil {
		_ = master.Close()
		t.Fatalf("create test db: %v", err)
	}

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: "mssql",
		MSSQL: pkgmodel.MSSQLConfig{
			AuthMode: pkgmodel.MSSQLAuthSQL,
			Host:     "localhost",
			Port:     1433,
			User:     "sa",
			Password: "Formae_Test_1234!",
			Database: dbName,
		},
	}

	ds, err := mssql.NewDatastoreMSSQL(context.Background(), cfg, "test-agent")
	if err != nil {
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
		_ = master.Close()
		t.Fatalf("NewDatastoreMSSQL: %v", err)
	}

	cleanup := func() {
		ds.Close()
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
		_ = master.Close()
	}
	return ds, cleanup
}

func TestMSSQLStacksRoundTrip(t *testing.T) {
	ds, cleanup := newStacksTestDS(t)
	defer cleanup()

	const label = "stacks-rt"
	const commandID = "cmd-stacks-1"

	// Create.
	createVersion, err := ds.CreateStack(&pkgmodel.Stack{Label: label, Description: "initial"}, commandID)
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	if createVersion == "" {
		t.Fatal("CreateStack returned empty version")
	}

	// Duplicate create must fail.
	if _, err := ds.CreateStack(&pkgmodel.Stack{Label: label, Description: "dup"}, commandID); err == nil {
		t.Fatal("expected duplicate CreateStack to fail, got nil")
	}

	// Get reflects the created stack.
	got, err := ds.GetStackByLabel(label)
	if err != nil {
		t.Fatalf("GetStackByLabel after create: %v", err)
	}
	if got == nil {
		t.Fatal("GetStackByLabel returned nil after create")
	}
	if got.Label != label || got.Description != "initial" {
		t.Errorf("got = {Label:%q Description:%q}, want {%q initial}", got.Label, got.Description, label)
	}
	stackID := got.ID
	if stackID == "" {
		t.Fatal("created stack has empty ID")
	}

	// Update creates a new version under the same id and updates the description.
	updateVersion, err := ds.UpdateStack(&pkgmodel.Stack{Label: label, Description: "updated"}, commandID)
	if err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}
	if updateVersion == "" || updateVersion == createVersion {
		t.Errorf("UpdateStack version = %q (create was %q); want a distinct non-empty version", updateVersion, createVersion)
	}

	got, err = ds.GetStackByLabel(label)
	if err != nil {
		t.Fatalf("GetStackByLabel after update: %v", err)
	}
	if got == nil {
		t.Fatal("GetStackByLabel returned nil after update")
	}
	if got.Description != "updated" {
		t.Errorf("after update Description = %q, want updated", got.Description)
	}
	if got.ID != stackID {
		t.Errorf("after update ID = %q, want stable %q", got.ID, stackID)
	}

	// List includes exactly the one live stack.
	stacks, err := ds.ListAllStacks()
	if err != nil {
		t.Fatalf("ListAllStacks: %v", err)
	}
	if len(stacks) != 1 {
		t.Fatalf("ListAllStacks len = %d, want 1", len(stacks))
	}
	if stacks[0].Label != label || stacks[0].Description != "updated" {
		t.Errorf("listed stack = {%q %q}, want {%q updated}", stacks[0].Label, stacks[0].Description, label)
	}
	if stacks[0].CreatedAt.IsZero() {
		t.Error("listed stack CreatedAt is zero; expected valid_from to populate it")
	}

	// CountResourcesInStack with no resources should be zero.
	count, err := ds.CountResourcesInStack(label)
	if err != nil {
		t.Fatalf("CountResourcesInStack: %v", err)
	}
	if count != 0 {
		t.Errorf("CountResourcesInStack = %d, want 0", count)
	}

	// Delete writes a tombstone and returns a fresh version.
	deleteVersion, err := ds.DeleteStack(label, commandID)
	if err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}
	if deleteVersion == "" || deleteVersion == updateVersion {
		t.Errorf("DeleteStack version = %q (update was %q); want a distinct non-empty version", deleteVersion, updateVersion)
	}

	// After delete the stack is excluded from Get and List.
	got, err = ds.GetStackByLabel(label)
	if err != nil {
		t.Fatalf("GetStackByLabel after delete: %v", err)
	}
	if got != nil {
		t.Errorf("GetStackByLabel after delete = %+v, want nil", got)
	}

	stacks, err = ds.ListAllStacks()
	if err != nil {
		t.Fatalf("ListAllStacks after delete: %v", err)
	}
	if len(stacks) != 0 {
		t.Errorf("ListAllStacks after delete len = %d, want 0", len(stacks))
	}

	// Update/Delete of a now-deleted stack must report not found.
	if _, err := ds.UpdateStack(&pkgmodel.Stack{Label: label}, commandID); err == nil {
		t.Error("expected UpdateStack on deleted stack to fail, got nil")
	}
	if _, err := ds.DeleteStack(label, commandID); err == nil {
		t.Error("expected DeleteStack on deleted stack to fail, got nil")
	}
}

func TestMSSQLStacksUnknownLabel(t *testing.T) {
	ds, cleanup := newStacksTestDS(t)
	defer cleanup()

	// Get of a never-created stack is (nil, nil).
	got, err := ds.GetStackByLabel("does-not-exist")
	if err != nil {
		t.Fatalf("GetStackByLabel(unknown): %v", err)
	}
	if got != nil {
		t.Errorf("GetStackByLabel(unknown) = %+v, want nil", got)
	}

	// Update/Delete of an unknown stack must report not found.
	if _, err := ds.UpdateStack(&pkgmodel.Stack{Label: "does-not-exist"}, "cmd"); err == nil {
		t.Error("expected UpdateStack(unknown) to fail, got nil")
	}
	if _, err := ds.DeleteStack("does-not-exist", "cmd"); err == nil {
		t.Error("expected DeleteStack(unknown) to fail, got nil")
	}

	// Count on an unknown stack is zero, not an error.
	count, err := ds.CountResourcesInStack("does-not-exist")
	if err != nil {
		t.Fatalf("CountResourcesInStack(unknown): %v", err)
	}
	if count != 0 {
		t.Errorf("CountResourcesInStack(unknown) = %d, want 0", count)
	}
}
