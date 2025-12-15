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
		Operation:          resource_update.OperationCreate,
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
		Operation:          resource_update.OperationCreate,
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
				Operation: resource_update.OperationCreate,
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
				Operation: resource_update.OperationCreate,
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
				Operation: resource_update.OperationCreate,
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
		Resources: []ResourceUpdateRef{
			{URI: resource1Ksuid, Operation: resource_update.OperationCreate},
			{URI: resource2Ksuid, Operation: resource_update.OperationCreate},
		},
		ResourceModifiedTs: now,
	}

	updateResult := formaPersister.Call(sender, failResources)
	assert.NoError(t, updateResult.Error)
	assert.True(t, updateResult.Response.(bool))

	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand := loadResult.Response.(*forma_command.FormaCommand)

	// Build a map of KSUID -> ResourceUpdate for order-agnostic assertions
	ruByKsuid := make(map[string]*resource_update.ResourceUpdate)
	for i := range loadedCommand.ResourceUpdates {
		ru := &loadedCommand.ResourceUpdates[i]
		ruByKsuid[ru.Resource.Ksuid] = ru
	}

	// Verify resource0 unchanged, resource1 and resource2 failed
	assert.Equal(t, resource_update.ResourceUpdateStateNotStarted, ruByKsuid[resource0Ksuid.KSUID()].State)
	assert.Equal(t, resource_update.ResourceUpdateStateFailed, ruByKsuid[resource1Ksuid.KSUID()].State)
	assert.Equal(t, resource_update.ResourceUpdateStateFailed, ruByKsuid[resource2Ksuid.KSUID()].State)

	// Ensure ts modified for failed resources
	assert.WithinDuration(t, now, ruByKsuid[resource1Ksuid.KSUID()].ModifiedTs, 1*time.Second)
	assert.WithinDuration(t, now, ruByKsuid[resource2Ksuid.KSUID()].ModifiedTs, 1*time.Second)
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
		Operation:                  formaCommand.ResourceUpdates[0].Operation,
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
		Operation:                  formaCommand.ResourceUpdates[0].Operation,
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
		Operation:                  formaCommand.ResourceUpdates[0].Operation,
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
		ID:          "test-sync-command-id",
		Command:     pkgmodel.CommandSync,
		Description: pkgmodel.Description{},
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
				StackLabel:     "test-stack",
			},
		},
	}
}

func newFormaCommandWithCreateResourceUpdate() *forma_command.FormaCommand {
	resourceKsuid := util.NewID()

	return &forma_command.FormaCommand{
		ID:          "test-forma-id",
		Description: pkgmodel.Description{},
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
				Operation:      resource_update.OperationCreate,
				State:          resource_update.ResourceUpdateStateNotStarted,
				ProgressResult: []resource.ProgressResult{},
				StackLabel:     "test-stack",
			},
		},
	}
}

// Test cache helper functions
func TestBuildResourceUpdateIndex(t *testing.T) {
	ksuid1 := util.NewID()
	ksuid2 := util.NewID()
	ksuid3 := util.NewID()

	cmd := &forma_command.FormaCommand{
		ID: "test-command",
		ResourceUpdates: []resource_update.ResourceUpdate{
			{Resource: pkgmodel.Resource{Ksuid: ksuid1, Label: "resource1"}, Operation: resource_update.OperationCreate},
			{Resource: pkgmodel.Resource{Ksuid: ksuid2, Label: "resource2"}, Operation: resource_update.OperationUpdate},
			{Resource: pkgmodel.Resource{Ksuid: ksuid3, Label: "resource3"}, Operation: resource_update.OperationDelete},
		},
	}

	ksuidOpToIndex := buildResourceUpdateIndex(cmd)

	// Composite key index should have 3 entries
	assert.Len(t, ksuidOpToIndex, 3)
	assert.Equal(t, 0, ksuidOpToIndex[resourceUpdateKey(ksuid1, resource_update.OperationCreate)])
	assert.Equal(t, 1, ksuidOpToIndex[resourceUpdateKey(ksuid2, resource_update.OperationUpdate)])
	assert.Equal(t, 2, ksuidOpToIndex[resourceUpdateKey(ksuid3, resource_update.OperationDelete)])
}

func TestBuildResourceUpdateIndex_DuplicateKsuids(t *testing.T) {
	// Test case: Replace operation stores two ResourceUpdates with same ksuid (delete + create)
	ksuid1 := util.NewID()

	cmd := &forma_command.FormaCommand{
		ID: "test-command",
		ResourceUpdates: []resource_update.ResourceUpdate{
			{Resource: pkgmodel.Resource{Ksuid: ksuid1, Label: "resource1"}, Operation: resource_update.OperationDelete},
			{Resource: pkgmodel.Resource{Ksuid: ksuid1, Label: "resource1"}, Operation: resource_update.OperationCreate},
		},
	}

	ksuidOpToIndex := buildResourceUpdateIndex(cmd)

	// Composite key index should have 2 entries (delete + create for same ksuid)
	assert.Len(t, ksuidOpToIndex, 2)
	assert.Equal(t, 0, ksuidOpToIndex[resourceUpdateKey(ksuid1, resource_update.OperationDelete)])
	assert.Equal(t, 1, ksuidOpToIndex[resourceUpdateKey(ksuid1, resource_update.OperationCreate)])
}

func TestCachedCommand_FindResourceUpdateIndex(t *testing.T) {
	ksuid1 := util.NewID()
	ksuid2 := util.NewID()

	cached := &cachedCommand{
		command: &forma_command.FormaCommand{
			ResourceUpdates: []resource_update.ResourceUpdate{
				{Resource: pkgmodel.Resource{Ksuid: ksuid1}, Operation: resource_update.OperationCreate},
				{Resource: pkgmodel.Resource{Ksuid: ksuid2}, Operation: resource_update.OperationUpdate},
			},
		},
		ksuidOpToIndex: map[string]int{
			resourceUpdateKey(ksuid1, resource_update.OperationCreate): 0,
			resourceUpdateKey(ksuid2, resource_update.OperationUpdate): 1,
		},
	}

	// Test composite key lookup
	assert.Equal(t, 0, cached.findResourceUpdateIndex(ksuid1, resource_update.OperationCreate))
	assert.Equal(t, 1, cached.findResourceUpdateIndex(ksuid2, resource_update.OperationUpdate))
	assert.Equal(t, -1, cached.findResourceUpdateIndex(ksuid1, resource_update.OperationDelete)) // wrong operation
	assert.Equal(t, -1, cached.findResourceUpdateIndex("nonexistent", resource_update.OperationCreate))
}

func TestCachedCommand_FindResourceUpdateIndex_DuplicateKsuid(t *testing.T) {
	// Test case: same ksuid with different operations (replace = delete + create)
	ksuid1 := util.NewID()

	cached := &cachedCommand{
		command: &forma_command.FormaCommand{
			ResourceUpdates: []resource_update.ResourceUpdate{
				{Resource: pkgmodel.Resource{Ksuid: ksuid1}, Operation: resource_update.OperationDelete},
				{Resource: pkgmodel.Resource{Ksuid: ksuid1}, Operation: resource_update.OperationCreate},
			},
		},
		ksuidOpToIndex: map[string]int{
			resourceUpdateKey(ksuid1, resource_update.OperationDelete): 0,
			resourceUpdateKey(ksuid1, resource_update.OperationCreate): 1,
		},
	}

	// Composite key lookup should find the correct entry
	assert.Equal(t, 0, cached.findResourceUpdateIndex(ksuid1, resource_update.OperationDelete))
	assert.Equal(t, 1, cached.findResourceUpdateIndex(ksuid1, resource_update.OperationCreate))
}

func TestFormaCommandPersister_CacheEvictionOnFinalState(t *testing.T) {
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	// Store the command - this should add it to the cache
	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)

	// Mark the resource as complete - this should trigger cache eviction
	resourceURI := formaCommand.ResourceUpdates[0].Resource.URI()
	markComplete := messages.MarkResourceUpdateAsComplete{
		CommandID:          formaCommand.ID,
		ResourceURI:        resourceURI,
		Operation:          formaCommand.ResourceUpdates[0].Operation,
		FinalState:         resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow().Add(20 * time.Second),
		Version:            "test-version",
	}
	res := formaPersister.Call(sender, markComplete)
	assert.NoError(t, res.Error)

	// Load the command again - this should work (from DB since cache was evicted)
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand := loadResult.Response.(*forma_command.FormaCommand)
	assert.Equal(t, forma_command.CommandStateSuccess, loadedCommand.State)
}

func TestFormaCommandPersister_MultipleProgressUpdatesUseCacheHit(t *testing.T) {
	// Create a command with multiple resources
	ksuid1 := util.NewID()
	ksuid2 := util.NewID()

	formaCommand := &forma_command.FormaCommand{
		ID: "test-multi-resource-cache",
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:      "resource1",
					Type:       "AWS::EC2::VPC",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{}`),
					Ksuid:      ksuid1,
				},
				Operation: resource_update.OperationCreate,
				State:     resource_update.ResourceUpdateStateNotStarted,
			},
			{
				Resource: pkgmodel.Resource{
					Label:      "resource2",
					Type:       "AWS::EC2::Subnet",
					Stack:      "test-stack",
					Properties: json.RawMessage(`{}`),
					Ksuid:      ksuid2,
				},
				Operation: resource_update.OperationCreate,
				State:     resource_update.ResourceUpdateStateNotStarted,
			},
		},
	}

	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)

	// Send multiple progress updates for both resources
	for i := 0; i < 3; i++ {
		for _, ksuid := range []string{ksuid1, ksuid2} {
			progress := messages.UpdateResourceProgress{
				CommandID:          formaCommand.ID,
				ResourceURI:        pkgmodel.NewFormaeURI(ksuid, ""),
				Operation:          resource_update.OperationCreate,
				ResourceStartTs:    util.TimeNow(),
				ResourceModifiedTs: util.TimeNow(),
				ResourceState:      resource_update.ResourceUpdateStateInProgress,
				Progress: resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
				},
			}
			res := formaPersister.Call(sender, progress)
			assert.NoError(t, res.Error)
		}
	}

	// Verify both resources are updated
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loadedCommand := loadResult.Response.(*forma_command.FormaCommand)

	assert.Equal(t, resource_update.ResourceUpdateStateInProgress, loadedCommand.ResourceUpdates[0].State)
	assert.Equal(t, resource_update.ResourceUpdateStateInProgress, loadedCommand.ResourceUpdates[1].State)
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
