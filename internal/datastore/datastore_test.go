// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit || integration

package datastore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/aurora"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

// prepareDatastore creates a datastore for testing. The backend is selected via
// the FORMAE_TEST_DATASTORE_TYPE environment variable (defaults to sqlite).
// Aurora connections also require FORMAE_TEST_AURORA_* variables.
func prepareDatastore() (datastore.Datastore, error) {
	switch os.Getenv("FORMAE_TEST_DATASTORE_TYPE") {
	case pkgmodel.PostgresDatastore:
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
			return nil, fmt.Errorf("failed to setup Postgres datastore: %w", err)
		}

		return ds, nil

	case pkgmodel.AuroraDataAPIDatastore:
		clusterArn := os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN")
		secretArn := os.Getenv("FORMAE_TEST_AURORA_SECRET_ARN")
		if clusterArn == "" || secretArn == "" {
			return nil, fmt.Errorf("Aurora Data API requires FORMAE_TEST_AURORA_CLUSTER_ARN and FORMAE_TEST_AURORA_SECRET_ARN")
		}
		auroraDatabase := os.Getenv("FORMAE_TEST_AURORA_DATABASE")
		if auroraDatabase == "" {
			auroraDatabase = "formae"
		}
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.AuroraDataAPIDatastore,
			AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{
				ClusterARN: clusterArn,
				SecretARN:  secretArn,
				Database:   auroraDatabase,
				Region:     os.Getenv("FORMAE_TEST_AURORA_REGION"),
				Endpoint:   os.Getenv("FORMAE_TEST_AURORA_ENDPOINT"),
			},
		}

		ds, err := aurora.NewDatastoreAuroraDataAPI(context.Background(), cfg, "test")
		if err != nil {
			return nil, fmt.Errorf("failed to setup Aurora Data API datastore: %w", err)
		}

		// Clean up any stale data from previous test runs
		if d, ok := ds.(*aurora.DatastoreAuroraDataAPI); ok {
			if err := d.CleanUp(); err != nil {
				slog.Warn("Failed to clean up datastore", "error", err)
			}
		}

		return ds, nil

	default:
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.SqliteDatastore,
			Sqlite: pkgmodel.SqliteConfig{
				FilePath: ":memory:",
			},
		}

		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		if err != nil {
			return nil, fmt.Errorf("failed to setup SQLite datastore: %w", err)
		}

		return ds, nil
	}
}

// cleanupDatastore cleans up test databases after tests complete.
// For Postgres, this drops the test database. For SQLite in-memory, this is a no-op.
func cleanupDatastore(ds datastore.Datastore) {
	switch d := ds.(type) {
	case postgres.DatastorePostgres:
		_ = d.CleanUp()
	case dssqlite.DatastoreSQLite:
		_ = d.CleanUp()
	case *aurora.DatastoreAuroraDataAPI:
		_ = d.CleanUp()
	}
}

func TestMain(m *testing.M) {
	m.Run()
	os.Exit(0)
}

type AssumeRolePolicyDocument struct {
	Version   string `json:"Version"`
	Statement []struct {
		Effect    string `json:"Effect"`
		Principal struct {
			Service string `json:"Service"`
		} `json:"Principal"`
		Action string `json:"Action"`
	} `json:"Statement"`
}

type IAMRole struct {
	RoleName                 string                   `json:"RoleName"`
	AssumeRolePolicyDocument AssumeRolePolicyDocument `json:"AssumeRolePolicyDocument"`
	Description              string                   `json:"Description,omitempty"`
	MaxSessionDuration       int                      `json:"MaxSessionDuration,omitempty"`
}

func TestDatastore_FormaApplyTest(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		app1 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{DesiredState: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "target1", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateSuccess},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateSuccess,
		}

		app2 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ResourceTarget:           pkgmodel.Target{Label: "target2", Namespace: "default", Config: json.RawMessage("{}")},
					MostRecentProgressResult: plugin.TrackedProgress{ProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")}},
					DesiredState:             pkgmodel.Resource{Properties: json.RawMessage("{}")},
					State:                    resource_update.ResourceUpdateStateRejected},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateFailed,
		}

		app3 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ResourceTarget:           pkgmodel.Target{Label: "target3", Namespace: "default", Config: json.RawMessage("{}")},
					MostRecentProgressResult: plugin.TrackedProgress{ProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")}},
					DesiredState:             pkgmodel.Resource{Properties: json.RawMessage("{}")},
					State:                    resource_update.ResourceUpdateStateFailed},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateFailed,
		}

		app4 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ResourceTarget:           pkgmodel.Target{Label: "target4", Namespace: "default", Config: json.RawMessage("{}")},
					MostRecentProgressResult: plugin.TrackedProgress{ProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")}},
					DesiredState: pkgmodel.Resource{
						Properties:         json.RawMessage("{}"),
						ReadOnlyProperties: json.RawMessage("{}"),
					},
					State: resource_update.ResourceUpdateStateInProgress},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateInProgress,
		}
		err := ds.StoreFormaCommand(app1, app1.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(app2, app2.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(app3, app3.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(app4, app4.ID)
		assert.NoError(t, err)

		commands, err := ds.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, commands, 4)

		incomplete, err := ds.LoadIncompleteFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, incomplete, 1)

		// Compare the essential fields rather than the entire object
		assert.Equal(t, app4.ID, incomplete[0].ID)
		assert.Equal(t, app4.Command, incomplete[0].Command)
		assert.Equal(t, resource_update.ResourceUpdateStateInProgress, incomplete[0].ResourceUpdates[0].State)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_LoadIncompleteFormaCommandsTest(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		cmd1 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{DesiredState: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "cmd1-target", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateInProgress},
			},
			Command: pkgmodel.CommandSync,
			State:   forma_command.CommandStateInProgress,
		}

		cmd2 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{DesiredState: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "cmd2-target", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateInProgress},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateInProgress,
		}

		err := ds.StoreFormaCommand(cmd1, cmd1.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(cmd2, cmd2.ID)
		assert.NoError(t, err)

		incomplete, err := ds.LoadIncompleteFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, incomplete, 1)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_GetFormaApplyByFormaHash(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		app1 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					PriorState: pkgmodel.Resource{
						Properties: json.RawMessage("null"),
					},
					MostRecentProgressResult: plugin.TrackedProgress{ProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")}},
					ResourceTarget:           pkgmodel.Target{Label: "hash-target", Namespace: "default", Config: json.RawMessage("{}")},
					DesiredState:             pkgmodel.Resource{Properties: json.RawMessage("{}")},
					State:                    resource_update.ResourceUpdateStateSuccess,
				},
			},
		}

		err := ds.StoreFormaCommand(app1, app1.ID)
		assert.NoError(t, err)

		retrieved, err := ds.GetFormaCommandByCommandID(app1.ID)
		assert.NoError(t, err)
		assert.Equal(t, app1, retrieved)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_StoreAndLoad_FormaCommand_OptionalFields(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	cmd := &forma_command.FormaCommand{
		ID:          util.NewID(),
		ClientID:    "synchronizer",
		Command:     pkgmodel.CommandApply,
		State:       forma_command.CommandStatePending,
		Description: pkgmodel.Description{Text: "deploy production stack"},
		Config:      config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState:   pkgmodel.Resource{Properties: json.RawMessage("{}")},
				ResourceTarget: pkgmodel.Target{Label: "t", Namespace: "default", Config: json.RawMessage("{}")},
				State:          resource_update.ResourceUpdateStateNotStarted,
			},
		},
	}

	err = ds.StoreFormaCommand(cmd, cmd.ID)
	assert.NoError(t, err)

	loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
	assert.NoError(t, err)
	assert.Equal(t, "synchronizer", loaded.ClientID)
	assert.Equal(t, "deploy production stack", loaded.Description.Text)
	assert.Equal(t, pkgmodel.FormaApplyModePatch, loaded.Config.Mode)
}

func TestDatastore_StoreFormaCommand_SyncSkipsResourceUpdates(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	resourceKsuid := util.NewID()

	// Non-sync command: resource updates should be stored
	applyCmd := &forma_command.FormaCommand{
		ID:          util.NewID(),
		Command:     pkgmodel.CommandApply,
		State:       forma_command.CommandStatePending,
		Description: pkgmodel.Description{},
		Config:      config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{
					Ksuid:      resourceKsuid,
					Label:      "test-resource",
					Type:       "AWS::S3::Bucket",
					Stack:      "default",
					Properties: json.RawMessage(`{"BucketName":"test"}`),
				},
				ResourceTarget: pkgmodel.Target{Label: "aws-target", Namespace: "AWS", Config: json.RawMessage(`{}`)},
				Operation:      resource_update.OperationCreate,
				State:          resource_update.ResourceUpdateStateNotStarted,
			},
		},
	}

	err = ds.StoreFormaCommand(applyCmd, applyCmd.ID)
	assert.NoError(t, err)

	loadedApply, err := ds.GetFormaCommandByCommandID(applyCmd.ID)
	assert.NoError(t, err)
	assert.Len(t, loadedApply.ResourceUpdates, 1, "non-sync command should have resource updates stored")
	assert.Equal(t, resourceKsuid, loadedApply.ResourceUpdates[0].DesiredState.Ksuid)

	// Sync command: resource updates should NOT be stored
	syncKsuid := util.NewID()
	syncCmd := &forma_command.FormaCommand{
		ID:          util.NewID(),
		Command:     pkgmodel.CommandSync,
		State:       forma_command.CommandStatePending,
		Description: pkgmodel.Description{},
		Config:      config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{
					Ksuid:      syncKsuid,
					Label:      "sync-resource",
					Type:       "AWS::S3::Bucket",
					Stack:      "default",
					Properties: json.RawMessage(`{"BucketName":"sync"}`),
				},
				ResourceTarget: pkgmodel.Target{Label: "aws-target", Namespace: "AWS", Config: json.RawMessage(`{}`)},
				Operation:      resource_update.OperationRead,
				State:          resource_update.ResourceUpdateStateNotStarted,
			},
		},
	}

	err = ds.StoreFormaCommand(syncCmd, syncCmd.ID)
	assert.NoError(t, err)

	loadedSync, err := ds.GetFormaCommandByCommandID(syncCmd.ID)
	assert.NoError(t, err)
	assert.Empty(t, loadedSync.ResourceUpdates, "sync command resource updates should not be stored upfront")
}

func TestDatastore_GetMostRecentFormaCommandByClientID(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		clientID := "test"
		olderTime, _ := time.Parse(time.RFC3339, "2023-01-01T10:00:00Z")
		olderCommand := &forma_command.FormaCommand{
			Description: pkgmodel.Description{},
			ClientID:    clientID,
			StartTs:     olderTime,
			Command:     pkgmodel.CommandApply,
		}

		newerTime, _ := time.Parse(time.RFC3339, "2023-01-02T10:00:00Z")
		newerCommand := &forma_command.FormaCommand{
			Description: pkgmodel.Description{},
			ClientID:    clientID,
			StartTs:     newerTime,
			Command:     pkgmodel.CommandApply,
		}

		// Store both commands
		err := ds.StoreFormaCommand(olderCommand, olderCommand.ID)
		assert.NoError(t, err)

		err = ds.StoreFormaCommand(newerCommand, newerCommand.ID)
		assert.NoError(t, err)

		// Retrieve the most recent command
		retrieved, err := ds.GetMostRecentFormaCommandByClientID(clientID)
		assert.NoError(t, err)

		// The most recent command should be the newer one
		assert.Equal(t, newerCommand.StartTs, retrieved.StartTs)

		// Test with non-existent client ID
		_, err = ds.GetMostRecentFormaCommandByClientID("non-existent-client")
		assert.Error(t, err)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_GetMostRecentNonReconcileFormaCommandsByStack(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		target := &pkgmodel.Target{
			Label:     "default-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		reconcileStack1 := &forma_command.FormaCommand{
			ID:          "reconcile-stack1-id",
			Description: pkgmodel.Description{},
			Command:     pkgmodel.CommandApply,
			Config: config.FormaCommandConfig{
				Mode: pkgmodel.FormaApplyModeReconcile,
			},
			StartTs: util.TimeNow().Add(-4 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-1",
				},
			},
		}
		syncStack1 := &forma_command.FormaCommand{
			ID:          "sync-stack1-id",
			Description: pkgmodel.Description{},
			Command:     pkgmodel.CommandSync,
			Config:      config.FormaCommandConfig{},
			StartTs:     util.TimeNow().Add(-3 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-1",
				},
			},
		}
		reconcileStack2 := &forma_command.FormaCommand{
			ID:          "reconcile-stack2-id",
			Description: pkgmodel.Description{},
			Command:     pkgmodel.CommandApply,
			Config: config.FormaCommandConfig{
				Mode: pkgmodel.FormaApplyModeReconcile,
			},
			StartTs: util.TimeNow().Add(-4 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-2",
				},
			},
		}
		syncStack2 := &forma_command.FormaCommand{
			ID:          "sync-stack2-id",
			Description: pkgmodel.Description{},
			Command:     pkgmodel.CommandSync,
			Config:      config.FormaCommandConfig{},
			StartTs:     util.TimeNow().Add(-3 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-2",
				},
			},
		}
		patchStack2 := &forma_command.FormaCommand{
			ID:          "patch-stack2-id",
			Description: pkgmodel.Description{},
			Command:     pkgmodel.CommandApply,
			Config: config.FormaCommandConfig{
				Mode: pkgmodel.FormaApplyModePatch,
			},
			StartTs: util.TimeNow().Add(-2 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-2",
				},
			},
		}

		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-1",
			Stack:    "stack-1",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-1",
			Target:   "default-target",
		}, reconcileStack1.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(reconcileStack1, reconcileStack1.ID)
		assert.NoError(t, err)

		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-2",
			Stack:    "stack-1",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-1b",
			Target:   "default-target",
		}, syncStack1.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(syncStack1, syncStack1.ID)
		assert.NoError(t, err)

		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-3",
			Stack:    "stack-2",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-2",
			Target:   "default-target",
		}, reconcileStack2.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(reconcileStack2, reconcileStack2.ID)
		assert.NoError(t, err)

		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-4",
			Stack:    "stack-2",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-2b",
			Target:   "default-target",
		}, syncStack2.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(syncStack2, syncStack2.ID)
		assert.NoError(t, err)

		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-5",
			Stack:    "stack-2",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-2c",
			Target:   "default-target",
		}, patchStack2.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(patchStack2, patchStack2.ID)
		assert.NoError(t, err)

		modifications, err := ds.GetResourceModificationsSinceLastReconcile("stack-2")
		assert.NoError(t, err)

		assert.Len(t, modifications, 2)
		assert.Equal(t, "stack-2", modifications[0].Stack)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_QueryFormaCommands(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		for i := range 20 {
			command := &forma_command.FormaCommand{
				Description: pkgmodel.Description{},
				ClientID:    fmt.Sprintf("client-%d", i%5),
				StartTs:     util.TimeNow().Add(time.Duration(-i) * time.Hour),
				Command: func() pkgmodel.Command {
					if i%2 == 0 {
						return pkgmodel.CommandApply
					}
					return pkgmodel.CommandDestroy
				}(),
				State: forma_command.CommandStateInProgress,
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						DesiredState: pkgmodel.Resource{
							Properties: json.RawMessage(fmt.Sprintf(`{"key": "value-%d"}`, i)),
							Stack:      fmt.Sprintf("stack-%d", i%2),
						},
						StackLabel: fmt.Sprintf("stack-%d", i%2),
						State:      resource_update.ResourceUpdateStateSuccess,
					},
					{
						DesiredState: pkgmodel.Resource{
							Properties: json.RawMessage(fmt.Sprintf(`{"key": "value-%d"}`, i)),
							Stack:      fmt.Sprintf("stack-%d", i%2),
						},
						StackLabel: fmt.Sprintf("stack-%d", i%2),
						State:      resource_update.ResourceUpdateStateInProgress,
					},
				},
			}
			command.ID = fmt.Sprintf("command-%d", i)
			err := ds.StoreFormaCommand(command, command.ID)
			assert.NoError(t, err)
		}

		query := &datastore.StatusQuery{
			ClientID: &datastore.QueryItem[string]{
				Item:       "client-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "client-1", result.ClientID)
		}

		query = &datastore.StatusQuery{
			CommandID: &datastore.QueryItem[string]{
				Item:       results[0].ID,
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, query.CommandID.Item, results[0].ID)

		query = &datastore.StatusQuery{
			Command: &datastore.QueryItem[string]{
				Item:       string(pkgmodel.CommandApply),
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.Len(t, results, 10)
		for _, result := range results {
			assert.Equal(t, pkgmodel.CommandApply, result.Command)
		}

		query = &datastore.StatusQuery{
			Status: &datastore.QueryItem[string]{
				Item:       string(forma_command.CommandStateSuccess),
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.Empty(t, results)
		for _, result := range results {
			found := false
			for _, rc := range result.ResourceUpdates {
				if rc.State == resource_update.ResourceUpdateStateSuccess {
					found = true
					break
				}
			}
			assert.True(t, found)
		}

		query = &datastore.StatusQuery{
			ClientID: &datastore.QueryItem[string]{
				Item:       "client-2",
				Constraint: datastore.Required,
			},
			Command: &datastore.QueryItem[string]{
				Item:       string(pkgmodel.CommandApply),
				Constraint: datastore.Required,
			},
			Status: &datastore.QueryItem[string]{
				Item:       "inprogress",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "client-2", result.ClientID)
			assert.Equal(t, pkgmodel.CommandApply, result.Command)
			found := false
			for _, rc := range result.ResourceUpdates {
				if rc.State == resource_update.ResourceUpdateStateInProgress {
					found = true
					break
				}
			}
			assert.True(t, found)
		}

		query = &datastore.StatusQuery{
			Stack: &datastore.QueryItem[string]{
				Item:       "stack-1",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)

		for _, result := range results {
			found := false
			for _, rc := range result.ResourceUpdates {
				if string(rc.DesiredState.Stack) == "stack-1" {
					found = true
					break
				}
			}
			assert.True(t, found)
		}

		query = &datastore.StatusQuery{
			Stack: &datastore.QueryItem[string]{
				Item:       "stack-1",
				Constraint: datastore.Excluded,
			},
		}
		results, err = ds.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			found := false
			for _, rc := range result.ResourceUpdates {
				if string(rc.DesiredState.Stack) == "stack-1" {
					found = true
					break
				}
			}
			assert.False(t, found)
		}
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_StoreResource(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		target := &pkgmodel.Target{
			Label:     "target-1",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Target:   "target-1",
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
			Managed: false,
		}

		_, err = ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)

		assert.NotEmpty(t, results[0].Ksuid)
		assert.Equal(t, resource.URI(), results[0].URI())

		assert.Equal(t, resource.NativeID, results[0].NativeID)
		assert.Equal(t, resource.Stack, results[0].Stack)
		assert.Equal(t, resource.Type, results[0].Type)
		assert.Equal(t, resource.Label, results[0].Label)
		assert.Equal(t, resource.Target, results[0].Target)
		assert.Equal(t, resource.Managed, results[0].Managed)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_UpdateResource(t *testing.T) {
	t.Run("Should create a new version if the resource properties changed", func(t *testing.T) {
		if ds, err := prepareDatastore(); err == nil {
			defer cleanupDatastore(ds)
			originalResource := &pkgmodel.Resource{
				Ksuid:    util.NewID(),
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "value"
				}`),
			}
			originalVersion, err := ds.StoreResource(originalResource, "test-command-1")
			assert.NoError(t, err)

			newVersion, err := ds.StoreResource(&pkgmodel.Resource{
				Ksuid:    originalResource.Ksuid,
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "new-value"
				}`),
			}, "test-command-2")
			assert.NoError(t, err)

			newFromDb, err := ds.LoadResourceById(originalResource.Ksuid)
			assert.NoError(t, err)

			assert.NotEqual(t, originalVersion, newVersion)
			assert.JSONEq(t, `{"key": "new-value"}`, string(newFromDb.Properties))
		} else {
			t.Fatalf("Failed to prepare datastore: %v\n", err)
		}
	})

	t.Run("Should not create a new version if the read-only properties changed", func(t *testing.T) {
		if ds, err := prepareDatastore(); err == nil {
			defer cleanupDatastore(ds)
			originalResource := &pkgmodel.Resource{
				Ksuid:    util.NewID(),
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "value"
				}`),
				ReadOnlyProperties: json.RawMessage(`{
				"ro-key": "ro-value"
				}`),
			}
			originalVersion, err := ds.StoreResource(originalResource, "test-command-1")
			assert.NoError(t, err)

			newVersion, err := ds.StoreResource(&pkgmodel.Resource{
				Ksuid:    originalResource.Ksuid,
				NativeID: "native-1",
				Stack:    "stack-1",
				Type:     "type-1",
				Label:    "label-1",
				Properties: json.RawMessage(`{
				"key": "value"
				}`),
				ReadOnlyProperties: json.RawMessage(`{
				"ro-key": "new-ro-value"
				}`),
			}, "test-command-2")
			assert.NoError(t, err)

			newFromDb, err := ds.LoadResourceById(originalResource.Ksuid)
			assert.NoError(t, err)

			assert.Equal(t, originalVersion, newVersion)
			assert.JSONEq(t, `{"ro-key": "new-ro-value"}`, string(newFromDb.ReadOnlyProperties))
		} else {
			t.Fatalf("Failed to prepare datastore: %v\n", err)
		}
	})
}

func TestDatastore_DeleteResource(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
		}

		_, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		_, err = ds.DeleteResource(resource, "test-command")
		assert.NoError(t, err)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Empty(t, results)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_QueryResources(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		for i := range 7 {
			target := &pkgmodel.Target{
				Label:     fmt.Sprintf("target-%d", i),
				Namespace: "default",
				Config:    json.RawMessage(`{}`),
			}
			_, err := ds.CreateTarget(target)
			assert.NoError(t, err)
		}

		for i := range 10 {
			resource := &pkgmodel.Resource{
				NativeID: fmt.Sprintf("native-%d", i),
				Stack:    fmt.Sprintf("stack-%d", i%3),
				Type:     fmt.Sprintf("type-%d", i%4),
				Label:    fmt.Sprintf("label-%d", i%5),
				Target:   fmt.Sprintf("target-%d", i%7),
				Managed:  i%2 == 0,
				Properties: json.RawMessage(fmt.Sprintf(`{
				"key": "value-%d"
				}`, i)),
			}
			_, err := ds.StoreResource(resource, "test-command")
			assert.NoError(t, err)
		}

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		for _, result := range results {
			assert.Equal(t, "native-1", result.NativeID)
		}

		query = &datastore.ResourceQuery{
			Stack: &datastore.QueryItem[string]{
				Item:       "stack-2",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "stack-2", result.Stack)
		}

		query = &datastore.ResourceQuery{
			Type: &datastore.QueryItem[string]{
				Item:       "type-3",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "type-3", result.Type)
		}

		query = &datastore.ResourceQuery{
			Label: &datastore.QueryItem[string]{
				Item:       "label-4",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "label-4", result.Label)
		}

		query = &datastore.ResourceQuery{
			Target: &datastore.QueryItem[string]{
				Item:       "target-4",
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "target-4", result.Target)
		}

		query = &datastore.ResourceQuery{}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 10)

		query = &datastore.ResourceQuery{
			Managed: &datastore.QueryItem[bool]{
				Item:       true,
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 5)

		query = &datastore.ResourceQuery{
			Managed: &datastore.QueryItem[bool]{
				Item:       false,
				Constraint: datastore.Required,
			},
		}
		results, err = ds.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 5)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// Storing the same resource twice should not create duplicates and should return the same version ID
func TestDatastore_StoreResource_SameResourceTwiceReturnsSameVersionId(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Target:   "test-target",
			Managed:  true,
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
		}

		firstVersionId, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		secondVersionId, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		assert.Equal(t, firstVersionId, secondVersionId)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "native-1",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, resource.URI(), results[0].URI())
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_LoadResourceByNativeID(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		resource := &pkgmodel.Resource{
			NativeID: "native-123",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Properties: json.RawMessage(`{
			"key": "value"
		}`),
			Managed: false,
		}

		_, err := ds.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		loadedResource, err := ds.LoadResourceByNativeID("native-123", "type-1")
		assert.NoError(t, err)
		assert.NotNil(t, loadedResource)
		assert.Equal(t, resource.NativeID, loadedResource.NativeID)
		assert.Equal(t, resource.Stack, loadedResource.Stack)
		assert.Equal(t, resource.Type, loadedResource.Type)

		// Negative test - wrong type
		nonExistentResource, err := ds.LoadResourceByNativeID("native-123", "wrong-type")
		assert.NoError(t, err)
		assert.Nil(t, nonExistentResource)

		// Negative test - non-existent native ID
		nonExistentResource2, err := ds.LoadResourceByNativeID("non-existent", "type-1")
		assert.NoError(t, err)
		assert.Nil(t, nonExistentResource2)

		_, err = ds.DeleteResource(resource, "test-delete-command")
		assert.NoError(t, err)

		deletedResource, err := ds.LoadResourceByNativeID("native-123", "type-1")
		assert.NoError(t, err)
		assert.Nil(t, deletedResource, "Deleted resource should not be found")
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// TestDatastore_LoadResourceByNativeID_DifferentTypes verifies that resources
// with the same native ID but different types are properly distinguished
func TestDatastore_LoadResourceByNativeID_DifferentTypes(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		// Store two resources with same native ID but different types
		resource1 := &pkgmodel.Resource{
			NativeID: "shared-id-123",
			Type:     "AWS::S3::Bucket",
			Stack:    "stack-1",
			Label:    "bucket-1",
			Properties: json.RawMessage(`{
				"BucketName": "my-bucket"
			}`),
			Managed: false,
		}
		resource2 := &pkgmodel.Resource{
			NativeID: "shared-id-123",
			Type:     "AWS::IAM::Role",
			Stack:    "stack-1",
			Label:    "role-1",
			Properties: json.RawMessage(`{
				"RoleName": "my-role"
			}`),
			Managed: false,
		}

		_, err := ds.StoreResource(resource1, "cmd-1")
		assert.NoError(t, err)
		_, err = ds.StoreResource(resource2, "cmd-2")
		assert.NoError(t, err)

		// Should return bucket when querying for bucket type
		loaded1, err := ds.LoadResourceByNativeID("shared-id-123", "AWS::S3::Bucket")
		assert.NoError(t, err)
		assert.NotNil(t, loaded1, "Bucket should be found")
		assert.Equal(t, "AWS::S3::Bucket", loaded1.Type)
		assert.Equal(t, "bucket-1", loaded1.Label)

		// Should return role when querying for role type
		loaded2, err := ds.LoadResourceByNativeID("shared-id-123", "AWS::IAM::Role")
		assert.NoError(t, err)
		assert.NotNil(t, loaded2, "Role should be found")
		assert.Equal(t, "AWS::IAM::Role", loaded2.Type)
		assert.Equal(t, "role-1", loaded2.Label)

		// Should return nil for wrong type
		loaded3, err := ds.LoadResourceByNativeID("shared-id-123", "AWS::EC2::Instance")
		assert.NoError(t, err)
		assert.Nil(t, loaded3, "Non-existent type should return nil")
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// Store some test resources with known triplets
func TestDatastore_BatchGetKSUIDsByTriplets(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		resource1 := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "resource-1",
			Type:       "AWS::S3::Bucket",
			NativeID:   "bucket-1",
			Properties: json.RawMessage(`{"BucketName": "test-bucket-1"}`),
			Managed:    true,
		}

		resource2 := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "resource-2",
			Type:       "AWS::EC2::VPC",
			NativeID:   "vpc-1",
			Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
			Managed:    true,
		}

		resource3 := &pkgmodel.Resource{
			Stack:      "other-stack",
			Label:      "resource-3",
			Type:       "AWS::IAM::Role",
			NativeID:   "role-1",
			Properties: json.RawMessage(`{"RoleName": "test-role"}`),
			Managed:    true,
		}

		// Store the resources
		_, err := ds.StoreResource(resource1, "test-command-1")
		assert.NoError(t, err)
		_, err = ds.StoreResource(resource2, "test-command-2")
		assert.NoError(t, err)
		_, err = ds.StoreResource(resource3, "test-command-3")
		assert.NoError(t, err)

		// Get the actual KSUIDs from the stored resources by querying them back
		storedResource1, err := ds.LoadResourceByNativeID("bucket-1", "AWS::S3::Bucket")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource1)

		storedResource2, err := ds.LoadResourceByNativeID("vpc-1", "AWS::EC2::VPC")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource2)

		storedResource3, err := ds.LoadResourceByNativeID("role-1", "AWS::IAM::Role")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource3)

		// Test basic batch lookup
		triplets := []pkgmodel.TripletKey{
			{Stack: "test-stack", Label: "resource-1", Type: "AWS::S3::Bucket"},
			{Stack: "test-stack", Label: "resource-2", Type: "AWS::EC2::VPC"},
			{Stack: "other-stack", Label: "resource-3", Type: "AWS::IAM::Role"},
		}

		results, err := ds.BatchGetKSUIDsByTriplets(triplets)
		assert.NoError(t, err)
		assert.Len(t, results, 3)

		// Verify the correct KSUIDs are returned
		assert.Equal(t, storedResource1.Ksuid, results[triplets[0]])
		assert.Equal(t, storedResource2.Ksuid, results[triplets[1]])
		assert.Equal(t, storedResource3.Ksuid, results[triplets[2]])

		// Test with non-existent triplets
		mixedTriplets := []pkgmodel.TripletKey{
			{Stack: "test-stack", Label: "resource-1", Type: "AWS::S3::Bucket"},         // exists
			{Stack: "non-existent", Label: "resource-x", Type: "AWS::Lambda::Function"}, // doesn't exist
			{Stack: "other-stack", Label: "resource-3", Type: "AWS::IAM::Role"},         // exists
		}

		mixedResults, err := ds.BatchGetKSUIDsByTriplets(mixedTriplets)
		assert.NoError(t, err)
		assert.Len(t, mixedResults, 2) // Only 2 should be found

		// Verify existing resources are found
		assert.Equal(t, storedResource1.Ksuid, mixedResults[mixedTriplets[0]])
		assert.Equal(t, storedResource3.Ksuid, mixedResults[mixedTriplets[2]])

		// Verify non-existent resource is not in results
		_, exists := mixedResults[mixedTriplets[1]]
		assert.False(t, exists, "Non-existent triplet should not be in results")

		// Test with empty input
		emptyResults, err := ds.BatchGetKSUIDsByTriplets([]pkgmodel.TripletKey{})
		assert.NoError(t, err)
		assert.Empty(t, emptyResults)

		// Test that deleted resources are NOT returned (they should be filtered out)
		_, err = ds.DeleteResource(storedResource1, "delete-command")
		assert.NoError(t, err)

		afterDeleteResults, err := ds.BatchGetKSUIDsByTriplets(triplets)
		assert.NoError(t, err)
		assert.Len(t, afterDeleteResults, 2, "Deleted resources should not be returned")

		// Verify deleted resource is NOT in results
		_, exists = afterDeleteResults[triplets[0]]
		assert.False(t, exists, "Deleted resource should not be in results")

		// Verify other resources are still there
		assert.Equal(t, storedResource2.Ksuid, afterDeleteResults[triplets[1]])
		assert.Equal(t, storedResource3.Ksuid, afterDeleteResults[triplets[2]])
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// Create and store initial resource (simulating initial create)
func TestDatastore_BatchGetKSUIDsByTriplets_PatchScenario(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		initialResource := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			NativeID:   "bucket-123",
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Managed:    true,
		}

		// Store the initial resource
		_, err := ds.StoreResource(initialResource, "initial-command")
		assert.NoError(t, err)

		// Get the stored resource to see what KSUID it got
		storedResource, err := ds.LoadResourceByNativeID("bucket-123", "FakeAWS::S3::Bucket")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource)
		assert.NotEmpty(t, storedResource.Ksuid)

		t.Logf("Initial resource stored with KSUID: %s", storedResource.Ksuid)

		// Now simulate the patch scenario - try to look up the same triplet
		triplet := pkgmodel.TripletKey{
			Stack: "test-stack",
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
		}

		t.Logf("Looking up triplet: %+v", triplet)

		results, err := ds.BatchGetKSUIDsByTriplets([]pkgmodel.TripletKey{triplet})
		assert.NoError(t, err)

		t.Logf("Batch lookup results: %+v", results)

		// Verify the lookup found the resource
		ksuid, exists := results[triplet]
		assert.True(t, exists, "Batch lookup should find the existing resource")
		assert.Equal(t, storedResource.Ksuid, ksuid, "Should return the same KSUID")
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_DifferentResourceTypesSameNativeId(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		bucket := &pkgmodel.Resource{
			Stack:    "static-website-stack",
			Label:    "WebsiteBucket",
			Type:     "AWS::S3::Bucket",
			NativeID: "my-unique-bucket-name",
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"BucketName": "my-unique-bucket-name",
				"WebsiteConfiguration": {"IndexDocument": "index.html"}
			}`),
			Managed: true,
		}

		bucketPolicy := &pkgmodel.Resource{
			Stack:    "static-website-stack",
			Label:    "WebsiteBucketPolicy",
			Type:     "AWS::S3::BucketPolicy",
			NativeID: "my-unique-bucket-name", // Same NativeId as bucket!
			Target:   "aws-target",
			Properties: json.RawMessage(`{
				"Bucket": "my-unique-bucket-name",
				"PolicyDocument": {"Version": "2012-10-17", "Statement": []}
			}`),
			Managed: true,
		}

		_, err := ds.StoreResource(bucket, "test-command-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, bucket.Ksuid, "Bucket should have a KSUID assigned")

		_, err = ds.StoreResource(bucketPolicy, "test-command-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, bucketPolicy.Ksuid, "BucketPolicy should have a KSUID assigned")

		assert.NotEqual(t, bucket.Ksuid, bucketPolicy.Ksuid,
			"Bucket and BucketPolicy should have different KSUIDs even though they share the same NativeId")

		loadedBucket, err := ds.LoadResource(bucket.URI())
		assert.NoError(t, err)
		assert.NotNil(t, loadedBucket)
		assert.Equal(t, "AWS::S3::Bucket", loadedBucket.Type)
		assert.Equal(t, "WebsiteBucket", loadedBucket.Label)
		assert.Equal(t, bucket.Ksuid, loadedBucket.Ksuid)

		loadedBucketPolicy, err := ds.LoadResource(bucketPolicy.URI())
		assert.NoError(t, err)
		assert.NotNil(t, loadedBucketPolicy)
		assert.Equal(t, "AWS::S3::BucketPolicy", loadedBucketPolicy.Type)
		assert.Equal(t, "WebsiteBucketPolicy", loadedBucketPolicy.Label)
		assert.Equal(t, bucketPolicy.Ksuid, loadedBucketPolicy.Ksuid)

		bucket.Properties = json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"WebsiteConfiguration": {"IndexDocument": "index.html", "ErrorDocument": "error.html"}
		}`)
		_, err = ds.StoreResource(bucket, "test-command-2")
		assert.NoError(t, err)

		reloadedBucketPolicy, err := ds.LoadResource(bucketPolicy.URI())
		assert.NoError(t, err)
		assert.NotNil(t, reloadedBucketPolicy)
		assert.JSONEq(t, string(bucketPolicy.Properties), string(reloadedBucketPolicy.Properties),
			"BucketPolicy should remain unchanged after Bucket update")

		_, err = ds.DeleteResource(bucketPolicy, "test-delete-command")
		assert.NoError(t, err)

		query := &datastore.ResourceQuery{
			NativeID: &datastore.QueryItem[string]{
				Item:       "my-unique-bucket-name",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryResources(query)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(results), "Should find only the Bucket after BucketPolicy deletion")
		assert.Equal(t, "AWS::S3::Bucket", results[0].Type, "Remaining resource should be the Bucket")

	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// TestGetResourceModificationsSinceLastReconcile_WithIntermediateReconcileCommand verifies
// that an intermediate reconcile command (for a different stack or target) doesn't affect
// modification tracking for the original stack.
func TestGetResourceModificationsSinceLastReconcile_WithIntermediateReconcileCommand(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		stackReconcile := &forma_command.FormaCommand{
			ID:              "stack-reconcile-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			StartTs:         util.TimeNow().Add(-10 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{{StackLabel: "test-stack"}},
		}
		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "bucket-1",
			Stack:    "test-stack",
			Type:     "AWS::S3::Bucket",
			Label:    "bucket-1",
			Target:   "default-target",
		}, stackReconcile.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(stackReconcile, stackReconcile.ID)
		assert.NoError(t, err)

		stackPatchA := &forma_command.FormaCommand{
			ID:              "stack-patch-a-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
			StartTs:         util.TimeNow().Add(-8 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{{StackLabel: "test-stack"}},
		}
		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "bucket-2",
			Stack:    "test-stack",
			Type:     "AWS::S3::Bucket",
			Label:    "bucket-2",
			Target:   "default-target",
		}, stackPatchA.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(stackPatchA, stackPatchA.ID)
		assert.NoError(t, err)

		intermediateReconcile := &forma_command.FormaCommand{
			ID:              "intermediate-reconcile-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			StartTs:         util.TimeNow().Add(-6 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{},
		}
		err = ds.StoreFormaCommand(intermediateReconcile, intermediateReconcile.ID)
		assert.NoError(t, err)

		stackPatchB := &forma_command.FormaCommand{
			ID:              "stack-patch-b-id",
			Command:         pkgmodel.CommandApply,
			Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
			StartTs:         util.TimeNow().Add(-4 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{{StackLabel: "test-stack"}},
		}
		_, err = ds.StoreResource(&pkgmodel.Resource{
			NativeID: "bucket-3",
			Stack:    "test-stack",
			Type:     "AWS::S3::Bucket",
			Label:    "bucket-3",
			Target:   "default-target",
		}, stackPatchB.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(stackPatchB, stackPatchB.ID)
		assert.NoError(t, err)

		modifications, err := ds.GetResourceModificationsSinceLastReconcile("test-stack")
		assert.NoError(t, err)

		assert.Len(t, modifications, 2, "Should include both patches despite intermediate reconcile")
		labels := make(map[string]bool)
		for _, mod := range modifications {
			labels[mod.Label] = true
		}
		assert.True(t, labels["bucket-2"], "Should include first patch")
		assert.True(t, labels["bucket-3"], "Should include second patch")

	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func setupQueryTargetsTestData(ds datastore.Datastore, t *testing.T) {
	targets := []*pkgmodel.Target{
		{
			Label:        "prod-us-east-1",
			Namespace:    "AWS",
			Discoverable: true,
			Config:       json.RawMessage(`{"Region":"us-east-1","AccountID":"123"}`),
		},
		{
			Label:        "prod-us-west-2",
			Namespace:    "AWS",
			Discoverable: true,
			Config:       json.RawMessage(`{"Region":"us-west-2","AccountID":"123"}`),
		},
		{
			Label:        "dev-us-east-1",
			Namespace:    "AWS",
			Discoverable: false,
			Config:       json.RawMessage(`{"Region":"us-east-1","AccountID":"456"}`),
		},
		{
			Label:        "tailscale-main",
			Namespace:    "TAILSCALE",
			Discoverable: true,
			Config:       json.RawMessage(`{"Tailnet":"example.com"}`),
		},
	}

	for _, target := range targets {
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)
	}
}

func TestDatastore_QueryTargets_All(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	setupQueryTargetsTestData(ds, t)

	got, err := ds.QueryTargets(&datastore.TargetQuery{})
	assert.NoError(t, err)
	assert.Len(t, got, 4)
}

func TestDatastore_QueryTargets_ByNamespace(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	setupQueryTargetsTestData(ds, t)

	got, err := ds.QueryTargets(&datastore.TargetQuery{
		Namespace: &datastore.QueryItem[string]{
			Item:       "AWS",
			Constraint: datastore.Required,
		},
	})
	assert.NoError(t, err)
	assert.Len(t, got, 3)
	for _, target := range got {
		assert.Equal(t, "AWS", target.Namespace)
	}
}

func TestDatastore_QueryTargets_ByDiscoverable(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	setupQueryTargetsTestData(ds, t)

	got, err := ds.QueryTargets(&datastore.TargetQuery{
		Discoverable: &datastore.QueryItem[bool]{
			Item:       true,
			Constraint: datastore.Required,
		},
	})
	assert.NoError(t, err)
	assert.Len(t, got, 3)
	for _, target := range got {
		assert.True(t, target.Discoverable)
	}
}

func TestDatastore_QueryTargets_ByLabel(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	setupQueryTargetsTestData(ds, t)

	got, err := ds.QueryTargets(&datastore.TargetQuery{
		Label: &datastore.QueryItem[string]{
			Item:       "prod-us-east-1",
			Constraint: datastore.Required,
		},
	})
	assert.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "prod-us-east-1", got[0].Label)
}

func TestDatastore_QueryTargets_DiscoverableAWS(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	setupQueryTargetsTestData(ds, t)

	got, err := ds.QueryTargets(&datastore.TargetQuery{
		Namespace: &datastore.QueryItem[string]{
			Item:       "AWS",
			Constraint: datastore.Required,
		},
		Discoverable: &datastore.QueryItem[bool]{
			Item:       true,
			Constraint: datastore.Required,
		},
	})
	assert.NoError(t, err)
	assert.Len(t, got, 2)
	for _, target := range got {
		assert.Equal(t, "AWS", target.Namespace)
		assert.True(t, target.Discoverable)
	}
}

func TestDatastore_QueryTargets_NonDiscoverable(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	setupQueryTargetsTestData(ds, t)

	got, err := ds.QueryTargets(&datastore.TargetQuery{
		Discoverable: &datastore.QueryItem[bool]{
			Item:       false,
			Constraint: datastore.Required,
		},
	})
	assert.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "dev-us-east-1", got[0].Label)
	assert.False(t, got[0].Discoverable)
}

func TestDatastore_QueryTargets_Versioning(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	target := &pkgmodel.Target{
		Label:        "version-test",
		Namespace:    "AWS",
		Discoverable: false,
		Config:       json.RawMessage(`{"Version":"1"}`),
	}

	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	target.Discoverable = true
	target.Config = json.RawMessage(`{"Version":"2"}`)
	_, err = ds.UpdateTarget(target)
	assert.NoError(t, err)

	results, err := ds.QueryTargets(&datastore.TargetQuery{
		Label: &datastore.QueryItem[string]{
			Item:       "version-test",
			Constraint: datastore.Required,
		},
	})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.True(t, results[0].Discoverable)
	assert.JSONEq(t, `{"Version":"2"}`, string(results[0].Config))
}

func TestDatastore_CountResourcesInTarget(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create target
	target := &pkgmodel.Target{
		Label:        "target-count-test",
		Namespace:    "test-namespace",
		Config:       json.RawMessage(`{}`),
		Discoverable: false,
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Initially no resources
	count, err := ds.CountResourcesInTarget("target-count-test")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add a resource to the target
	resource := &pkgmodel.Resource{
		Stack:  "default",
		Label:  "test-resource",
		Type:   "AWS::S3::Bucket",
		Target: "target-count-test",
		Ksuid:  mksuid.New().String(),
	}
	_, err = ds.StoreResource(resource, "cmd-1")
	assert.NoError(t, err)

	// Now count should be 1
	count, err = ds.CountResourcesInTarget("target-count-test")
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDatastore_DeleteTarget_Success(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create target
	target := &pkgmodel.Target{
		Label:        "delete-target-test",
		Namespace:    "test-namespace",
		Config:       json.RawMessage(`{}`),
		Discoverable: false,
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Verify target exists
	loaded, err := ds.LoadTarget("delete-target-test")
	assert.NoError(t, err)
	assert.NotNil(t, loaded)

	// Delete target
	version, err := ds.DeleteTarget("delete-target-test")
	assert.NoError(t, err)
	assert.Equal(t, "delete-target-test_deleted", version)

	// Verify target no longer exists
	loaded, err = ds.LoadTarget("delete-target-test")
	assert.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestDatastore_UpdateTarget_NotFound_ReturnsError(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	_, err = ds.UpdateTarget(&pkgmodel.Target{
		Label:     "non-existent-target",
		Namespace: "default",
		Config:    json.RawMessage(`{}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestDatastore_DeleteTarget_NotFound(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Try to delete non-existent target
	_, err = ds.DeleteTarget("non-existent-target")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}


// Stack tests

func TestDatastore_CreateStack(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	stack := &pkgmodel.Stack{
		Label:       "my-stack",
		Description: "A test stack",
	}

	version, err := ds.CreateStack(stack, "cmd-1")
	assert.NoError(t, err)
	assert.NotEmpty(t, version) // Version is a ksuid now

	// Verify we can retrieve it
	retrieved, err := ds.GetStackByLabel("my-stack")
	assert.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.NotEmpty(t, retrieved.ID) // Should have a ksuid ID
	assert.Equal(t, "my-stack", retrieved.Label)
	assert.Equal(t, "A test stack", retrieved.Description)
}

func TestDatastore_CreateStack_AlreadyExists(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	stack := &pkgmodel.Stack{
		Label:       "duplicate-stack",
		Description: "First stack",
	}

	_, err = ds.CreateStack(stack, "cmd-1")
	assert.NoError(t, err)

	// Try to create another stack with the same label
	stack2 := &pkgmodel.Stack{
		Label:       "duplicate-stack",
		Description: "Second stack",
	}
	_, err = ds.CreateStack(stack2, "cmd-2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stack already exists")
}

func TestDatastore_GetStackByLabel_NotFound(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Try to get a non-existent stack
	retrieved, err := ds.GetStackByLabel("non-existent")
	assert.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestDatastore_UpdateStack(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create initial stack
	stack := &pkgmodel.Stack{
		Label:       "update-test",
		Description: "Initial description",
	}
	_, err = ds.CreateStack(stack, "cmd-1")
	assert.NoError(t, err)

	// Get the original ID
	original, _ := ds.GetStackByLabel("update-test")
	originalID := original.ID

	// Sleep to ensure KSUID is in a different second (KSUID has 1-second precision)
	time.Sleep(1100 * time.Millisecond)

	// Update the description
	stack.Description = "Updated description"
	version, err := ds.UpdateStack(stack, "cmd-2")
	assert.NoError(t, err)
	assert.NotEmpty(t, version)

	// Verify the update
	retrieved, err := ds.GetStackByLabel("update-test")
	assert.NoError(t, err)
	assert.Equal(t, "Updated description", retrieved.Description)
	assert.Equal(t, originalID, retrieved.ID) // ID should remain the same
}

func TestDatastore_DeleteStack(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Check for pre-existing stacks
	existingStacks, _ := ds.ListAllStacks()
	t.Logf("DEBUG: Pre-existing stacks count: %d", len(existingStacks))
	for _, s := range existingStacks {
		t.Logf("DEBUG: Pre-existing stack: label=%s, id=%s", s.Label, s.ID)
	}

	// Create a stack
	stack := &pkgmodel.Stack{
		Label:       "delete-test",
		Description: "To be deleted",
	}
	createVersion, err := ds.CreateStack(stack, "cmd-1")
	assert.NoError(t, err)
	t.Logf("DEBUG: Created stack with version: %s", createVersion)

	// Sleep to ensure KSUID is in a different second (KSUID has 1-second precision)
	time.Sleep(1100 * time.Millisecond)

	// Delete it (tombstone)
	deleteVersion, err := ds.DeleteStack("delete-test", "cmd-2")
	assert.NoError(t, err)
	assert.NotEmpty(t, deleteVersion)
	t.Logf("DEBUG: Deleted stack with version: %s", deleteVersion)
	t.Logf("DEBUG: Version comparison - deleteVersion > createVersion: %v", deleteVersion > createVersion)

	// Verify it's no longer retrievable
	retrieved, err := ds.GetStackByLabel("delete-test")
	assert.NoError(t, err)
	if retrieved != nil {
		t.Logf("DEBUG: GetStackByLabel returned non-nil! ID=%s, Description=%s", retrieved.ID, retrieved.Description)
	}
	assert.Nil(t, retrieved)
}

func TestDatastore_DeleteStack_ThenRecreate(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create a stack
	stack := &pkgmodel.Stack{
		Label:       "recreate-test",
		Description: "Original stack",
	}
	_, err = ds.CreateStack(stack, "cmd-1")
	assert.NoError(t, err)

	original, _ := ds.GetStackByLabel("recreate-test")
	originalID := original.ID

	// Sleep to ensure KSUID is in a different second (KSUID has 1-second precision)
	time.Sleep(1100 * time.Millisecond)

	// Delete it
	_, err = ds.DeleteStack("recreate-test", "cmd-2")
	assert.NoError(t, err)

	// Sleep to ensure KSUID is in a different second
	time.Sleep(1100 * time.Millisecond)

	// Recreate with same label
	stack2 := &pkgmodel.Stack{
		Label:       "recreate-test",
		Description: "New stack with same label",
	}
	_, err = ds.CreateStack(stack2, "cmd-3")
	assert.NoError(t, err)

	// Verify it's a new stack with different ID
	retrieved, err := ds.GetStackByLabel("recreate-test")
	assert.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.NotEqual(t, originalID, retrieved.ID) // Should have a new ID
	assert.Equal(t, "New stack with same label", retrieved.Description)
}

func TestDatastore_CountResourcesInStack(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create stack
	stack := &pkgmodel.Stack{
		Label:       "count-test",
		Description: "Stack for counting",
	}
	_, err = ds.CreateStack(stack, "cmd-1")
	assert.NoError(t, err)

	// Initially no resources
	count, err := ds.CountResourcesInStack("count-test")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add a resource to the stack
	resource := &pkgmodel.Resource{
		Stack:  "count-test",
		Label:  "test-resource",
		Type:   "AWS::S3::Bucket",
		Target: "formae://target1",
		Ksuid:  mksuid.New().String(),
	}
	_, err = ds.StoreResource(resource, "cmd-1")
	assert.NoError(t, err)

	// Now count should be 1
	count, err = ds.CountResourcesInStack("count-test")
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDatastore_FindResourcesDependingOn(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create target
	target := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Create the "parent" resource (the one being deleted)
	parentKsuid := mksuid.New().String()
	parentResource := &pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "parent-bucket-native",
		Stack:      "test-stack",
		Label:      "parent-bucket",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
	}
	_, err = ds.StoreResource(parentResource, "cmd-1")
	assert.NoError(t, err)

	// Create a "child" resource that references the parent via $ref
	// This is the actual format stored in the database
	childKsuid := mksuid.New().String()
	childProperties := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:s3:::my-bucket"}}`, parentKsuid)
	childResource := &pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "child-role-native",
		Stack:      "test-stack",
		Label:      "child-role",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(childProperties),
	}
	_, err = ds.StoreResource(childResource, "cmd-1")
	assert.NoError(t, err)

	// Create an unrelated resource (no reference)
	unrelatedKsuid := mksuid.New().String()
	unrelatedResource := &pkgmodel.Resource{
		Ksuid:      unrelatedKsuid,
		NativeID:   "unrelated-native",
		Stack:      "test-stack",
		Label:      "unrelated-resource",
		Type:       "AWS::Lambda::Function",
		Target:     "test-target",
		Properties: json.RawMessage(`{"FunctionName":"my-function"}`),
	}
	_, err = ds.StoreResource(unrelatedResource, "cmd-1")
	assert.NoError(t, err)

	// Find resources depending on the parent
	dependents, err := ds.FindResourcesDependingOn(parentKsuid)
	assert.NoError(t, err)
	assert.Len(t, dependents, 1, "Should find exactly one dependent resource")
	assert.Equal(t, childKsuid, dependents[0].Ksuid)
	assert.Equal(t, "child-role", dependents[0].Label)
}

func TestDatastore_FindResourcesDependingOn_MultipleRefs(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create target
	target := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Create the parent resource
	parentKsuid := mksuid.New().String()
	parentResource := &pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "vpc-native",
		Stack:      "test-stack",
		Label:      "vpc",
		Type:       "AWS::EC2::VPC",
		Target:     "test-target",
		Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
	}
	_, err = ds.StoreResource(parentResource, "cmd-1")
	assert.NoError(t, err)

	// Create multiple resources that depend on the parent
	for i := 0; i < 3; i++ {
		childKsuid := mksuid.New().String()
		childProperties := fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId","$value":"vpc-123"}}`, parentKsuid)
		childResource := &pkgmodel.Resource{
			Ksuid:      childKsuid,
			NativeID:   fmt.Sprintf("subnet-native-%d", i), // unique native ID required
			Stack:      "test-stack",
			Label:      fmt.Sprintf("subnet-%d", i),
			Type:       "AWS::EC2::Subnet",
			Target:     "test-target",
			Properties: json.RawMessage(childProperties),
		}
		_, err = ds.StoreResource(childResource, "cmd-1")
		assert.NoError(t, err)
	}

	// Find resources depending on the parent
	dependents, err := ds.FindResourcesDependingOn(parentKsuid)
	assert.NoError(t, err)
	assert.Len(t, dependents, 3, "Should find all three dependent subnets")
}

func TestDatastore_FindResourcesDependingOn_NoRefs(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create target
	target := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Create a resource with no dependents
	parentKsuid := mksuid.New().String()
	parentResource := &pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "standalone-bucket-native",
		Stack:      "test-stack",
		Label:      "standalone-bucket",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
	}
	_, err = ds.StoreResource(parentResource, "cmd-1")
	assert.NoError(t, err)

	// Find resources depending on the parent (should be empty)
	dependents, err := ds.FindResourcesDependingOn(parentKsuid)
	assert.NoError(t, err)
	assert.Empty(t, dependents, "Should find no dependent resources")
}

func TestDatastore_FindResourcesDependingOn_DeletedResourcesExcluded(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	// Create target
	target := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Create the parent resource
	parentKsuid := mksuid.New().String()
	parentResource := &pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "deleted-parent-bucket-native",
		Stack:      "test-stack",
		Label:      "parent-bucket",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
	}
	_, err = ds.StoreResource(parentResource, "cmd-1")
	assert.NoError(t, err)

	// Create a child resource that references the parent
	childKsuid := mksuid.New().String()
	childProperties := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:s3:::my-bucket"}}`, parentKsuid)
	childResource := &pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "deleted-child-role-native",
		Stack:      "test-stack",
		Label:      "child-role",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(childProperties),
	}
	_, err = ds.StoreResource(childResource, "cmd-1")
	assert.NoError(t, err)

	// Delete the child resource
	_, err = ds.DeleteResource(childResource, "cmd-2")
	assert.NoError(t, err)

	// Find resources depending on the parent - should be empty since child was deleted
	dependents, err := ds.FindResourcesDependingOn(parentKsuid)
	assert.NoError(t, err)
	assert.Empty(t, dependents, "Should not find deleted resources as dependents")
}

// TestDatastore_StoreResource_AfterDeleteWithSameNativeID verifies that when a resource is deleted
// and then recreated with the same NativeID but a different KSUID (as happens during destroy → re-apply),
// the new resource is stored under the new KSUID, not the old one.
//
// This is a regression test for a bug where storeResource's NativeID lookup did not filter out deleted
// resources, causing the new resource to inherit the old (deleted) resource's KSUID. The system would
// then fail with "resource not found" when trying to load by the new KSUID.
func TestDatastore_StoreResource_AfterDeleteWithSameNativeID(t *testing.T) {
	ds, err := prepareDatastore()
	if err != nil {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
	defer cleanupDatastore(ds)

	nativeID := "/subscriptions/test-sub/resourceGroups/test-rg"
	resourceType := "Azure::Resources::ResourceGroup"

	target := &pkgmodel.Target{
		Label:     "target-1",
		Namespace: "AZURE",
		Config:    json.RawMessage(`{}`),
	}
	_, err = ds.CreateTarget(target)
	assert.NoError(t, err)

	// Step 1: Create the resource with KSUID-A
	ksuidA := util.NewID()
	resourceA := &pkgmodel.Resource{
		Ksuid:      ksuidA,
		NativeID:   nativeID,
		Stack:      "test-stack",
		Type:       resourceType,
		Label:      "rg",
		Target:     "target-1",
		Managed:    true,
		Properties: json.RawMessage(`{"name": "test-rg", "location": "eastus"}`),
	}
	_, err = ds.StoreResource(resourceA, "cmd-create-1")
	assert.NoError(t, err)

	// Verify the resource is stored under KSUID-A
	loaded, err := ds.LoadResourceById(ksuidA)
	assert.NoError(t, err)
	assert.NotNil(t, loaded, "resource should exist under KSUID-A after create")

	// Step 2: Delete the resource (simulating destroy)
	_, err = ds.DeleteResource(resourceA, "cmd-delete")
	assert.NoError(t, err)

	// Step 3: Recreate with the SAME NativeID but a NEW KSUID-B (simulating re-apply)
	time.Sleep(1100 * time.Millisecond) // KSUID has 1-second precision
	ksuidB := util.NewID()
	resourceB := &pkgmodel.Resource{
		Ksuid:      ksuidB,
		NativeID:   nativeID,
		Stack:      "test-stack",
		Type:       resourceType,
		Label:      "rg",
		Target:     "target-1",
		Managed:    true,
		Properties: json.RawMessage(`{"name": "test-rg", "location": "eastus"}`),
	}
	versionB, err := ds.StoreResource(resourceB, "cmd-create-2")
	assert.NoError(t, err)

	// Step 4: Verify the return value of StoreResource contains the NEW KSUID-B, not the old KSUID-A.
	assert.True(t, strings.HasPrefix(versionB, ksuidB+"_"),
		"StoreResource version should start with KSUID-B (%s), got: %s", ksuidB, versionB)
	assert.False(t, strings.HasPrefix(versionB, ksuidA+"_"),
		"StoreResource version must NOT start with old KSUID-A (%s), got: %s", ksuidA, versionB)

	// Step 5: Verify the new resource is stored under KSUID-B, not the old KSUID-A.
	loaded, err = ds.LoadResourceById(ksuidB)
	assert.NoError(t, err)
	assert.NotNil(t, loaded, "resource should be loadable under KSUID-B after recreate")
	assert.Equal(t, ksuidB, loaded.Ksuid, "resource should have KSUID-B, not the old KSUID-A")
	assert.Equal(t, nativeID, loaded.NativeID)
}
