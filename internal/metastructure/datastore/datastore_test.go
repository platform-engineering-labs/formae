// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit || integration

package datastore

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

var dbType string

func prepareDatastore() (Datastore, error) {
	switch dbType {
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

		datastore, err := NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
		if err != nil {
			return nil, fmt.Errorf("failed to setup Postgres datastore: %w", err)
		}

		return datastore, nil
	default:
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.SqliteDatastore,
			Sqlite: pkgmodel.SqliteConfig{
				FilePath: ":memory:",
			},
		}

		datastore, err := NewDatastoreSQLite(context.Background(), cfg, "test")
		if err != nil {
			return nil, fmt.Errorf("failed to setup SQLite datastore: %w", err)
		}

		return datastore, nil
	}
}

func TestMain(m *testing.M) {
	flag.StringVar(&dbType, "dbType", pkgmodel.SqliteDatastore, fmt.Sprintf("Specify the database type (e.g., %s, %s)", pkgmodel.SqliteDatastore, pkgmodel.PostgresDatastore))
	flag.Parse()

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
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		app1 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{Resource: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "target1", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateSuccess},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateSuccess,
		}

		app2 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ResourceTarget:           pkgmodel.Target{Label: "target2", Namespace: "default", Config: json.RawMessage("{}")},
					MostRecentProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")},
					Resource:                 pkgmodel.Resource{Properties: json.RawMessage("{}")},
					State:                    resource_update.ResourceUpdateStateRejected},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateFailed,
		}

		app3 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ResourceTarget:           pkgmodel.Target{Label: "target3", Namespace: "default", Config: json.RawMessage("{}")},
					MostRecentProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")},
					Resource:                 pkgmodel.Resource{Properties: json.RawMessage("{}")},
					State:                    resource_update.ResourceUpdateStateFailed},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateFailed,
		}

		app4 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ResourceTarget:           pkgmodel.Target{Label: "target4", Namespace: "default", Config: json.RawMessage("{}")},
					MostRecentProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")},
					Resource: pkgmodel.Resource{
						Properties:         json.RawMessage("{}"),
						ReadOnlyProperties: json.RawMessage("{}"),
					},
					State: resource_update.ResourceUpdateStateInProgress},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateInProgress,
		}
		err := datastore.StoreFormaCommand(app1, app1.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(app2, app2.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(app3, app3.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(app4, app4.ID)
		assert.NoError(t, err)

		commands, err := datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, commands, 4)

		incomplete, err := datastore.LoadIncompleteFormaCommands()
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
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		cmd1 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{Resource: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "cmd1-target", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateInProgress},
			},
			Command: pkgmodel.CommandSync,
			State:   forma_command.CommandStateInProgress,
		}

		cmd2 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{Resource: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "cmd2-target", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateInProgress},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateInProgress,
		}

		err := datastore.StoreFormaCommand(cmd1, cmd1.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(cmd2, cmd2.ID)
		assert.NoError(t, err)

		incomplete, err := datastore.LoadIncompleteFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, incomplete, 1)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_GetFormaApplyByFormaHash(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		app1 := &forma_command.FormaCommand{
			ID:    util.NewID(),
			Forma: pkgmodel.Forma{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					ExistingResource: pkgmodel.Resource{
						Properties: json.RawMessage("null"),
					},
					MostRecentProgressResult: pkgresource.ProgressResult{ResourceProperties: json.RawMessage("{}")},
					ResourceTarget:           pkgmodel.Target{Label: "hash-target", Namespace: "default", Config: json.RawMessage("{}")},
					Resource:                 pkgmodel.Resource{Properties: json.RawMessage("{}")},
					State:                    resource_update.ResourceUpdateStateSuccess,
					MetaData:                 json.RawMessage("null"),
				},
			},
		}

		err := datastore.StoreFormaCommand(app1, app1.ID)
		assert.NoError(t, err)

		retrieved, err := datastore.GetFormaCommandByCommandID(app1.ID)
		assert.NoError(t, err)
		assert.Equal(t, app1, retrieved)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_GetMostRecentFormaCommandByClientID(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		clientID := "test"
		olderTime, _ := time.Parse(time.RFC3339, "2023-01-01T10:00:00Z")
		olderCommand := &forma_command.FormaCommand{
			Forma:    pkgmodel.Forma{},
			ClientID: clientID,
			StartTs:  olderTime,
			Command:  pkgmodel.CommandApply,
		}

		newerTime, _ := time.Parse(time.RFC3339, "2023-01-02T10:00:00Z")
		newerCommand := &forma_command.FormaCommand{
			Forma:    pkgmodel.Forma{},
			ClientID: clientID,
			StartTs:  newerTime,
			Command:  pkgmodel.CommandApply,
		}

		// Store both commands
		err := datastore.StoreFormaCommand(olderCommand, olderCommand.ID)
		assert.NoError(t, err)

		err = datastore.StoreFormaCommand(newerCommand, newerCommand.ID)
		assert.NoError(t, err)

		// Retrieve the most recent command
		retrieved, err := datastore.GetMostRecentFormaCommandByClientID(clientID)
		assert.NoError(t, err)

		// The most recent command should be the newer one
		assert.Equal(t, newerCommand.StartTs, retrieved.StartTs)

		// Test with non-existent client ID
		_, err = datastore.GetMostRecentFormaCommandByClientID("non-existent-client")
		assert.Error(t, err)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_GetMostRecentNonReconcileFormaCommandsByStack(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		target := &pkgmodel.Target{
			Label:     "default-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := datastore.CreateTarget(target)
		assert.NoError(t, err)

		reconcileStack1 := &forma_command.FormaCommand{
			ID:      "reconcile-stack1-id",
			Forma:   pkgmodel.Forma{},
			Command: pkgmodel.CommandApply,
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
			ID:      "sync-stack1-id",
			Forma:   pkgmodel.Forma{},
			Command: pkgmodel.CommandSync,
			Config:  config.FormaCommandConfig{},
			StartTs: util.TimeNow().Add(-3 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-1",
				},
			},
		}
		reconcileStack2 := &forma_command.FormaCommand{
			ID:      "reconcile-stack2-id",
			Forma:   pkgmodel.Forma{},
			Command: pkgmodel.CommandApply,
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
			ID:      "sync-stack2-id",
			Forma:   pkgmodel.Forma{},
			Command: pkgmodel.CommandSync,
			Config:  config.FormaCommandConfig{},
			StartTs: util.TimeNow().Add(-3 * time.Minute),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					StackLabel: "stack-2",
				},
			},
		}
		patchStack2 := &forma_command.FormaCommand{
			ID:      "patch-stack2-id",
			Forma:   pkgmodel.Forma{},
			Command: pkgmodel.CommandApply,
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

		_, err = datastore.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-1",
			Stack:    "stack-1",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-1",
			Target:   "default-target",
		}, reconcileStack1.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(reconcileStack1, reconcileStack1.ID)
		assert.NoError(t, err)

		_, err = datastore.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-2",
			Stack:    "stack-1",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-1b",
			Target:   "default-target",
		}, syncStack1.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(syncStack1, syncStack1.ID)
		assert.NoError(t, err)

		_, err = datastore.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-3",
			Stack:    "stack-2",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-2",
			Target:   "default-target",
		}, reconcileStack2.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(reconcileStack2, reconcileStack2.ID)
		assert.NoError(t, err)

		_, err = datastore.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-4",
			Stack:    "stack-2",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-2b",
			Target:   "default-target",
		}, syncStack2.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(syncStack2, syncStack2.ID)
		assert.NoError(t, err)

		_, err = datastore.StoreResource(&pkgmodel.Resource{
			NativeID: "resource-5",
			Stack:    "stack-2",
			Type:     "AWS::S3::Bucket",
			Label:    "test-bucket-2c",
			Target:   "default-target",
		}, patchStack2.ID)
		assert.NoError(t, err)
		err = datastore.StoreFormaCommand(patchStack2, patchStack2.ID)
		assert.NoError(t, err)

		modifications, err := datastore.GetResourceModificationsSinceLastReconcile("stack-2")
		assert.NoError(t, err)

		assert.Len(t, modifications, 2)
		assert.Equal(t, "stack-2", modifications[0].Stack)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_QueryFormaCommands(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		for i := range 20 {
			command := &forma_command.FormaCommand{
				Forma:    pkgmodel.Forma{},
				ClientID: fmt.Sprintf("client-%d", i%5),
				StartTs:  util.TimeNow().Add(time.Duration(-i) * time.Hour),
				Command: func() pkgmodel.Command {
					if i%2 == 0 {
						return pkgmodel.CommandApply
					}
					return pkgmodel.CommandDestroy
				}(),
				State: forma_command.CommandStateInProgress,
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Resource: pkgmodel.Resource{
							Properties: json.RawMessage(fmt.Sprintf(`{"key": "value-%d"}`, i)),
							Stack:      fmt.Sprintf("stack-%d", i%2),
						},
						State: resource_update.ResourceUpdateStateSuccess,
					},
					{
						Resource: pkgmodel.Resource{
							Properties: json.RawMessage(fmt.Sprintf(`{"key": "value-%d"}`, i)),
							Stack:      fmt.Sprintf("stack-%d", i%2),
						},
						State: resource_update.ResourceUpdateStateInProgress,
					},
				},
			}
			command.ID = fmt.Sprintf("command-%d", i)
			err := datastore.StoreFormaCommand(command, command.ID)
			assert.NoError(t, err)
		}

		query := &StatusQuery{
			ClientID: &QueryItem[string]{
				Item:       "client-1",
				Constraint: Required,
			},
		}
		results, err := datastore.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "client-1", result.ClientID)
		}

		query = &StatusQuery{
			CommandID: &QueryItem[string]{
				Item:       results[0].ID,
				Constraint: Required,
			},
		}
		results, err = datastore.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, query.CommandID.Item, results[0].ID)

		query = &StatusQuery{
			Command: &QueryItem[string]{
				Item:       string(pkgmodel.CommandApply),
				Constraint: Required,
			},
		}
		results, err = datastore.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.Len(t, results, 10)
		for _, result := range results {
			assert.Equal(t, pkgmodel.CommandApply, result.Command)
		}

		query = &StatusQuery{
			Status: &QueryItem[string]{
				Item:       string(forma_command.CommandStateSuccess),
				Constraint: Required,
			},
		}
		results, err = datastore.QueryFormaCommands(query)
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

		query = &StatusQuery{
			ClientID: &QueryItem[string]{
				Item:       "client-2",
				Constraint: Required,
			},
			Command: &QueryItem[string]{
				Item:       string(pkgmodel.CommandApply),
				Constraint: Required,
			},
			Status: &QueryItem[string]{
				Item:       "inprogress",
				Constraint: Required,
			},
		}
		results, err = datastore.QueryFormaCommands(query)
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

		query = &StatusQuery{
			Stack: &QueryItem[string]{
				Item:       "stack-1",
				Constraint: Required,
			},
		}
		results, err = datastore.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)

		for _, result := range results {
			found := false
			for _, rc := range result.ResourceUpdates {
				if string(rc.Resource.Stack) == "stack-1" {
					found = true
					break
				}
			}
			assert.True(t, found)
		}

		query = &StatusQuery{
			Stack: &QueryItem[string]{
				Item:       "stack-1",
				Constraint: Excluded,
			},
		}
		results, err = datastore.QueryFormaCommands(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			found := false
			for _, rc := range result.ResourceUpdates {
				if string(rc.Resource.Stack) == "stack-1" {
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
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		target := &pkgmodel.Target{
			Label:     "target-1",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := datastore.CreateTarget(target)
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

		_, err = datastore.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		query := &ResourceQuery{
			NativeID: &QueryItem[string]{
				Item:       "native-1",
				Constraint: Required,
			},
		}
		results, err := datastore.QueryResources(query)
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
		if datastore, err := prepareDatastore(); err == nil {
			defer datastore.CleanUp()
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
			originalVersion, err := datastore.StoreResource(originalResource, "test-command-1")
			assert.NoError(t, err)

			newVersion, err := datastore.StoreResource(&pkgmodel.Resource{
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

			newFromDb, err := datastore.LoadResourceById(originalResource.Ksuid)
			assert.NoError(t, err)

			assert.NotEqual(t, originalVersion, newVersion)
			assert.JSONEq(t, `{"key": "new-value"}`, string(newFromDb.Properties))
		} else {
			t.Fatalf("Failed to prepare datastore: %v\n", err)
		}
	})

	t.Run("Should not create a new version if the read-only properties changed", func(t *testing.T) {
		if datastore, err := prepareDatastore(); err == nil {
			defer datastore.CleanUp()
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
			originalVersion, err := datastore.StoreResource(originalResource, "test-command-1")
			assert.NoError(t, err)

			newVersion, err := datastore.StoreResource(&pkgmodel.Resource{
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

			newFromDb, err := datastore.LoadResourceById(originalResource.Ksuid)
			assert.NoError(t, err)

			assert.Equal(t, originalVersion, newVersion)
			assert.JSONEq(t, `{"ro-key": "new-ro-value"}`, string(newFromDb.ReadOnlyProperties))
		} else {
			t.Fatalf("Failed to prepare datastore: %v\n", err)
		}
	})
}

func TestDatastore_DeleteResource(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		resource := &pkgmodel.Resource{
			NativeID: "native-1",
			Stack:    "stack-1",
			Type:     "type-1",
			Label:    "label-1",
			Properties: json.RawMessage(`{
			"key": "value"
			}`),
		}

		_, err := datastore.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		_, err = datastore.DeleteResource(resource, "test-command")
		assert.NoError(t, err)

		query := &ResourceQuery{
			NativeID: &QueryItem[string]{
				Item:       "native-1",
				Constraint: Required,
			},
		}
		results, err := datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.Empty(t, results)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_QueryResources(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		for i := range 7 {
			target := &pkgmodel.Target{
				Label:     fmt.Sprintf("target-%d", i),
				Namespace: "default",
				Config:    json.RawMessage(`{}`),
			}
			_, err := datastore.CreateTarget(target)
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
			_, err := datastore.StoreResource(resource, "test-command")
			assert.NoError(t, err)
		}

		query := &ResourceQuery{
			NativeID: &QueryItem[string]{
				Item:       "native-1",
				Constraint: Required,
			},
		}
		results, err := datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		for _, result := range results {
			assert.Equal(t, "native-1", result.NativeID)
		}

		query = &ResourceQuery{
			Stack: &QueryItem[string]{
				Item:       "stack-2",
				Constraint: Required,
			},
		}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "stack-2", result.Stack)
		}

		query = &ResourceQuery{
			Type: &QueryItem[string]{
				Item:       "type-3",
				Constraint: Required,
			},
		}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "type-3", result.Type)
		}

		query = &ResourceQuery{
			Label: &QueryItem[string]{
				Item:       "label-4",
				Constraint: Required,
			},
		}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "label-4", result.Label)
		}

		query = &ResourceQuery{
			Target: &QueryItem[string]{
				Item:       "target-4",
				Constraint: Required,
			},
		}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		for _, result := range results {
			assert.Equal(t, "target-4", result.Target)
		}

		query = &ResourceQuery{}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 10)

		query = &ResourceQuery{
			Managed: &QueryItem[bool]{
				Item:       true,
				Constraint: Required,
			},
		}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 5)

		query = &ResourceQuery{
			Managed: &QueryItem[bool]{
				Item:       false,
				Constraint: Required,
			},
		}
		results, err = datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.NotEmpty(t, results)
		assert.Len(t, results, 5)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// Storing the same resource twice should not create duplicates and should return the same version ID
func TestDatastore_StoreResource_SameResourceTwiceReturnsSameVersionId(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := datastore.CreateTarget(target)
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

		firstVersionId, err := datastore.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		secondVersionId, err := datastore.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		assert.Equal(t, firstVersionId, secondVersionId)

		query := &ResourceQuery{
			NativeID: &QueryItem[string]{
				Item:       "native-1",
				Constraint: Required,
			},
		}
		results, err := datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, resource.URI(), results[0].URI())
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_LoadResourceByNativeID(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

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

		_, err := datastore.StoreResource(resource, "test-command")
		assert.NoError(t, err)

		loadedResource, err := datastore.LoadResourceByNativeID("native-123")
		assert.NoError(t, err)
		assert.NotNil(t, loadedResource)
		assert.Equal(t, resource.NativeID, loadedResource.NativeID)
		assert.Equal(t, resource.Stack, loadedResource.Stack)
		assert.Equal(t, resource.Type, loadedResource.Type)

		// Negative test
		nonExistentResource, err := datastore.LoadResourceByNativeID("non-existent")
		assert.NoError(t, err)
		assert.Nil(t, nonExistentResource)

		_, err = datastore.DeleteResource(resource, "test-delete-command")
		assert.NoError(t, err)

		deletedResource, err := datastore.LoadResourceByNativeID("native-123")
		assert.NoError(t, err)
		assert.Nil(t, deletedResource, "Deleted resource should not be found")
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// Store some test resources with known triplets
func TestDatastore_BatchGetKSUIDsByTriplets(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

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
		_, err := datastore.StoreResource(resource1, "test-command-1")
		assert.NoError(t, err)
		_, err = datastore.StoreResource(resource2, "test-command-2")
		assert.NoError(t, err)
		_, err = datastore.StoreResource(resource3, "test-command-3")
		assert.NoError(t, err)

		// Get the actual KSUIDs from the stored resources by querying them back
		storedResource1, err := datastore.LoadResourceByNativeID("bucket-1")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource1)

		storedResource2, err := datastore.LoadResourceByNativeID("vpc-1")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource2)

		storedResource3, err := datastore.LoadResourceByNativeID("role-1")
		assert.NoError(t, err)
		assert.NotNil(t, storedResource3)

		// Test basic batch lookup
		triplets := []pkgmodel.TripletKey{
			{Stack: "test-stack", Label: "resource-1", Type: "AWS::S3::Bucket"},
			{Stack: "test-stack", Label: "resource-2", Type: "AWS::EC2::VPC"},
			{Stack: "other-stack", Label: "resource-3", Type: "AWS::IAM::Role"},
		}

		results, err := datastore.BatchGetKSUIDsByTriplets(triplets)
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

		mixedResults, err := datastore.BatchGetKSUIDsByTriplets(mixedTriplets)
		assert.NoError(t, err)
		assert.Len(t, mixedResults, 2) // Only 2 should be found

		// Verify existing resources are found
		assert.Equal(t, storedResource1.Ksuid, mixedResults[mixedTriplets[0]])
		assert.Equal(t, storedResource3.Ksuid, mixedResults[mixedTriplets[2]])

		// Verify non-existent resource is not in results
		_, exists := mixedResults[mixedTriplets[1]]
		assert.False(t, exists, "Non-existent triplet should not be in results")

		// Test with empty input
		emptyResults, err := datastore.BatchGetKSUIDsByTriplets([]pkgmodel.TripletKey{})
		assert.NoError(t, err)
		assert.Empty(t, emptyResults)

		// Test that deleted resources' KSUIDs are still available for reuse (KSUID stability)
		_, err = datastore.DeleteResource(storedResource1, "delete-command")
		assert.NoError(t, err)

		afterDeleteResults, err := datastore.BatchGetKSUIDsByTriplets(triplets)
		assert.NoError(t, err)
		assert.Len(t, afterDeleteResults, 3)

		// Verify deleted resource's KSUID is still available for reuse
		ksuid, exists := afterDeleteResults[triplets[0]]
		assert.True(t, exists, "Deleted resource's KSUID should still be available for reuse")
		assert.Equal(t, storedResource1.Ksuid, ksuid, "Should return the same KSUID for stability")

		// Verify other resources are still there
		assert.Equal(t, storedResource2.Ksuid, afterDeleteResults[triplets[1]])
		assert.Equal(t, storedResource3.Ksuid, afterDeleteResults[triplets[2]])
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

// Create and store initial resource (simulating initial create)
func TestDatastore_BatchGetKSUIDsByTriplets_PatchScenario(t *testing.T) {
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

		initialResource := &pkgmodel.Resource{
			Stack:      "test-stack",
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			NativeID:   "bucket-123",
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Managed:    true,
		}

		// Store the initial resource
		_, err := datastore.StoreResource(initialResource, "initial-command")
		assert.NoError(t, err)

		// Get the stored resource to see what KSUID it got
		storedResource, err := datastore.LoadResourceByNativeID("bucket-123")
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

		results, err := datastore.BatchGetKSUIDsByTriplets([]pkgmodel.TripletKey{triplet})
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
	if datastore, err := prepareDatastore(); err == nil {
		defer datastore.CleanUp()

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

		_, err := datastore.StoreResource(bucket, "test-command-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, bucket.Ksuid, "Bucket should have a KSUID assigned")

		_, err = datastore.StoreResource(bucketPolicy, "test-command-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, bucketPolicy.Ksuid, "BucketPolicy should have a KSUID assigned")

		assert.NotEqual(t, bucket.Ksuid, bucketPolicy.Ksuid,
			"Bucket and BucketPolicy should have different KSUIDs even though they share the same NativeId")

		loadedBucket, err := datastore.LoadResource(bucket.URI())
		assert.NoError(t, err)
		assert.NotNil(t, loadedBucket)
		assert.Equal(t, "AWS::S3::Bucket", loadedBucket.Type)
		assert.Equal(t, "WebsiteBucket", loadedBucket.Label)
		assert.Equal(t, bucket.Ksuid, loadedBucket.Ksuid)

		loadedBucketPolicy, err := datastore.LoadResource(bucketPolicy.URI())
		assert.NoError(t, err)
		assert.NotNil(t, loadedBucketPolicy)
		assert.Equal(t, "AWS::S3::BucketPolicy", loadedBucketPolicy.Type)
		assert.Equal(t, "WebsiteBucketPolicy", loadedBucketPolicy.Label)
		assert.Equal(t, bucketPolicy.Ksuid, loadedBucketPolicy.Ksuid)

		bucket.Properties = json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"WebsiteConfiguration": {"IndexDocument": "index.html", "ErrorDocument": "error.html"}
		}`)
		_, err = datastore.StoreResource(bucket, "test-command-2")
		assert.NoError(t, err)

		reloadedBucketPolicy, err := datastore.LoadResource(bucketPolicy.URI())
		assert.NoError(t, err)
		assert.NotNil(t, reloadedBucketPolicy)
		assert.JSONEq(t, string(bucketPolicy.Properties), string(reloadedBucketPolicy.Properties),
			"BucketPolicy should remain unchanged after Bucket update")

		_, err = datastore.DeleteResource(bucketPolicy, "test-delete-command")
		assert.NoError(t, err)

		query := &ResourceQuery{
			NativeID: &QueryItem[string]{
				Item:       "my-unique-bucket-name",
				Constraint: Required,
			},
		}
		results, err := datastore.QueryResources(query)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(results), "Should find only the Bucket after BucketPolicy deletion")
		assert.Equal(t, "AWS::S3::Bucket", results[0].Type, "Remaining resource should be the Bucket")

	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}
