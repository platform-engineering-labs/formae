// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package workflow_tests

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
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
		// Setup plugin overrides to simulate a long-running operation
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "test-request-id",
						NativeID:        "test-native-id",
						ResourceType:    request.Resource.Type,
						StartTs:         time.Now(),
						ModifiedTs:      time.Now(),
						Attempts:        1,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				// Always return InProgress to simulate a long-running operation
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

		// Create a forma with a single resource
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource-cancel",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      "test-stack",
					Target:     "test-target",
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

		// Wait for the resource update to be in progress (polling the datastore)
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			if len(commands) != 1 {
				return false
			}

			if len(commands[0].ResourceUpdates) != 1 {
				return false
			}

			return commands[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateInProgress
		}, 10*time.Second, 100*time.Millisecond, "Expected resource update to be in progress")

		// Cancel the command
		err = m.CancelCommand(commandID, "test-client")
		require.NoError(t, err)

		// TODO: Uncomment once cancellation is fully implemented
		// Verify the command was canceled
		// assert.Eventually(t, func() bool {
		// 	commands, err := m.Datastore.LoadFormaCommands()
		// 	if err != nil {
		// 		t.Logf("Failed to load commands: %v", err)
		// 		return false
		// 	}

		// 	if len(commands) != 1 {
		// 		t.Logf("Expected 1 command, got %d", len(commands))
		// 		return false
		// 	}

		// 	cmd := commands[0]

		// 	// Check command state
		// 	if cmd.State != forma_command.CommandStateCanceled {
		// 		t.Logf("Command state is %s, expected %s", cmd.State, forma_command.CommandStateCanceled)
		// 		return false
		// 	}

		// 	// Check resource update state
		// 	if len(cmd.ResourceUpdates) != 1 {
		// 		t.Logf("Expected 1 resource update, got %d", len(cmd.ResourceUpdates))
		// 		return false
		// 	}

		// 	if cmd.ResourceUpdates[0].State != resource_update.ResourceUpdateStateCanceled {
		// 		t.Logf("Resource update state is %s, expected %s",
		// 			cmd.ResourceUpdates[0].State,
		// 			resource_update.ResourceUpdateStateCanceled)
		// 		return false
		// 	}

		// 	return true
		// }, 10*time.Second, 100*time.Millisecond, "Expected command to be canceled")

		// // Verify no resources were created in the datastore
		// stack, err := m.Datastore.LoadStack("test-stack")
		// if err == nil && stack != nil {
		// 	assert.Empty(t, stack.Resources, "Expected no resources to be created after cancellation")
		// }
	})
}
