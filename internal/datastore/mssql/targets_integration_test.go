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

	json "github.com/goccy/go-json"
	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const targetsTestBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

// newTargetsTestDS creates a fresh database on the local test container and
// returns a connected DatastoreMSSQL plus a cleanup func.
func newTargetsTestDS(t *testing.T) (datastore.Datastore, func()) {
	t.Helper()

	master, err := sql.Open("sqlserver", targetsTestBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	if err := master.Ping(); err != nil {
		_ = master.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_targets_%d", time.Now().UnixNano())
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

func TestMSSQLTargetsRoundTrip(t *testing.T) {
	ds, cleanup := newTargetsTestDS(t)
	defer cleanup()

	cfgA := json.RawMessage(`{"account":"123","region":"us-east-1"}`)
	cfgB := json.RawMessage(`{"account":"456","region":"eu-west-1"}`)

	// Create version 1.
	id, err := ds.CreateTarget(&pkgmodel.Target{
		Label:        "target-a",
		Namespace:    "ns-1",
		Config:       cfgA,
		Discoverable: true,
	})
	if err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	if id != "target-a_1" {
		t.Errorf("CreateTarget id = %q, want target-a_1", id)
	}

	// LoadTarget returns the freshly created target.
	loaded, err := ds.LoadTarget("target-a")
	if err != nil {
		t.Fatalf("LoadTarget: %v", err)
	}
	if loaded == nil {
		t.Fatalf("LoadTarget returned nil for existing target")
	}
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
	if loaded.Namespace != "ns-1" {
		t.Errorf("Namespace = %q, want ns-1", loaded.Namespace)
	}
	if !loaded.Discoverable {
		t.Errorf("Discoverable = false, want true")
	}
	if string(loaded.Config) != string(cfgA) {
		t.Errorf("Config = %s, want %s", loaded.Config, cfgA)
	}

	// LoadTarget for a missing label returns (nil, nil).
	missing, err := ds.LoadTarget("does-not-exist")
	if err != nil {
		t.Fatalf("LoadTarget(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("LoadTarget(missing) = %+v, want nil", missing)
	}

	// Update bumps the version and changes namespace/config/discoverable.
	id, err = ds.UpdateTarget(&pkgmodel.Target{
		Label:        "target-a",
		Namespace:    "ns-2",
		Config:       cfgB,
		Discoverable: false,
	})
	if err != nil {
		t.Fatalf("UpdateTarget: %v", err)
	}
	if id != "target-a_2" {
		t.Errorf("UpdateTarget id = %q, want target-a_2", id)
	}

	loaded, err = ds.LoadTarget("target-a")
	if err != nil {
		t.Fatalf("LoadTarget after update: %v", err)
	}
	if loaded.Version != 2 {
		t.Errorf("Version after update = %d, want 2", loaded.Version)
	}
	if loaded.Namespace != "ns-2" {
		t.Errorf("Namespace after update = %q, want ns-2", loaded.Namespace)
	}
	if loaded.Discoverable {
		t.Errorf("Discoverable after update = true, want false")
	}

	// Updating a non-existent target errors.
	if _, err := ds.UpdateTarget(&pkgmodel.Target{Label: "ghost"}); err == nil {
		t.Errorf("UpdateTarget(ghost) expected error, got nil")
	}

	// Create a second, discoverable target with a distinct config.
	if _, err := ds.CreateTarget(&pkgmodel.Target{
		Label:        "target-b",
		Namespace:    "ns-3",
		Config:       cfgA,
		Discoverable: true,
	}); err != nil {
		t.Fatalf("CreateTarget(target-b): %v", err)
	}

	// LoadAllTargets returns latest version per label (2 labels).
	all, err := ds.LoadAllTargets()
	if err != nil {
		t.Fatalf("LoadAllTargets: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("LoadAllTargets len = %d, want 2", len(all))
	}

	// LoadTargetsByLabels with an IN clause.
	byLabels, err := ds.LoadTargetsByLabels([]string{"target-a", "target-b", "nope"})
	if err != nil {
		t.Fatalf("LoadTargetsByLabels: %v", err)
	}
	if len(byLabels) != 2 {
		t.Fatalf("LoadTargetsByLabels len = %d, want 2", len(byLabels))
	}
	// Empty slice short-circuits to an empty result.
	empty, err := ds.LoadTargetsByLabels(nil)
	if err != nil {
		t.Fatalf("LoadTargetsByLabels(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("LoadTargetsByLabels(nil) len = %d, want 0", len(empty))
	}

	// LoadDiscoverableTargets: target-a's latest version is not discoverable,
	// target-b is, so only target-b should come back.
	disc, err := ds.LoadDiscoverableTargets()
	if err != nil {
		t.Fatalf("LoadDiscoverableTargets: %v", err)
	}
	if len(disc) != 1 {
		t.Fatalf("LoadDiscoverableTargets len = %d, want 1", len(disc))
	}
	if disc[0].Label != "target-b" {
		t.Errorf("LoadDiscoverableTargets[0].Label = %q, want target-b", disc[0].Label)
	}

	// QueryTargets: by exact label.
	q, err := ds.QueryTargets(&datastore.TargetQuery{
		Label: &datastore.QueryItem[string]{Item: "target-a", Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryTargets(label): %v", err)
	}
	if len(q) != 1 || q[0].Label != "target-a" {
		t.Fatalf("QueryTargets(label=target-a) = %+v, want [target-a]", q)
	}

	// QueryTargets: by discoverable bit.
	q, err = ds.QueryTargets(&datastore.TargetQuery{
		Discoverable: &datastore.QueryItem[bool]{Item: true, Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryTargets(discoverable): %v", err)
	}
	if len(q) != 1 || q[0].Label != "target-b" {
		t.Fatalf("QueryTargets(discoverable=true) = %+v, want [target-b]", q)
	}

	// QueryTargets: wildcard label.
	q, err = ds.QueryTargets(&datastore.TargetQuery{
		Label: &datastore.QueryItem[string]{Item: "target-*", Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryTargets(wildcard): %v", err)
	}
	if len(q) != 2 {
		t.Fatalf("QueryTargets(target-*) len = %d, want 2", len(q))
	}

	// CountResourcesInTarget: no resources stored, expect 0.
	count, err := ds.CountResourcesInTarget("target-a")
	if err != nil {
		t.Fatalf("CountResourcesInTarget: %v", err)
	}
	if count != 0 {
		t.Errorf("CountResourcesInTarget = %d, want 0", count)
	}

	// DeleteTarget removes all versions.
	delID, err := ds.DeleteTarget("target-a")
	if err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
	if delID != "target-a_deleted" {
		t.Errorf("DeleteTarget id = %q, want target-a_deleted", delID)
	}
	if loaded, err := ds.LoadTarget("target-a"); err != nil || loaded != nil {
		t.Errorf("LoadTarget after delete = (%+v, %v), want (nil, nil)", loaded, err)
	}

	// Deleting a missing target errors.
	if _, err := ds.DeleteTarget("target-a"); err == nil {
		t.Errorf("DeleteTarget(already-gone) expected error, got nil")
	}
}
