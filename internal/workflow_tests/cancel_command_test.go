// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetastructure_CancelCommand(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Track which resources have completed successfully (to simulate real plugin behavior)
		completedResources := make(map[string]bool)
		var completedMutex sync.Mutex

		// Setup plugin overrides to simulate long-running operations
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				// Return InProgress initially to trigger status polling
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "request-" + request.Resource.Label,
						NativeID:        "native-" + request.Resource.Label,
						ResourceType:    request.Resource.Type,
						StartTs:         time.Now(),
						ModifiedTs:      time.Now(),
						Attempts:        1,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				completedMutex.Lock()
				defer completedMutex.Unlock()

				// Check if this resource has "completed" (we'll mark them as complete after first status check)
				if completedResources[request.RequestID] {
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							RequestID:       request.RequestID,
							ResourceType:    request.ResourceType,
							StartTs:         time.Now(),
							ModifiedTs:      time.Now(),
						},
					}, nil
				}

				// Mark as complete for next status check (simulates resource becoming ready)
				completedResources[request.RequestID] = true

				// Return InProgress to simulate ongoing operation
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       request.RequestID,
						ResourceType:    request.ResourceType,
						StartTs:         time.Now(),
						ModifiedTs:      time.Now(),
					},
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Create a forma with multiple resources to test different cancellation scenarios:
		//
		// Resource setup:
		// 1. vpc: Independent resource, will start immediately
		// 2. bucket1: Independent resource, will start immediately
		// 3. subnet: Depends on VPC (via $res), will wait for VPC dependency
		// 4. bucket2: Independent resource
		//
		// We use Status override to keep resources InProgress for longer, giving us time to cancel.
		//
		// Expected behavior when we cancel:
		// - vpc and bucket1: Should complete successfully (in-progress, can't cancel - orphan risk)
		// - subnet: Should be canceled immediately (waiting on dependency)
		// - bucket2: May complete or be canceled depending on timing
		// - Command should transition to Canceling state, then Canceled once in-progress complete
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label: "vpc",
					Type:  "FakeAWS::EC2::VPC",
					Properties: json.RawMessage(`{
						"CidrBlock": "10.0.0.0/16"
					}`),
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
				{
					Label: "bucket1",
					Type:  "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{
						"BucketName": "test-bucket-1"
					}`),
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
				{
					Label: "subnet",
					Type:  "FakeAWS::EC2::Subnet",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "vpc",
							"$type": "FakeAWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
				{
					Label: "bucket2",
					Type:  "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{
						"BucketName": "test-bucket-2"
					}`),
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		// Apply the forma
		resp, err := m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)
		require.NotNil(t, resp)

		commandID := resp.CommandID

		// Wait for at least 2 resources to be in progress (vpc and bucket1)
		// We want to ensure these have started before we cancel
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			if len(commands) != 1 {
				return false
			}

			if len(commands[0].ResourceUpdates) != 4 {
				return false
			}

			inProgressCount := 0
			for _, ru := range commands[0].ResourceUpdates {
				if ru.State == resource_update.ResourceUpdateStateInProgress {
					inProgressCount++
				}
			}

			// Wait for at least 2 resources to be in progress (vpc and bucket1)
			return inProgressCount >= 2
		}, 10*time.Second, 100*time.Millisecond, "Expected at least 2 resource updates to be in progress")

		// Cancel the command while resources are still being processed
		err = m.CancelCommand(commandID, "test-client")
		require.NoError(t, err)

		// Verify the command was canceled
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				t.Logf("Failed to load commands: %v", err)
				return false
			}

			if len(commands) != 1 {
				t.Logf("Expected 1 command, got %d", len(commands))
				return false
			}

			cmd := commands[0]

			// Check command state - should eventually transition to Canceled
			// (may go through Canceling state first)
			if cmd.State != forma_command.CommandStateCanceled {
				t.Logf("Command state is %s, expected %s", cmd.State, forma_command.CommandStateCanceled)
				return false
			}

			return true
		}, 15*time.Second, 100*time.Millisecond, "Expected command to reach Canceled state")

		// Load final command state for detailed assertions
		commands, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 1)
		cmd := commands[0]

		// Verify resource update states
		require.Len(t, cmd.ResourceUpdates, 4)

		// Count resource updates by state and track which ones completed/canceled
		successCount := 0
		canceledCount := 0
		successfulResources := make([]string, 0)
		canceledResources := make([]string, 0)

		for _, ru := range cmd.ResourceUpdates {
			t.Logf("Resource %s: state=%s", ru.Resource.Label, ru.State)
			switch ru.State {
			case resource_update.ResourceUpdateStateSuccess:
				successCount++
				successfulResources = append(successfulResources, ru.Resource.Label)
			case resource_update.ResourceUpdateStateCanceled:
				canceledCount++
				canceledResources = append(canceledResources, ru.Resource.Label)
			default:
				t.Errorf("Unexpected resource state: %s for resource %s", ru.State, ru.Resource.Label)
			}
		}

		// Expected behavior:
		// - vpc and bucket1 should complete successfully (started immediately, in progress when canceled)
		// - subnet should be canceled (waiting on VPC dependency)
		// - bucket2 may complete or be canceled depending on timing
		assert.GreaterOrEqual(t, successCount, 2, "Expected at least 2 resources to complete successfully (vpc and bucket1)")
		assert.GreaterOrEqual(t, canceledCount, 1, "Expected at least 1 resource to be canceled (subnet)")
		assert.Equal(t, 4, successCount+canceledCount, "All resources should be either successful or canceled")

		// Verify which specific resources succeeded and were canceled
		assert.Contains(t, successfulResources, "vpc", "VPC should have completed successfully")
		assert.Contains(t, successfulResources, "bucket1", "Bucket1 should have completed successfully")
		assert.Contains(t, canceledResources, "subnet", "Subnet should have been canceled")

		// Verify only successful resources were persisted to the stack
		stack, err := m.Datastore.LoadStack("test-stack")
		if err == nil && stack != nil {
			// Only resources that completed successfully should be in the stack
			assert.Equal(t, successCount, len(stack.Resources),
				"Only successfully created resources should be persisted in the stack")
		}
	})
}
