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
)

const fcTestBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

// newFreshMSSQLDatastore creates a brand-new database on the local test
// container and returns a connected DatastoreMSSQL plus a cleanup func.
func newFreshMSSQLDatastore(t *testing.T) (datastore.Datastore, func()) {
	t.Helper()

	master, err := sql.Open("sqlserver", fcTestBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	if err := master.Ping(); err != nil {
		_ = master.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_fc_%d", time.Now().UnixNano())
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

func TestMSSQLFormaCommandRoundTrip(t *testing.T) {
	ds, cleanup := newFreshMSSQLDatastore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	commandID := "cmd-roundtrip-1"

	fc := &forma_command.FormaCommand{
		ID:          commandID,
		State:       forma_command.CommandStateNotStarted,
		StartTs:     now,
		ModifiedTs:  now,
		Command:     pkgmodel.CommandApply,
		ClientID:    "client-abc",
		Description: pkgmodel.Description{Text: "round trip", Confirm: true},
		Config:      fcconfig.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Force: true, Simulate: false},
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Operation:  types.OperationCreate,
				State:      resource_update.ResourceUpdateStateNotStarted,
				StartTs:    now,
				ModifiedTs: now,
				Version:    "v1",
				StackLabel: "my-stack",
				GroupID:    "g1",
				DesiredState: pkgmodel.Resource{
					Ksuid: "ksuid-res-1",
					Label: "res-1",
					Type:  "aws::s3::bucket",
					Stack: "my-stack",
				},
			},
		},
	}

	if err := ds.StoreFormaCommand(fc, commandID); err != nil {
		t.Fatalf("StoreFormaCommand: %v", err)
	}

	loaded, err := ds.GetFormaCommandByCommandID(commandID)
	if err != nil {
		t.Fatalf("GetFormaCommandByCommandID: %v", err)
	}

	if loaded.ID != commandID {
		t.Errorf("ID = %q, want %q", loaded.ID, commandID)
	}
	if loaded.Command != pkgmodel.CommandApply {
		t.Errorf("Command = %q, want apply", loaded.Command)
	}
	if loaded.State != forma_command.CommandStateNotStarted {
		t.Errorf("State = %q, want NotStarted", loaded.State)
	}
	if loaded.ClientID != "client-abc" {
		t.Errorf("ClientID = %q, want client-abc", loaded.ClientID)
	}
	if loaded.Description.Text != "round trip" || !loaded.Description.Confirm {
		t.Errorf("Description = %+v, want {round trip true}", loaded.Description)
	}
	if loaded.Config.Mode != pkgmodel.FormaApplyModeReconcile || !loaded.Config.Force || loaded.Config.Simulate {
		t.Errorf("Config = %+v", loaded.Config)
	}
	if !loaded.StartTs.Equal(now) {
		t.Errorf("StartTs = %v, want %v", loaded.StartTs, now)
	}
	if len(loaded.ResourceUpdates) != 1 {
		t.Fatalf("ResourceUpdates len = %d, want 1", len(loaded.ResourceUpdates))
	}
	ru := loaded.ResourceUpdates[0]
	if ru.Operation != types.OperationCreate {
		t.Errorf("RU.Operation = %q, want create", ru.Operation)
	}
	if ru.DesiredState.Ksuid != "ksuid-res-1" {
		t.Errorf("RU.DesiredState.Ksuid = %q, want ksuid-res-1", ru.DesiredState.Ksuid)
	}
	if ru.StackLabel != "my-stack" {
		t.Errorf("RU.StackLabel = %q, want my-stack", ru.StackLabel)
	}
	if ru.Version != "v1" {
		t.Errorf("RU.Version = %q, want v1", ru.Version)
	}

	// LoadFormaCommands should include the stored command.
	all, err := ds.LoadFormaCommands()
	if err != nil {
		t.Fatalf("LoadFormaCommands: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("LoadFormaCommands len = %d, want 1", len(all))
	}

	// LoadIncompleteFormaCommands should include the NotStarted command.
	incomplete, err := ds.LoadIncompleteFormaCommands()
	if err != nil {
		t.Fatalf("LoadIncompleteFormaCommands: %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("LoadIncompleteFormaCommands len = %d, want 1", len(incomplete))
	}

	// Most-recent-by-client.
	recent, err := ds.GetMostRecentFormaCommandByClientID("client-abc")
	if err != nil {
		t.Fatalf("GetMostRecentFormaCommandByClientID: %v", err)
	}
	if recent.ID != commandID {
		t.Errorf("recent.ID = %q, want %q", recent.ID, commandID)
	}

	// Update progress and confirm it persists.
	later := now.Add(5 * time.Second)
	if err := ds.UpdateFormaCommandProgress(commandID, forma_command.CommandStateInProgress, later); err != nil {
		t.Fatalf("UpdateFormaCommandProgress: %v", err)
	}
	reloaded, err := ds.GetFormaCommandByCommandID(commandID)
	if err != nil {
		t.Fatalf("GetFormaCommandByCommandID after progress: %v", err)
	}
	if reloaded.State != forma_command.CommandStateInProgress {
		t.Errorf("State after progress = %q, want InProgress", reloaded.State)
	}

	// Delete and confirm gone.
	if err := ds.DeleteFormaCommand(fc, commandID); err != nil {
		t.Fatalf("DeleteFormaCommand: %v", err)
	}
	if _, err := ds.GetFormaCommandByCommandID(commandID); err == nil {
		t.Errorf("expected error loading deleted command, got nil")
	}
}

func TestMSSQLQueryFormaCommands(t *testing.T) {
	ds, cleanup := newFreshMSSQLDatastore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)

	mk := func(id, client string, mode pkgmodel.FormaApplyMode, stack string, ts time.Time) *forma_command.FormaCommand {
		return &forma_command.FormaCommand{
			ID:         id,
			State:      forma_command.CommandStateSuccess,
			StartTs:    ts,
			ModifiedTs: ts,
			Command:    pkgmodel.CommandApply,
			ClientID:   client,
			Config:     fcconfig.FormaCommandConfig{Mode: mode},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					Operation:    types.OperationCreate,
					State:        resource_update.ResourceUpdateStateSuccess,
					StartTs:      ts,
					ModifiedTs:   ts,
					StackLabel:   stack,
					DesiredState: pkgmodel.Resource{Ksuid: "k-" + id, Label: "r-" + id, Type: "t", Stack: stack},
				},
			},
		}
	}

	cmds := []*forma_command.FormaCommand{
		mk("cmd-a", "client-1", pkgmodel.FormaApplyModeReconcile, "stack-x", now),
		mk("cmd-b", "client-2", pkgmodel.FormaApplyModePatch, "stack-y", now.Add(time.Second)),
		mk("cmd-c", "client-1", pkgmodel.FormaApplyModeReconcile, "stack-x", now.Add(2*time.Second)),
	}
	for _, c := range cmds {
		if err := ds.StoreFormaCommand(c, c.ID); err != nil {
			t.Fatalf("StoreFormaCommand %s: %v", c.ID, err)
		}
	}

	// No filter: all three, newest first.
	res, err := ds.QueryFormaCommands(&datastore.StatusQuery{})
	if err != nil {
		t.Fatalf("QueryFormaCommands(all): %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("QueryFormaCommands(all) len = %d, want 3", len(res))
	}
	if res[0].ID != "cmd-c" {
		t.Errorf("newest = %q, want cmd-c", res[0].ID)
	}

	// Filter by client_id.
	res, err = ds.QueryFormaCommands(&datastore.StatusQuery{
		ClientID: &datastore.QueryItem[string]{Item: "client-1", Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryFormaCommands(client): %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("QueryFormaCommands(client-1) len = %d, want 2", len(res))
	}

	// Filter by stack (via resource_updates).
	res, err = ds.QueryFormaCommands(&datastore.StatusQuery{
		Stack: &datastore.QueryItem[string]{Item: "stack-y", Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryFormaCommands(stack): %v", err)
	}
	if len(res) != 1 || res[0].ID != "cmd-b" {
		t.Fatalf("QueryFormaCommands(stack-y) = %+v, want [cmd-b]", res)
	}

	// Wildcard CommandID filter.
	res, err = ds.QueryFormaCommands(&datastore.StatusQuery{
		CommandID: &datastore.QueryItem[string]{Item: "cmd-*", Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryFormaCommands(wildcard): %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("QueryFormaCommands(cmd-*) len = %d, want 3", len(res))
	}
}
