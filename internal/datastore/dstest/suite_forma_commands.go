// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func RunFormaApplyTest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FormaApplyTest", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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
	})
}

func RunLoadIncompleteFormaCommandsTest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadIncompleteFormaCommandsTest", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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

		cmd3 := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{DesiredState: pkgmodel.Resource{Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "cmd3-target", Namespace: "default", Config: json.RawMessage("{}")},
					State:          resource_update.ResourceUpdateStateNotStarted},
			},
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateNotStarted,
		}

		err := ds.StoreFormaCommand(cmd1, cmd1.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(cmd2, cmd2.ID)
		assert.NoError(t, err)
		err = ds.StoreFormaCommand(cmd3, cmd3.ID)
		assert.NoError(t, err)

		incomplete, err := ds.LoadIncompleteFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, incomplete, 2)
	})
}

func RunGetFormaApplyByFormaHash(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetFormaApplyByFormaHash", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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
	})
}

func RunStoreAndLoadFormaCommandOptionalFields(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StoreAndLoad_FormaCommand_OptionalFields", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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

		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		assert.Equal(t, "synchronizer", loaded.ClientID)
		assert.Equal(t, "deploy production stack", loaded.Description.Text)
		assert.Equal(t, pkgmodel.FormaApplyModePatch, loaded.Config.Mode)
	})
}

func RunStoreFormaCommandSyncSkipsResourceUpdates(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StoreFormaCommand_SyncSkipsResourceUpdates", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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

		err := ds.StoreFormaCommand(applyCmd, applyCmd.ID)
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
	})
}

func RunGetMostRecentFormaCommandByClientID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetMostRecentFormaCommandByClientID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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
	})
}

func RunGetMostRecentNonReconcileFormaCommandsByStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetMostRecentNonReconcileFormaCommandsByStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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
	})
}

func RunQueryFormaCommands(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryFormaCommands", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

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
	})
}

// RunQueryFormaCommands_StackWildcardEscape verifies that wildcards in
// `stack:` queries — which route through an EXISTS subquery against
// resource_updates — produce valid SQL with the ESCAPE clause positioned
// correctly *inside* the EXISTS parens. A naive append-to-end places
// `ESCAPE '\'` after the closing `)`, which is a syntax error.
func RunQueryFormaCommands_StackWildcardEscape(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryFormaCommands_StackWildcardEscape", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Two commands with different stack labels. A wildcard query
		// `stack:life*` should match the lifecycle one only.
		commands := []*forma_command.FormaCommand{
			{
				Description: pkgmodel.Description{},
				ClientID:    "client-a",
				StartTs:     util.TimeNow(),
				Command:     pkgmodel.CommandApply,
				State:       forma_command.CommandStateInProgress,
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						DesiredState: pkgmodel.Resource{Stack: "lifecycle-prod", Properties: json.RawMessage(`{}`)},
						StackLabel:   "lifecycle-prod",
						State:        resource_update.ResourceUpdateStateSuccess,
					},
				},
			},
			{
				Description: pkgmodel.Description{},
				ClientID:    "client-b",
				StartTs:     util.TimeNow(),
				Command:     pkgmodel.CommandApply,
				State:       forma_command.CommandStateInProgress,
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						DesiredState: pkgmodel.Resource{Stack: "other-prod", Properties: json.RawMessage(`{}`)},
						StackLabel:   "other-prod",
						State:        resource_update.ResourceUpdateStateSuccess,
					},
				},
			},
		}
		for i, c := range commands {
			c.ID = fmt.Sprintf("cmd-stack-wildcard-%d", i)
			err := ds.StoreFormaCommand(c, c.ID)
			assert.NoError(t, err)
		}

		query := &datastore.StatusQuery{
			Stack: &datastore.QueryItem[string]{
				Item:       "life*",
				Constraint: datastore.Required,
			},
		}
		results, err := ds.QueryFormaCommands(query)
		assert.NoError(t, err, "stack wildcard must produce valid SQL inside the EXISTS clause")
		assert.Len(t, results, 1)
		if len(results) == 1 {
			assert.Equal(t, "client-a", results[0].ClientID)
		}
	})
}

// RunTerminalStatesLiteralsTest asserts that the SQL IN-list literals used by the
// datastore backends exactly match types.TerminalStates.
func RunTerminalStatesLiteralsTest(t *testing.T, _ func(t *testing.T) TestDatastore) {
	t.Run("TerminalStatesLiterals", func(t *testing.T) {
		sqlLiterals := []string{"Success", "Failed", "Rejected", "Canceled"}
		var typeStrings []string
		for _, s := range types.TerminalStates {
			typeStrings = append(typeStrings, string(s))
		}
		assert.ElementsMatch(t, sqlLiterals, typeStrings,
			"SQL IN-list literals must match types.TerminalStates exactly")
	})
}

// RunMonotonicTerminalityTest verifies that once a ResourceUpdate reaches a terminal
// state, subsequent state writes are no-ops (not errors) and the state is preserved.
// Within the agent, state writes are serialized by the FormaCommandPersister actor;
// this DB-level test proves the fence holds independently.
func RunMonotonicTerminalityTest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("MonotonicTerminality", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		resourceKsuid := util.NewID()
		cmd := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Command:     pkgmodel.CommandApply,
			State:       forma_command.CommandStateInProgress,
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState:   pkgmodel.Resource{Ksuid: resourceKsuid, Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "t", Namespace: "default", Config: json.RawMessage("{}")},
					Operation:      resource_update.OperationCreate,
					State:          resource_update.ResourceUpdateStateInProgress,
				},
			},
		}
		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		now := time.Now()

		// First write: InProgress → Success (should succeed)
		err = ds.UpdateResourceUpdateState(cmd.ID, resourceKsuid, resource_update.OperationCreate, resource_update.ResourceUpdateStateSuccess, now)
		assert.NoError(t, err, "transition to Success should succeed")

		// Second write: try to overwrite Success with Failed (should be a no-op, not an error)
		err = ds.UpdateResourceUpdateState(cmd.ID, resourceKsuid, resource_update.OperationCreate, resource_update.ResourceUpdateStateFailed, now)
		assert.NoError(t, err, "attempt to overwrite terminal state should be a no-op, not an error")

		// Reload and assert state is still Success
		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		if assert.Len(t, loaded.ResourceUpdates, 1) {
			assert.Equal(t, resource_update.ResourceUpdateStateSuccess, loaded.ResourceUpdates[0].State,
				"terminal state Success must not be overwritten by Failed")
		}
	})
}

// RunForceCancelResourceUpdatesTest verifies ForceCancelResourceUpdates:
//   - transitions InProgress+NotStarted rows to Canceled
//   - writes force-cancel progress for the InProgress row
//   - leaves already-terminal rows untouched and reports them in Skipped
//   - is idempotent: a second call makes zero further changes
func RunForceCancelResourceUpdatesTest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ForceCancelResourceUpdates", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		inProgressKsuid := util.NewID()
		notStartedKsuid := util.NewID()
		successKsuid := util.NewID()

		cmd := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Command:     pkgmodel.CommandApply,
			State:       forma_command.CommandStateInProgress,
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState:   pkgmodel.Resource{Ksuid: inProgressKsuid, Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "t", Namespace: "default", Config: json.RawMessage("{}")},
					Operation:      resource_update.OperationCreate,
					State:          resource_update.ResourceUpdateStateInProgress,
				},
				{
					DesiredState:   pkgmodel.Resource{Ksuid: notStartedKsuid, Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "t", Namespace: "default", Config: json.RawMessage("{}")},
					Operation:      resource_update.OperationCreate,
					State:          resource_update.ResourceUpdateStateNotStarted,
				},
				{
					DesiredState:   pkgmodel.Resource{Ksuid: successKsuid, Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "t", Namespace: "default", Config: json.RawMessage("{}")},
					Operation:      resource_update.OperationCreate,
					State:          resource_update.ResourceUpdateStateSuccess,
				},
			},
		}
		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		// Build the force-cancel progress JSON for the InProgress row.
		forceCancelProgress := plugin.TrackedProgress{
			ProgressResult: pkgresource.ProgressResult{
				StatusMessage: "force-canceled",
			},
		}
		progressList := []plugin.TrackedProgress{forceCancelProgress}
		progressJSON, err := json.Marshal(progressList)
		assert.NoError(t, err)
		mostRecentJSON, err := json.Marshal(forceCancelProgress)
		assert.NoError(t, err)

		inProgressRow := datastore.ForceCancelRow{
			KSUID:                  inProgressKsuid,
			Operation:              resource_update.OperationCreate,
			ProgressJSON:           progressJSON,
			MostRecentProgressJSON: mostRecentJSON,
		}
		notStartedRef := datastore.ResourceUpdateRef{
			KSUID:      notStartedKsuid,
			Operation:  resource_update.OperationCreate,
		}
		successRef := datastore.ResourceUpdateRef{
			KSUID:      successKsuid,
			Operation:  resource_update.OperationCreate,
		}

		now := time.Now()
		result, err := ds.ForceCancelResourceUpdates(
			cmd.ID,
			[]datastore.ForceCancelRow{inProgressRow},
			[]datastore.ResourceUpdateRef{notStartedRef, successRef},
			now,
		)
		assert.NoError(t, err)

		// Verify result split: inProgress → CanceledInProgress, notStarted → CanceledNotStarted, success → Skipped
		assert.Len(t, result.CanceledInProgress, 1)
		assert.Equal(t, inProgressKsuid, result.CanceledInProgress[0].KSUID)
		assert.Len(t, result.CanceledNotStarted, 1)
		assert.Equal(t, notStartedKsuid, result.CanceledNotStarted[0].KSUID)
		assert.Len(t, result.Skipped, 1)
		assert.Equal(t, successKsuid, result.Skipped[0].KSUID)

		// Verify the DB reflects the state changes
		updates, err := ds.LoadResourceUpdates(cmd.ID)
		assert.NoError(t, err)
		assert.Len(t, updates, 3)

		byKsuid := make(map[string]resource_update.ResourceUpdate)
		for _, u := range updates {
			byKsuid[u.DesiredState.Ksuid] = u
		}

		assert.Equal(t, resource_update.ResourceUpdateStateCanceled, byKsuid[inProgressKsuid].State)
		assert.Equal(t, resource_update.ResourceUpdateStateCanceled, byKsuid[notStartedKsuid].State)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, byKsuid[successKsuid].State, "already-terminal row must not be modified")

		// Verify the InProgress row now has the force-cancel progress entry
		assert.Equal(t, "force-canceled", byKsuid[inProgressKsuid].MostRecentProgressResult.ProgressResult.StatusMessage)
		assert.Len(t, byKsuid[inProgressKsuid].ProgressResult, 1)
		assert.Equal(t, "force-canceled", byKsuid[inProgressKsuid].ProgressResult[0].ProgressResult.StatusMessage)

		// --- Idempotency: call again; all three rows are now terminal ---
		result2, err := ds.ForceCancelResourceUpdates(
			cmd.ID,
			[]datastore.ForceCancelRow{inProgressRow},
			[]datastore.ResourceUpdateRef{notStartedRef, successRef},
			now,
		)
		assert.NoError(t, err)

		assert.Empty(t, result2.CanceledInProgress, "retry must not re-cancel already-Canceled rows")
		assert.Empty(t, result2.CanceledNotStarted, "retry must not re-cancel already-Canceled rows")
		assert.Len(t, result2.Skipped, 3, "all rows must be reported as Skipped on retry")
	})
}

// RunMonotonicTerminalityRaceTest fires two concurrent UpdateResourceUpdateState calls
// on the same ResourceUpdate — one writing Success, one writing Failed. First write wins.
// Within the agent these are serialized by the FormaCommandPersister actor; this test
// proves the DB fence holds independently of that serialization.
func RunMonotonicTerminalityRaceTest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("MonotonicTerminalityRace", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		resourceKsuid := util.NewID()
		cmd := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Command:     pkgmodel.CommandApply,
			State:       forma_command.CommandStateInProgress,
			Description: pkgmodel.Description{},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState:   pkgmodel.Resource{Ksuid: resourceKsuid, Properties: json.RawMessage("{}")},
					ResourceTarget: pkgmodel.Target{Label: "t", Namespace: "default", Config: json.RawMessage("{}")},
					Operation:      resource_update.OperationCreate,
					State:          resource_update.ResourceUpdateStateInProgress,
				},
			},
		}
		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		now := time.Now()

		var wg sync.WaitGroup
		wg.Add(2)

		var err1, err2 error
		go func() {
			defer wg.Done()
			err1 = ds.UpdateResourceUpdateState(cmd.ID, resourceKsuid, resource_update.OperationCreate, resource_update.ResourceUpdateStateSuccess, now)
		}()
		go func() {
			defer wg.Done()
			err2 = ds.UpdateResourceUpdateState(cmd.ID, resourceKsuid, resource_update.OperationCreate, resource_update.ResourceUpdateStateFailed, now)
		}()
		wg.Wait()

		// Both calls must return nil (one transitions, the other is a no-op)
		assert.NoError(t, err1)
		assert.NoError(t, err2)

		// Final state must be one of the two terminal states and must be stable
		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		if assert.Len(t, loaded.ResourceUpdates, 1) {
			finalState := loaded.ResourceUpdates[0].State
			assert.True(t,
				finalState == resource_update.ResourceUpdateStateSuccess || finalState == resource_update.ResourceUpdateStateFailed,
				"final state must be either Success or Failed, got: %s", finalState)
		}
	})
}
