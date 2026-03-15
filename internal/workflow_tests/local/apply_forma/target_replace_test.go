// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestApplyForma_TargetReplace_HappyPath(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Seed initial state — create a target with config {"region":"us-east-1"} and a resource on it
		initialForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		_, err = m.ApplyForma(
			initialForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		// Wait for the first command to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(fas) == 1 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "first apply command should complete")

		// Step 2: Apply with changed target config — same target label and namespace, different config
		replaceForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"eu-west-1"}`),
				},
			},
		}

		_, err = m.ApplyForma(
			replaceForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		// Step 3: Wait for the second command to complete
		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(commands) == 2 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "second apply command should complete")

		// Step 4: Verify the second command has replace operations (delete + create) for the resource
		// Commands are ordered by timestamp DESC, so the newest command is at index 0
		replaceCommand := commands[0]
		require.GreaterOrEqual(t, len(replaceCommand.ResourceUpdates), 1, "replace command should have resource updates")

		hasDelete := false
		hasCreate := false
		for _, ru := range replaceCommand.ResourceUpdates {
			if ru.State == resource_update.ResourceUpdateStateSuccess {
				if ru.Operation == resource_update.OperationDelete {
					hasDelete = true
				}
				if ru.Operation == resource_update.OperationCreate {
					hasCreate = true
				}
			}
		}
		assert.True(t, hasDelete, "replace command should have a successful delete operation for the resource")
		assert.True(t, hasCreate, "replace command should have a successful create operation for the resource")

		// Verify the target in the datastore has the new config
		target, err := m.Datastore.LoadTarget("test-target")
		require.NoError(t, err)
		require.NotNil(t, target)
		assert.Equal(t, "test-target", target.Label)
		assert.Equal(t, "FakeAWS", target.Namespace)
		assert.JSONEq(t, `{"region":"eu-west-1"}`, string(target.Config))

		// Verify the resource still exists in the datastore
		resources, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Equal(t, "test-bucket", resources[0].Label)
	})
}

func TestApplyForma_TargetReplace_SimulateWarnsAboutUnmanagedResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Seed initial state — create a target and a managed resource
		initialForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		_, err = m.ApplyForma(
			initialForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		// Wait for the first command to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(fas) == 1 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "initial apply should complete")

		// Step 2: Insert an unmanaged resource on the same target
		unmanagedResource := pkgmodel.Resource{
			Label:      "discovered-lambda",
			Type:       "FakeAWS::Lambda::Function",
			Stack:      "$unmanaged",
			Target:     "test-target",
			Properties: json.RawMessage(`{"FunctionName":"my-lambda"}`),
			Managed:    false,
		}
		_, err = m.Datastore.BulkStoreResources([]pkgmodel.Resource{unmanagedResource}, "discovery-command-1")
		require.NoError(t, err)

		// Step 3: Simulate with changed target config
		simulateForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"eu-west-1"}`),
				},
			},
		}

		resp, err := m.ApplyForma(
			simulateForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id")
		require.NoError(t, err)

		// Step 4: Verify the simulation response
		assert.True(t, resp.Simulation.ChangesRequired, "simulation should indicate changes are required")
		require.NotEmpty(t, resp.Simulation.Warnings, "simulation should have warnings about unmanaged resources")

		// Verify the warning mentions the target label and unmanaged resource count.
		// The expected format is: Target "test-target" is being replaced. 1 unmanaged resource(s) ...
		foundWarning := false
		for _, warning := range resp.Simulation.Warnings {
			if strings.Contains(warning, "test-target") && strings.Contains(warning, "1 unmanaged resource") {
				foundWarning = true
				break
			}
		}
		assert.True(t, foundWarning, "should have a warning about unmanaged resources on the replaced target, got: %v", resp.Simulation.Warnings)
	})
}
