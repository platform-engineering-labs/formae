// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestSynchronizer_ApplyThenChangeThenSyncStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Resource.Label == "1" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "1",
							NativeID:        "1",
							ResourceType:    request.Resource.Type,
						},
					}, nil
				} else {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "2",
							NativeID:        "2",
							ResourceType:    request.Resource.Type,
						},
					}, nil
				}
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if request.NativeID == "1" {
					return &resource.ReadResult{ResourceType: request.ResourceType,
						Properties: `{"foo":"1","bar":"2","foobar":"3"}`,
					}, nil
				} else {
					return &resource.ReadResult{ResourceType: request.ResourceType,
						Properties: `{"foo":"11","bar":"22","foobar":"33"}`,
					}, nil
				}
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1",
					NativeID:        "1",
					ResourceType:    request.ResourceType,
				}}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = true
		cfg.Agent.Synchronization.Interval = 2 * time.Second
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "1",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"01","bar":"02","foobar":"03"}`),
					Stack:      "test-stack1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "bar", "foobar"},
					},
					Target: "test-target",
				},
				{
					Label:      "2",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"011","bar":"022","foobar":"033"}`),
					Stack:      "test-stack1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "bar", "foobar"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		require := require.New(t)
		require.Eventually(
			func() bool {
				stacks, err := m.Datastore.LoadAllStacks()
				if err != nil {
					return false
				}
				if len(stacks) != 1 || len(stacks[0].Resources) != 2 {
					return false
				}
				var resource1, resource2 pkgmodel.Resource
				if stacks[0].Resources[0].Label == "1" {
					resource1 = stacks[0].Resources[0]
					resource2 = stacks[0].Resources[1]
				} else {
					resource1 = stacks[0].Resources[1]
					resource2 = stacks[0].Resources[0]
				}
				return util.JsonEqual(`{"foo":"1","bar":"2","foobar":"3"}`, string(resource1.Properties)) && util.JsonEqual(`{"foo":"11","bar":"22","foobar":"33"}`, string(resource2.Properties))
			},
			10*time.Second,
			200*time.Millisecond)
	})
}

func TestSynchronizer_ApplyThenDestroyThenSyncStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Resource.Label == "1" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "1",
							NativeID:        "1",
							ResourceType:    request.Resource.Type,
						},
					}, nil
				} else {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       "2",
							NativeID:        "2",
							ResourceType:    request.Resource.Type,
						},
					}, nil
				}
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if request.NativeID == "1" {
					return &resource.ReadResult{ResourceType: request.ResourceType,
						Properties: `{"foo":"1","bar":"2","foobar":"3"}`,
					}, nil
				}

				return nil, fmt.Errorf("resource not found")
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = true
		cfg.Agent.Synchronization.Interval = 2 * time.Second
		cfg.Agent.Retry.MaxRetries = 0
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		stack := "test-stack-" + uuid.New().String()

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: stack,
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "1",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"01","bar":"02","foobar":"03"}`),
					Stack:      stack,
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "bar", "foobar"},
					},
					Target: "test-target",
				},
				{
					Label:      "2",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"011","bar":"022","foobar":"033"}`),
					Stack:      stack,
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "bar", "foobar"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		require := require.New(t)
		require.Eventually(
			func() bool {
				stacks, err := m.Datastore.LoadAllStacks()
				if err != nil {
					return false
				}
				return len(stacks) == 1 && len(stacks[0].Resources) == 2
			},
			4*time.Second,
			200*time.Millisecond)
	})
}

func TestSynchronizer_SynchronizeOnce(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		readCalled := false
		updated := map[string]string{"foo": "updated", "bar": "updated"}
		updatedJson, _ := json.Marshal(updated)

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "1",
						NativeID:        "1",
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				readCalled = true

				if request.NativeID == "1" {
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   string(updatedJson),
					}, nil
				}

				return nil, fmt.Errorf("resource not found")
			},
		}

		// Set synchronization interval to 0 to disable automatic sync
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		stack := "test-stack-" + util.NewID()
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: stack,
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "1",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"foo":"original","bar":"original"}`),
					Stack:      stack,
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "bar", "foobar"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test")
		assert.NoError(t, err)
		time.Sleep(1 * time.Second)

		// Manual one-time synchronization
		err = m.ForceSync()
		assert.NoError(t, err)
		time.Sleep(2 * time.Second)
		require.True(t, readCalled, "Read function should have been called during force sync")

		// Verify synchronization happened
		stacks, err := m.Datastore.LoadAllStacks()
		assert.NoError(t, err)
		assert.Equal(t, 1, len(stacks))
		assert.Equal(t, 1, len(stacks[0].Resources))

		var got map[string]string
		err = json.Unmarshal(stacks[0].Resources[0].Properties, &got)
		assert.NoError(t, err)

		assert.Equal(t, updated, got)

		// Wait long enough to ensure no additional syncs would have occurred
		// if the timer was incorrectly set
		time.Sleep(2 * time.Second)
		stacks, err = m.Datastore.LoadAllStacks()
		assert.NoError(t, err)

		initialProps := got
		var laterProps map[string]string
		err = json.Unmarshal(stacks[0].Resources[0].Properties, &laterProps)
		assert.NoError(t, err)
		assert.Equal(t, initialProps, laterProps)
	})
}

func TestSynchronizer_SyncHandlesResourceNotFound(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		readCalled := false

		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				readCalled = true
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					ErrorCode:    resource.OperationErrorCodeNotFound,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)

		defer def()
		require.NoError(t, err)

		db, ok := m.Datastore.(datastore.TestDatastoreSQLite)
		require.True(t, ok)

		db.ClearCommandsTable()

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack-notfound",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "notfound",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      "test-stack-notfound",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return len(fas) == 1 && fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess
		}, 2*time.Second, 100*time.Millisecond)
		stacks, err := m.Datastore.LoadAllStacks()
		require.NoError(t, err)
		require.Equal(t, 1, len(stacks))
		require.NoError(t, err)

		// Manual one-time synchronization
		err = m.ForceSync()
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			if len(fas) != 2 {
				return false
			}
			// Find the sync command by type (don't rely on order)
			for _, fc := range fas {
				if fc.Command == pkgmodel.CommandSync && len(fc.ResourceUpdates) > 0 {
					if fc.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess {
						return true
					}
				}
			}
			return false
		}, 2*time.Second, 100*time.Millisecond)
		require.True(t, readCalled, "Read should have been called")

		// Check that the resource is either removed or marked as not found
		stacks, err = m.Datastore.LoadAllStacks()
		require.NoError(t, err)
		require.Equal(t, 0, len(stacks))

		formaCommands, _ := m.Datastore.LoadFormaCommands()

		assert.Equal(t, 2, len(formaCommands), "There should be two forma command recorded")

		// Find apply and sync commands by type (don't rely on order)
		var applyCmd, syncCmd *forma_command.FormaCommand
		for _, fc := range formaCommands {
			switch fc.Command {
			case pkgmodel.CommandApply:
				applyCmd = fc
			case pkgmodel.CommandSync:
				syncCmd = fc
			}
		}

		assert.NotNil(t, syncCmd, "The sync command should exist")
		assert.NotNil(t, applyCmd, "The apply command should exist")
		assert.Equal(t, 1, len(syncCmd.ResourceUpdates), "There should be one resource update in the sync command")
	})

}

func TestSynchronizer_OverlapProtection(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		blockFirstSync := make(chan struct{})
		firstSyncStarted := make(chan struct{})
		syncCallCount := 0
		var mu sync.Mutex

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       request.Resource.Label,
						NativeID:        request.Resource.Label,
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				mu.Lock()
				syncCallCount++
				callNum := syncCallCount
				mu.Unlock()

				if callNum == 1 {
					select {
					case firstSyncStarted <- struct{}{}:
					default:
					}
					// Block the first sync
					<-blockFirstSync
				}

				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"synced"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "resource-1",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"foo":"original"}`),
					Stack:      stack,
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Target:     "test-target",
				},
				{
					Label:      "resource-2",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"foo":"original"}`),
					Stack:      stack,
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test")
		require.NoError(t, err)

		// Wait for resources to be created
		require.Eventually(t, func() bool {
			stacks, err := m.Datastore.LoadAllStacks()
			return err == nil && len(stacks) == 1 && len(stacks[0].Resources) == 2
		}, 5*time.Second, 100*time.Millisecond)

		// Start first sync (will block)
		go func() {
			err := m.ForceSync()
			assert.NoError(t, err)
		}()

		// Wait for first sync to start
		<-firstSyncStarted

		err = m.ForceSync()
		assert.NoError(t, err)

		// Start third sync (should be ignored)
		err = m.ForceSync()
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		// Unblock the first sync
		close(blockFirstSync)

		// Wait for syncs to complete, one per resource
		require.Eventually(t, func() bool {
			return syncCallCount >= 2
		}, 10*time.Second, 100*time.Millisecond)

		time.Sleep(500 * time.Millisecond)

		// With 2 resources x 3 sync requests, expect minimal overlap due to fast mock operations
		assert.GreaterOrEqual(t, syncCallCount, 2, "Should have at least 2 read calls (one per resource)")
		assert.LessOrEqual(t, syncCallCount, 4, "Should not have more than 4 read calls (overlap protection should prevent excessive calls)")
	})
}
func TestSynchronizer_ExcludesResourcesBeingUpdatedByApply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Set up test logger to capture log output for deterministic assertions
		logCapture := test_helpers.SetupTestLogger()

		// Channels to coordinate the test
		updateStarted := make(chan struct{})
		updateCanComplete := make(chan struct{})

		var mu sync.Mutex
		statusCallCount := 0

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "create-" + request.Resource.Label,
						NativeID:        request.Resource.Label,
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				// Signal that update has started
				select {
				case updateStarted <- struct{}{}:
				default:
				}

				// Return InProgress status
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationUpdate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "update-" + request.Resource.Label,
						NativeID:        request.Resource.Label,
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				mu.Lock()
				statusCallCount++
				mu.Unlock()

				// Keep returning InProgress until we get the signal to complete
				// This simulates a long-running update operation
				select {
				case <-updateCanComplete:
					// Signal received, return success
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationUpdate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       request.RequestID,
							ResourceType:    request.ResourceType,
						},
					}, nil
				default:
					// Still in progress
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationUpdate,
							OperationStatus: resource.OperationStatusInProgress,
							RequestID:       request.RequestID,
							ResourceType:    request.ResourceType,
						},
					}, nil
				}
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// Return original properties - we're not simulating an out-of-band change
				// The update will change the properties via the Update operation
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"original","bar":"original"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false // Manual sync control
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()

		// Initial apply to create the resource
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"foo":"original","bar":"original"}`),
					Stack:      stack,
					Schema:     pkgmodel.Schema{Fields: []string{"foo", "bar"}},
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test")
		require.NoError(t, err)

		// Wait for initial resource creation
		require.Eventually(t, func() bool {
			stacks, err := m.Datastore.LoadAllStacks()
			return err == nil && len(stacks) == 1 && len(stacks[0].Resources) == 1
		}, 5*time.Second, 100*time.Millisecond)

		// Get the initial resource to check versions later
		stacks, err := m.Datastore.LoadAllStacks()
		require.NoError(t, err)
		initialResource := stacks[0].Resources[0]

		// Start update in a goroutine using reconcile mode
		// This is more realistic - a full reconcile that happens to update the resource
		// Note: We don't include Targets since the target hasn't changed and reconcile
		// would check if target config differs from what's already stored
		go func() {
			fUpdate := &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: stack}},
				Resources: []pkgmodel.Resource{
					{
						Label:      "test-resource",
						Type:       "FakeAWS::Resource",
						Properties: json.RawMessage(`{"foo":"updated","bar":"updated"}`),
						Stack:      stack,
						Schema:     pkgmodel.Schema{Fields: []string{"foo", "bar"}},
						Target:     "test-target",
					},
				},
				Targets: []pkgmodel.Target{}, // Empty - target already exists and hasn't changed
			}
			_, err := m.ApplyForma(fUpdate, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test")
			assert.NoError(t, err)
		}()

		// Wait for update to start
		<-updateStarted

		// Trigger sync while update is in progress
		err = m.ForceSync()
		require.NoError(t, err)

		// Wait for sync to FINISH processing (log-based, deterministic)
		// When our fix works, sync will exclude the resource being updated and have no resources to sync
		// This logs either "Synchronizer: no resources found to synchronize" or "Synchronization finished"
		require.Eventually(t, func() bool {
			return logCapture.ContainsAll("Starting resource synchronization") &&
				(logCapture.ContainsAll("Synchronizer: no resources found to synchronize") ||
					logCapture.ContainsAll("Synchronization finished"))
		}, 10*time.Second, 100*time.Millisecond,
			"Synchronizer should have processed (either finished or found no resources)")

		// Check that sync command either wasn't created OR didn't include the resource being updated
		// When our fix works, all resources are filtered out, so either:
		// 1. No sync command is created (synchronizer returns early)
		// 2. Sync command exists but has no resource updates
		commands, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		var syncCommand *forma_command.FormaCommand
		for i := range commands {
			if commands[i].Command == pkgmodel.CommandSync {
				syncCommand = commands[i]
				break
			}
		}

		// If sync command was created, it should NOT include the resource being updated
		if syncCommand != nil {
			for _, ru := range syncCommand.ResourceUpdates {
				assert.NotEqual(t, initialResource.URI(), ru.URI(), "Sync should not include resource being updated by apply")
			}
			// Verify the sync command is empty (all resources were filtered out)
			assert.Empty(t, syncCommand.ResourceUpdates, "Sync command should have no resource updates (all filtered out)")
		}
		// If no sync command was created, that's also correct behavior (synchronizer filtered out all resources)

		// Allow the update to complete
		close(updateCanComplete)

		// Wait for update to complete
		require.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}
			// Find the update command (should be the second reconcile command)
			reconcileCount := 0
			for _, cmd := range commands {
				if cmd.Command == pkgmodel.CommandApply && cmd.Config.Mode == pkgmodel.FormaApplyModeReconcile {
					reconcileCount++
					// The second reconcile is our update
					if reconcileCount == 2 && cmd.State == forma_command.CommandStateSuccess {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond)

		// Verify both commands completed successfully
		commands, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		// Should have at least 2 commands: initial create (reconcile), update (reconcile)
		// Sync command might not exist if all resources were filtered out (which is correct!)
		require.GreaterOrEqual(t, len(commands), 2, "Should have at least 2 commands")

		// Find and verify update command completed successfully (second reconcile)
		var updateCmd *forma_command.FormaCommand
		reconcileCount := 0
		for i := range commands {
			if commands[i].Command == pkgmodel.CommandApply && commands[i].Config.Mode == pkgmodel.FormaApplyModeReconcile {
				reconcileCount++
				if reconcileCount == 2 {
					updateCmd = commands[i]
					break
				}
			}
		}
		require.NotNil(t, updateCmd, "Update command should exist")
		require.Equal(t, forma_command.CommandStateSuccess, updateCmd.State, "Update command should complete successfully")

		// If a sync command was created, verify it completed successfully
		// (It might not exist if all resources were filtered out)
		syncCommand = nil
		for i := range commands {
			if commands[i].Command == pkgmodel.CommandSync {
				syncCommand = commands[i]
				break
			}
		}
		if syncCommand != nil {
			require.Equal(t, forma_command.CommandStateSuccess, syncCommand.State, "Sync command should complete successfully if it was created")
		}
	})
}

// TestSynchronizer_SyncDoesNotOverwriteApplyStackChange verifies that sync preserves
// user-controlled state (Stack, Managed, Schema.Discoverable, Schema.Extractable) when
// a race occurs between apply and sync operations.
func TestSynchronizer_SyncDoesNotOverwriteApplyStackChange(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		readStarted := make(chan struct{})
		readCanComplete := make(chan struct{})
		readCount := 0
		var mu sync.Mutex

		nativeID := "vpc-" + uuid.New().String()

		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				mu.Lock()
				readCount++
				currentRead := readCount
				mu.Unlock()

				if currentRead == 1 {
					select {
					case readStarted <- struct{}{}:
					default:
					}
					<-readCanComplete
				}

				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"CidrBlock":"10.0.0.0/16","VpcId":"` + nativeID + `"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		unmanagedResource := &pkgmodel.Resource{
			Ksuid:      util.NewID(),
			NativeID:   nativeID,
			Stack:      "$unmanaged",
			Type:       "FakeAWS::EC2::VPC",
			Label:      nativeID,
			Target:     "test-target",
			Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
			Schema:     pkgmodel.Schema{Discoverable: false, Extractable: false},
			Managed:    false,
		}
		_, err = m.Datastore.StoreResource(unmanagedResource, "discovery-cmd")
		require.NoError(t, err)

		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{Label: "test-target", Discoverable: true})
		require.NoError(t, err)

		go func() { _ = m.ForceSync() }()
		<-readStarted

		managedStack := "my-managed-stack"
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: managedStack}},
			Resources: []pkgmodel.Resource{{
				Ksuid:      unmanagedResource.Ksuid,
				Label:      nativeID,
				Type:       "FakeAWS::EC2::VPC",
				NativeID:   nativeID,
				Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
				Stack:      managedStack,
				Schema:     pkgmodel.Schema{Fields: []string{"CidrBlock"}, Discoverable: true, Extractable: true},
				Target:     "test-target",
				Managed:    true,
			}},
			Targets: []pkgmodel.Target{},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			res, err := m.Datastore.LoadResourceByNativeID(nativeID, "FakeAWS::EC2::VPC")
			return err == nil && res != nil && res.Stack == managedStack && res.Managed
		}, 5*time.Second, 100*time.Millisecond)

		// Unblock sync's READ
		close(readCanComplete)
		time.Sleep(2 * time.Second)

		res, err := m.Datastore.LoadResourceByNativeID(nativeID, "FakeAWS::EC2::VPC")
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, managedStack, res.Stack, "Stack should be preserved")
		assert.True(t, res.Managed, "Managed should be preserved")
		assert.True(t, res.Schema.Discoverable, "Schema.Discoverable should be preserved")
		assert.True(t, res.Schema.Extractable, "Schema.Extractable should be preserved")
	})
}
