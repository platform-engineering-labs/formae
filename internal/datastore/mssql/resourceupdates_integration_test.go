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
	fcconfig "github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const ruTestBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

// newRUTestDS creates a brand-new database (formae_ru_<unixnano>) and returns a
// connected DatastoreMSSQL, a raw *sql.DB handle to the same database (so
// tests can seed the resources table directly while StoreResource is still a
// stub), and a cleanup func.
func newRUTestDS(t *testing.T) (datastore.Datastore, *sql.DB, func()) {
	t.Helper()

	master, err := sql.Open("sqlserver", ruTestBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	if err := master.Ping(); err != nil {
		_ = master.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_ru_%d", time.Now().UnixNano())
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

	dropDB := func() {
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
		_ = master.Close()
	}

	ds, err := mssql.NewDatastoreMSSQL(context.Background(), cfg, "test-agent")
	if err != nil {
		dropDB()
		t.Fatalf("NewDatastoreMSSQL: %v", err)
	}

	raw, err := sql.Open("sqlserver", ruTestBaseDSN+"&database="+dbName)
	if err != nil {
		ds.Close()
		dropDB()
		t.Fatalf("open raw conn: %v", err)
	}

	cleanup := func() {
		_ = raw.Close()
		ds.Close()
		dropDB()
	}
	return ds, raw, cleanup
}

// seedResource inserts a row into the resources table directly.
func seedResource(t *testing.T, raw *sql.DB, ksuid, stack, label, rtype, version, operation string, managed bool) {
	t.Helper()
	uri := ksuid + "#" + version
	_, err := raw.Exec(`
		INSERT INTO resources (uri, version, command_id, operation, stack, type, label, target, data, managed, ksuid)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11)`,
		uri, version, "seed-cmd", operation, stack, rtype, label, "tgt", "{}", managed, ksuid)
	if err != nil {
		t.Fatalf("seed resource %s: %v", ksuid, err)
	}
}

func TestMSSQLResourceUpdatesStateLifecycle(t *testing.T) {
	ds, _, cleanup := newRUTestDS(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	commandID := "ru-cmd-1"

	fc := &forma_command.FormaCommand{
		ID:         commandID,
		State:      forma_command.CommandStateNotStarted,
		StartTs:    now,
		ModifiedTs: now,
		Command:    pkgmodel.CommandApply,
		ClientID:   "client-ru",
		Config:     fcconfig.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Operation:    types.OperationCreate,
				State:        resource_update.ResourceUpdateStateNotStarted,
				StartTs:      now,
				ModifiedTs:   now,
				Version:      "v1",
				StackLabel:   "stk",
				GroupID:      "g1",
				Retries:      2,
				Remaining:    3,
				DesiredState: pkgmodel.Resource{Ksuid: "k-a", Label: "res-a", Type: "aws::s3::bucket", Stack: "stk"},
			},
			{
				Operation:    types.OperationCreate,
				State:        resource_update.ResourceUpdateStateNotStarted,
				StartTs:      now,
				ModifiedTs:   now,
				Version:      "v1",
				StackLabel:   "stk",
				DesiredState: pkgmodel.Resource{Ksuid: "k-b", Label: "res-b", Type: "aws::ec2::instance", Stack: "stk"},
			},
		},
	}
	if err := ds.StoreFormaCommand(fc, commandID); err != nil {
		t.Fatalf("StoreFormaCommand: %v", err)
	}

	// LoadResourceUpdates round-trips both rows in ksuid order.
	loaded, err := ds.LoadResourceUpdates(commandID)
	if err != nil {
		t.Fatalf("LoadResourceUpdates: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("LoadResourceUpdates len = %d, want 2", len(loaded))
	}
	if loaded[0].DesiredState.Ksuid != "k-a" || loaded[1].DesiredState.Ksuid != "k-b" {
		t.Fatalf("ksuid order = [%s,%s], want [k-a,k-b]", loaded[0].DesiredState.Ksuid, loaded[1].DesiredState.Ksuid)
	}
	if loaded[0].Retries != 2 || loaded[0].Remaining != 3 {
		t.Errorf("retries/remaining = %d/%d, want 2/3", loaded[0].Retries, loaded[0].Remaining)
	}
	if loaded[0].State != resource_update.ResourceUpdateStateNotStarted {
		t.Errorf("state = %q, want NotStarted", loaded[0].State)
	}

	// UpdateResourceUpdateState on a single row.
	later := now.Add(time.Second)
	if err := ds.UpdateResourceUpdateState(commandID, "k-a", types.OperationCreate, resource_update.ResourceUpdateStateSuccess, later); err != nil {
		t.Fatalf("UpdateResourceUpdateState: %v", err)
	}
	loaded, _ = ds.LoadResourceUpdates(commandID)
	if loaded[0].State != resource_update.ResourceUpdateStateSuccess {
		t.Errorf("k-a state = %q, want Success", loaded[0].State)
	}
	if loaded[1].State != resource_update.ResourceUpdateStateNotStarted {
		t.Errorf("k-b state = %q, want still NotStarted", loaded[1].State)
	}

	// UpdateResourceUpdateState on a missing row returns an error.
	if err := ds.UpdateResourceUpdateState(commandID, "missing", types.OperationCreate, resource_update.ResourceUpdateStateSuccess, later); err == nil {
		t.Errorf("expected error for missing ksuid, got nil")
	}

	// BatchUpdateResourceUpdateState flips both rows to Failed.
	refs := []datastore.ResourceUpdateRef{
		{KSUID: "k-a", Operation: types.OperationCreate},
		{KSUID: "k-b", Operation: types.OperationCreate},
	}
	if err := ds.BatchUpdateResourceUpdateState(commandID, refs, resource_update.ResourceUpdateStateFailed, later); err != nil {
		t.Fatalf("BatchUpdateResourceUpdateState: %v", err)
	}
	loaded, _ = ds.LoadResourceUpdates(commandID)
	for _, ru := range loaded {
		if ru.State != resource_update.ResourceUpdateStateFailed {
			t.Errorf("%s state = %q, want Failed", ru.DesiredState.Ksuid, ru.State)
		}
	}

	// BatchUpdateResourceUpdateState with empty refs is a no-op.
	if err := ds.BatchUpdateResourceUpdateState(commandID, nil, resource_update.ResourceUpdateStateSuccess, later); err != nil {
		t.Errorf("empty batch: %v", err)
	}
}

func TestMSSQLResourceUpdatesProgress(t *testing.T) {
	ds, _, cleanup := newRUTestDS(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	commandID := "ru-cmd-prog"

	fc := &forma_command.FormaCommand{
		ID:         commandID,
		State:      forma_command.CommandStateInProgress,
		StartTs:    now,
		ModifiedTs: now,
		Command:    pkgmodel.CommandApply,
		ClientID:   "client-prog",
		Config:     fcconfig.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Operation:    types.OperationCreate,
				State:        resource_update.ResourceUpdateStateNotStarted,
				StartTs:      now,
				ModifiedTs:   now,
				DesiredState: pkgmodel.Resource{Ksuid: "k-p", Label: "res-p", Type: "aws::s3::bucket", Stack: "stk"},
			},
		},
	}
	if err := ds.StoreFormaCommand(fc, commandID); err != nil {
		t.Fatalf("StoreFormaCommand: %v", err)
	}

	p1 := plugin.TrackedProgress{ProgressResult: resource.ProgressResult{RequestID: "req-1", StatusMessage: "step 1"}}
	if err := ds.UpdateResourceUpdateProgress(commandID, "k-p", types.OperationCreate, resource_update.ResourceUpdateStateInProgress, now.Add(time.Second), p1); err != nil {
		t.Fatalf("UpdateResourceUpdateProgress 1: %v", err)
	}
	p2 := plugin.TrackedProgress{ProgressResult: resource.ProgressResult{RequestID: "req-2", StatusMessage: "step 2"}}
	if err := ds.UpdateResourceUpdateProgress(commandID, "k-p", types.OperationCreate, resource_update.ResourceUpdateStateSuccess, now.Add(2*time.Second), p2); err != nil {
		t.Fatalf("UpdateResourceUpdateProgress 2: %v", err)
	}

	loaded, err := ds.LoadResourceUpdates(commandID)
	if err != nil {
		t.Fatalf("LoadResourceUpdates: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("len = %d, want 1", len(loaded))
	}
	ru := loaded[0]
	if ru.State != resource_update.ResourceUpdateStateSuccess {
		t.Errorf("state = %q, want Success", ru.State)
	}
	if len(ru.ProgressResult) != 2 {
		t.Errorf("ProgressResult len = %d, want 2 (appended)", len(ru.ProgressResult))
	}
	if ru.MostRecentProgressResult.RequestID != "req-2" {
		t.Errorf("MostRecentProgressResult.RequestID = %q, want req-2", ru.MostRecentProgressResult.RequestID)
	}

	// Progress on a missing row errors (the SELECT finds no row).
	if err := ds.UpdateResourceUpdateProgress(commandID, "nope", types.OperationCreate, resource_update.ResourceUpdateStateSuccess, now, p1); err == nil {
		t.Errorf("expected error for missing ksuid, got nil")
	}
}

func TestMSSQLResourceUpdatesKSUIDTriplet(t *testing.T) {
	ds, raw, cleanup := newRUTestDS(t)
	defer cleanup()

	// Seed resources: k1 has two versions (latest wins), k2 latest is deleted
	// (so it must be excluded), k3 plain.
	seedResource(t, raw, "k1", "stack-a", "label-1", "AWS::S3::Bucket", "001", "create", true)
	seedResource(t, raw, "k1", "stack-a", "label-1", "AWS::S3::Bucket", "002", "update", true)
	seedResource(t, raw, "k2", "stack-a", "label-2", "AWS::EC2::Instance", "001", "create", true)
	seedResource(t, raw, "k2", "stack-a", "label-2", "AWS::EC2::Instance", "002", "delete", true)
	seedResource(t, raw, "k3", "stack-b", "label-3", "AWS::IAM::Role", "001", "create", true)

	// GetKSUIDByTriplet: case-insensitive type match, latest non-deleted.
	got, err := ds.GetKSUIDByTriplet("stack-a", "label-1", "aws::s3::bucket")
	if err != nil {
		t.Fatalf("GetKSUIDByTriplet: %v", err)
	}
	if got != "k1" {
		t.Errorf("GetKSUIDByTriplet = %q, want k1", got)
	}

	// k2's latest version is a delete, but GetKSUIDByTriplet (like
	// postgres/sqlite) returns the latest NON-deleted version (v001 -> k2)
	// rather than excluding the triplet entirely. The NOT-EXISTS-based Batch
	// variant below is the one that drops it.
	got, err = ds.GetKSUIDByTriplet("stack-a", "label-2", "AWS::EC2::Instance")
	if err != nil {
		t.Fatalf("GetKSUIDByTriplet(deleted): %v", err)
	}
	if got != "k2" {
		t.Errorf("GetKSUIDByTriplet(deleted) = %q, want k2 (latest non-deleted)", got)
	}

	// Missing triplet returns empty string, no error.
	got, err = ds.GetKSUIDByTriplet("nope", "nope", "nope")
	if err != nil || got != "" {
		t.Errorf("GetKSUIDByTriplet(missing) = (%q,%v), want (\"\",nil)", got, err)
	}

	// BatchGetKSUIDsByTriplets.
	triplets := []pkgmodel.TripletKey{
		{Stack: "stack-a", Label: "label-1", Type: "AWS::S3::Bucket"},
		{Stack: "stack-a", Label: "label-2", Type: "AWS::EC2::Instance"}, // deleted latest -> absent
		{Stack: "stack-b", Label: "label-3", Type: "AWS::IAM::Role"},
	}
	byTriplet, err := ds.BatchGetKSUIDsByTriplets(triplets)
	if err != nil {
		t.Fatalf("BatchGetKSUIDsByTriplets: %v", err)
	}
	if byTriplet[pkgmodel.TripletKey{Stack: "stack-a", Label: "label-1", Type: "AWS::S3::Bucket"}] != "k1" {
		t.Errorf("triplet k1 = %q, want k1", byTriplet[pkgmodel.TripletKey{Stack: "stack-a", Label: "label-1", Type: "AWS::S3::Bucket"}])
	}
	if byTriplet[pkgmodel.TripletKey{Stack: "stack-b", Label: "label-3", Type: "AWS::IAM::Role"}] != "k3" {
		t.Errorf("triplet k3 = %q, want k3", byTriplet[pkgmodel.TripletKey{Stack: "stack-b", Label: "label-3", Type: "AWS::IAM::Role"}])
	}
	if _, ok := byTriplet[pkgmodel.TripletKey{Stack: "stack-a", Label: "label-2", Type: "AWS::EC2::Instance"}]; ok {
		t.Errorf("deleted triplet k2 should be absent")
	}

	// Empty input.
	if m, err := ds.BatchGetKSUIDsByTriplets(nil); err != nil || len(m) != 0 {
		t.Errorf("BatchGetKSUIDsByTriplets(nil) = (%v,%v)", m, err)
	}

	// BatchGetTripletsByKSUIDs.
	byKSUID, err := ds.BatchGetTripletsByKSUIDs([]string{"k1", "k2", "k3"})
	if err != nil {
		t.Fatalf("BatchGetTripletsByKSUIDs: %v", err)
	}
	if tk := byKSUID["k1"]; tk.Stack != "stack-a" || tk.Label != "label-1" || tk.Type != "AWS::S3::Bucket" {
		t.Errorf("k1 triplet = %+v", tk)
	}
	if tk := byKSUID["k3"]; tk.Label != "label-3" {
		t.Errorf("k3 triplet = %+v", tk)
	}
	// BatchGetTripletsByKSUIDs filters operation != delete INSIDE the CTE
	// (like postgres/sqlite), so k2's surviving non-deleted v001 is returned
	// rather than the ksuid being dropped.
	if tk := byKSUID["k2"]; tk.Label != "label-2" {
		t.Errorf("k2 triplet = %+v, want label-2 (latest non-deleted)", tk)
	}

	// Empty input.
	if m, err := ds.BatchGetTripletsByKSUIDs(nil); err != nil || len(m) != 0 {
		t.Errorf("BatchGetTripletsByKSUIDs(nil) = (%v,%v)", m, err)
	}
}

func TestMSSQLResourceUpdatesStats(t *testing.T) {
	ds, raw, cleanup := newRUTestDS(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Two apply commands (one with a failed RU), seeded resources for stack/type counts.
	store := func(id, client string, ruState resource_update.ResourceUpdateState) {
		fc := &forma_command.FormaCommand{
			ID:         id,
			State:      forma_command.CommandStateSuccess,
			StartTs:    now,
			ModifiedTs: now,
			Command:    pkgmodel.CommandApply,
			ClientID:   client,
			Config:     fcconfig.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					Operation:    types.OperationCreate,
					State:        ruState,
					StartTs:      now,
					ModifiedTs:   now,
					StackLabel:   "stk",
					DesiredState: pkgmodel.Resource{Ksuid: "k-" + id, Label: "r-" + id, Type: "AWS::S3::Bucket", Stack: "stk"},
				},
			},
		}
		if err := ds.StoreFormaCommand(fc, id); err != nil {
			t.Fatalf("StoreFormaCommand %s: %v", id, err)
		}
	}
	store("cmd-ok", "client-1", resource_update.ResourceUpdateStateSuccess)
	store("cmd-fail", "client-2", resource_update.ResourceUpdateStateFailed)

	seedResource(t, raw, "rk1", "stack-a", "lbl-1", "AWS::S3::Bucket", "001", "create", true)
	seedResource(t, raw, "rk2", "stack-b", "lbl-2", "AWS::EC2::Instance", "001", "create", true)

	s, err := ds.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.Clients != 2 {
		t.Errorf("Clients = %d, want 2", s.Clients)
	}
	if s.Commands["apply"] != 2 {
		t.Errorf("Commands[apply] = %d, want 2", s.Commands["apply"])
	}
	if s.States[string(forma_command.CommandStateSuccess)] != 2 {
		t.Errorf("States[Success] = %d, want 2", s.States[string(forma_command.CommandStateSuccess)])
	}
	if s.Stacks != 2 {
		t.Errorf("Stacks = %d, want 2 (stack-a, stack-b)", s.Stacks)
	}
	if s.ManagedResources["AWS"] != 2 {
		t.Errorf("ManagedResources[AWS] = %d, want 2", s.ManagedResources["AWS"])
	}
	if s.ResourceTypes["AWS::S3::Bucket"] != 1 || s.ResourceTypes["AWS::EC2::Instance"] != 1 {
		t.Errorf("ResourceTypes = %+v, want one each", s.ResourceTypes)
	}
	if s.ResourceErrors["AWS::S3::Bucket"] != 1 {
		t.Errorf("ResourceErrors[AWS::S3::Bucket] = %d, want 1", s.ResourceErrors["AWS::S3::Bucket"])
	}
}
