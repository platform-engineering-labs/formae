// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

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
        "github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
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
			return len(fas) == 2 && fas[1].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess
		}, 2*time.Second, 100*time.Millisecond)
		require.True(t, readCalled, "Read should have been called")

		// Check that the resource is either removed or marked as not found
		stacks, err = m.Datastore.LoadAllStacks()
		require.NoError(t, err)
		require.Equal(t, 0, len(stacks))

		formaCommands, _ := m.Datastore.LoadFormaCommands()

		assert.Equal(t, 2, len(formaCommands), "There should be two forma command recorded")

		assert.Equal(t, pkgmodel.CommandSync, formaCommands[1].Command, "The second command should be a sync command")
		assert.Equal(t, 1, len(formaCommands[0].ResourceUpdates), "There should be one resource update in the sync command")
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
