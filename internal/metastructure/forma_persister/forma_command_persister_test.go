// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_persister

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestFormaCommandPersister_StoresNewFormaCommand(t *testing.T) {
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	operator, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	storeResult := operator.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)
	assert.True(t, storeResult.Response.(bool))

	loadResult := operator.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand, ok := loadResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok)

	assert.Equal(t, formaCommand.ID, loadedCommand.ID)
}

func TestFormaCommandPersister_RecordsResourceProgress(t *testing.T) {
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)
	assert.True(t, storeResult.Response.(bool))

	// Use the KSUID from the resource
	resourceURI := formaCommand.ResourceUpdates[0].Resource.URI()

	updateResourceProgress := messages.UpdateResourceProgress{
		CommandID:          formaCommand.ID,
		ResourceURI:        resourceURI,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow().Add(20 * time.Second),
		ResourceState:      resource_update.ResourceUpdateStateInProgress,
		Progress: resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusInProgress,
			RequestID:       "test-request-id",
			StartTs:         util.TimeNow(),
			ModifiedTs:      util.TimeNow().Add(20 * time.Second),
		},
	}
	res := formaPersister.Call(sender, updateResourceProgress)
	assert.NoError(t, res.Error)
	assert.True(t, res.Response.(bool))

	loadCommandResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadCommandResult.Error)
	loadedCommand, ok := loadCommandResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok)

	assert.Equal(t, resource_update.ResourceUpdateStateInProgress, loadedCommand.ResourceUpdates[0].State)
	assert.WithinDuration(t, updateResourceProgress.ResourceStartTs, loadedCommand.ResourceUpdates[0].StartTs, 1*time.Second)
	assert.WithinDuration(t, updateResourceProgress.ResourceModifiedTs, loadedCommand.ResourceUpdates[0].ModifiedTs, 1*time.Second)
	assert.Len(t, loadedCommand.ResourceUpdates[0].ProgressResult, 1)

	assert.Equal(t, forma_command.CommandStateInProgress, loadedCommand.State)
	assert.WithinDuration(t, formaCommand.StartTs, loadedCommand.StartTs, 1*time.Second)
	assert.WithinDuration(t, updateResourceProgress.ResourceModifiedTs, loadedCommand.ModifiedTs, 1*time.Second)

	secondProgressUpdate := messages.UpdateResourceProgress{
		CommandID:          formaCommand.ID,
		ResourceURI:        resourceURI,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow().Add(20 * time.Second),
		ResourceState:      resource_update.ResourceUpdateStateSuccess,
		Progress: resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       "test-request-id",
			StartTs:         util.TimeNow(),
			ModifiedTs:      util.TimeNow().Add(20 * time.Second),
		},
	}
	secondRes := formaPersister.Call(sender, secondProgressUpdate)
	assert.NoError(t, secondRes.Error)
	assert.True(t, secondRes.Response.(bool))

	secondLoadCommandResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, secondLoadCommandResult.Error)
	secondLoadedCommand, ok := secondLoadCommandResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok)

	assert.Equal(t, forma_command.CommandStateSuccess, secondLoadedCommand.State)
	assert.Len(t, secondLoadedCommand.ResourceUpdates[0].ProgressResult, 1)
	assert.Equal(t, resource.OperationStatusSuccess, secondLoadedCommand.ResourceUpdates[0].ProgressResult[0].OperationStatus)
}

func TestFormaCommandPersister_BulkUpdateResourceState(t *testing.T) {
	var (
		resource0Ksuid = pkgmodel.NewFormaeURI(util.NewID(), "")
		resource1Ksuid = pkgmodel.NewFormaeURI(util.NewID(), "")
		resource2Ksuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	formaCommand := &forma_command.FormaCommand{
		ID: "test-bulk-update",
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:      "resource0",
					Type:       "AWS::EC2::VPC",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{"cidr":"10.0.0.0/16"}`),
					Ksuid:      resource0Ksuid.KSUID(),
				},
				State: resource_update.ResourceUpdateStateNotStarted,
			},
			{
				Resource: pkgmodel.Resource{
					Label:      "resource1",
					Type:       "AWS::EC2::Subnet",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{"cidr":"10.1.0.0/20"}`),
					Ksuid:      resource1Ksuid.KSUID(),
				},
				State: resource_update.ResourceUpdateStateNotStarted,
			},
			{
				Resource: pkgmodel.Resource{
					Label:      "resource2",
					Type:       "AWS::EC2::Instance",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{"ami":"ami-12345678"}`),
					Ksuid:      resource2Ksuid.KSUID(),
				},
				State: resource_update.ResourceUpdateStateNotStarted,
			},
		},
	}

	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)

	now := util.TimeNow()
	failResources := MarkResourcesAsFailed{
		CommandID: formaCommand.ID,
		ResourceUris: []pkgmodel.FormaeURI{
			resource1Ksuid,
			resource2Ksuid,
		},
		ResourceModifiedTs: now,
	}

	updateResult := formaPersister.Call(sender, failResources)
	assert.NoError(t, updateResult.Error)
	assert.True(t, updateResult.Response.(bool))

	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand := loadResult.Response.(*forma_command.FormaCommand)

	// Verify first command unchanged, second and third commands failed
	assert.Equal(t, resource_update.ResourceUpdateStateNotStarted, loadedCommand.ResourceUpdates[0].State)
	assert.Equal(t, resource_update.ResourceUpdateStateFailed, loadedCommand.ResourceUpdates[1].State)
	assert.Equal(t, resource_update.ResourceUpdateStateFailed, loadedCommand.ResourceUpdates[2].State)

	// Ensure ts modified
	assert.WithinDuration(t, now, loadedCommand.ResourceUpdates[1].ModifiedTs, 1*time.Second)
	assert.WithinDuration(t, now, loadedCommand.ResourceUpdates[2].ModifiedTs, 1*time.Second)
}

func TestFormaCommandPersister_DeletesSyncCommandWithNoVersions(t *testing.T) {
	formaCommand := newSyncFormaCommand()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	// Store the sync command
	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)
	assert.True(t, storeResult.Response.(bool))

	// Mark the resource update as complete WITHOUT a version (no changes detected)
	resourceURI := formaCommand.ResourceUpdates[0].Resource.URI()
	markComplete := messages.MarkResourceUpdateAsComplete{
		CommandID:                  formaCommand.ID,
		ResourceURI:                resourceURI,
		FinalState:                 resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:            util.TimeNow(),
		ResourceModifiedTs:         util.TimeNow().Add(20 * time.Second),
		ResourceProperties:         formaCommand.ResourceUpdates[0].Resource.Properties,
		ResourceReadOnlyProperties: nil,
		Version:                    "", // No version - no changes detected
	}
	res := formaPersister.Call(sender, markComplete)
	assert.NoError(t, res.Error)
	assert.True(t, res.Response.(bool))

	// Verify the command was deleted (LoadFormaCommand should return an error)
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.Error(t, loadResult.Error, "Sync command with no versions should be deleted")
	assert.Contains(t, loadResult.Error.Error(), "forma command not found")
}

func TestFormaCommandPersister_KeepsSyncCommandWithVersions(t *testing.T) {
	formaCommand := newSyncFormaCommand()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	// Store the sync command
	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)
	assert.True(t, storeResult.Response.(bool))

	// Mark the resource update as complete WITH a version (changes detected)
	resourceURI := formaCommand.ResourceUpdates[0].Resource.URI()
	markComplete := messages.MarkResourceUpdateAsComplete{
		CommandID:                  formaCommand.ID,
		ResourceURI:                resourceURI,
		FinalState:                 resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:            util.TimeNow(),
		ResourceModifiedTs:         util.TimeNow().Add(20 * time.Second),
		ResourceProperties:         formaCommand.ResourceUpdates[0].Resource.Properties,
		ResourceReadOnlyProperties: nil,
		Version:                    "test-version-hash", // Has version - changes were detected
	}
	res := formaPersister.Call(sender, markComplete)
	assert.NoError(t, res.Error)
	assert.True(t, res.Response.(bool))

	// Verify the command was kept
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand, ok := loadResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok, "Sync command with versions should be kept")
	assert.NotNil(t, loadedCommand)
	assert.Equal(t, forma_command.CommandStateSuccess, loadedCommand.State)
	assert.Equal(t, "test-version-hash", loadedCommand.ResourceUpdates[0].Version)
}

func TestFormaCommandPersister_KeepsApplyCommandWithNoVersions(t *testing.T) {
	// Apply commands should always be kept, even without versions
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	formaCommand.Command = pkgmodel.CommandApply
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	// Store the apply command
	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)
	assert.True(t, storeResult.Response.(bool))

	// Mark the resource update as complete WITHOUT a version
	resourceURI := formaCommand.ResourceUpdates[0].Resource.URI()
	markComplete := messages.MarkResourceUpdateAsComplete{
		CommandID:                  formaCommand.ID,
		ResourceURI:                resourceURI,
		FinalState:                 resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:            util.TimeNow(),
		ResourceModifiedTs:         util.TimeNow().Add(20 * time.Second),
		ResourceProperties:         formaCommand.ResourceUpdates[0].Resource.Properties,
		ResourceReadOnlyProperties: nil,
		Version:                    "", // No version
	}
	res := formaPersister.Call(sender, markComplete)
	assert.NoError(t, res.Error)
	assert.True(t, res.Response.(bool))

	// Verify the command was kept (apply commands should not be deleted)
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand, ok := loadResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok, "Apply command should always be kept")
	assert.NotNil(t, loadedCommand)
	assert.Equal(t, forma_command.CommandStateSuccess, loadedCommand.State)
}

func newSyncFormaCommand() *forma_command.FormaCommand {
	resourceKsuid := util.NewID()

	return &forma_command.FormaCommand{
		ID:      "test-sync-command-id",
		Command: pkgmodel.CommandSync,
		Forma: pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "test-stack",
					Description: "A test stack",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "test-type",
					Stack:      "test-stack",
					Properties: json.RawMessage("{}"),
					Ksuid:      resourceKsuid,
				},
			},
		},
		Config: config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		},
		StartTs: util.TimeNow().Add(-1 * time.Hour),
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:      "test-resource",
					Type:       "test-type",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Ksuid:      resourceKsuid,
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				Operation:      resource_update.OperationRead,
				State:          resource_update.ResourceUpdateStateNotStarted,
				ProgressResult: []resource.ProgressResult{},
			},
		},
	}
}

func newFormaCommandWithCreateResourceUpdate() *forma_command.FormaCommand {
	resourceKsuid := util.NewID()

	return &forma_command.FormaCommand{
		ID: "test-forma-id",
		Forma: pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "test-stack",
					Description: "A test stack",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "test-type",
					Stack:      "test-stack",
					Properties: json.RawMessage("{}"),
					Ksuid:      resourceKsuid,
				},
			},
		},
		Config: config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		},
		StartTs: util.TimeNow().Add(-1 * time.Hour),
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:      "test-resource",
					Type:       "test-type",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Ksuid:      resourceKsuid,
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				Operation:      resource_update.OperationReplace,
				State:          resource_update.ResourceUpdateStateNotStarted,
				ProgressResult: []resource.ProgressResult{},
			},
		},
	}
}

func newFormaCommandPersisterForTest(t *testing.T) (*unit.TestActor, gen.PID, error) {
	env := map[gen.Env]any{
		"DatastoreConfig": pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.SqliteDatastore,
			Sqlite: pkgmodel.SqliteConfig{
				FilePath: ":memory:",
			},
		},
		"AgentID": "test-agent-id",
		"Context": context.Background(),
	}

	sender := gen.PID{Node: "test", ID: 100}

	operator, error := unit.Spawn(t, NewFormaCommandPersister, unit.WithEnv(env))
	if error != nil {
		return nil, gen.PID{}, error
	}

	return operator, sender, nil
}
