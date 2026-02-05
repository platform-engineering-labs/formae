// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetastructure_StoreNewStack(t *testing.T) {
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
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		formaInitial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		m.ApplyForma(formaInitial, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			firstApply := fas[0]
			stackResources, err := m.Datastore.LoadResourcesByStack("test-stack1")
			return firstApply.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				firstApply.State == forma_command.CommandStateSuccess && err == nil && len(stackResources) > 0

		},
			2*time.Second,
			100*time.Millisecond,
			"Apply wasn't successfully",
		)
		stackResources, err := m.Datastore.LoadResourcesByStack("test-stack1")
		assert.NoError(t, err)
		assert.Equal(t, stackResources[0].Properties, json.RawMessage(`{"foo":"bar"}`))
		assert.NotEmpty(t, stackResources)
	})
}

func TestMetastructure_StorePatchStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "5678",
					ResourceProperties: request.Properties,
				}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationUpdate,
					OperationStatus: resource.OperationStatusInProgress,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   string(json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`)),
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "1234",
						NativeID:           "5678",
						ResourceProperties: json.RawMessage(`{"foo":"barbar","baz": "qux", "a":[3,4,2,7,8]}`),
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		formaInitial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "baz", "a"},
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

		m.ApplyForma(formaInitial, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			secondApply := fas[0]
			return secondApply.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				secondApply.State == forma_command.CommandStateSuccess
		},
			2*time.Second,
			100*time.Millisecond,
			"initial apply wasn't successfully",
		)

		formaPatch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"barbar","a":[7,8]}`),
					Stack:      "test-stack",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "baz", "a"},
					},
					Target: "test-target",
				},
			},
		}

		m.ApplyForma(formaPatch, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModePatch,
		}, "test")

		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			secondApply := fas[1]
			return secondApply.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				secondApply.State == forma_command.CommandStateSuccess
		},
			4*time.Second,
			100*time.Millisecond,
			"patch apply wasn't successfully",
		)
		time.Sleep(2 * time.Second)

		stackResources, err := m.Datastore.LoadResourcesByStack("test-stack")
		assert.NoError(t, err)
		assert.NotEmpty(t, stackResources)
		assert.JSONEq(t, string(json.RawMessage(`{"foo":"barbar","baz":"qux","a":[3,4,2,7,8]}`)), string(stackResources[0].Properties))
	})
}

func TestMetastructure_StorePatchAddResourceToStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "1234",
					ResourceProperties: request.Properties,
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   string(json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`)),
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "1234",
					ResourceProperties: request.DesiredProperties,
				}}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		formaInitial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "baz", "a"},
					},
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		m.ApplyForma(formaInitial, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		time.Sleep(2 * time.Second)

		formaPatch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource2",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"barbar","a":[7,8]}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo", "baz", "a"},
					},
				},
			},
		}

		m.ApplyForma(formaPatch, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModePatch,
		}, "test")

		time.Sleep(2 * time.Second)

		stackResources, err := m.Datastore.LoadResourcesByStack("test-stack")
		assert.NoError(t, err)
		assert.NotEmpty(t, stackResources)
		assert.JSONEq(t, `{"foo":"barbar","a":[7,8]}`, string(stackResources[1].Properties))
	})
}

func TestMetastructure_StackForApplyImplicitReplaceModeWithRemoveOfOneResource(t *testing.T) {
	t.Skip("Needs to be fixed after FSM - test needs to call apply")
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// This is a test setup that makes it easier to troubleshoot the storeStack
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource-one",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"name": "bucket1"}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
				{
					Label:      "test-resource-two", //Needs to be destroy name for the fake plugin to give the right operation
					Type:       "FakeAWS::S3::BucketDestroy",
					Properties: json.RawMessage(`{"name": "bucket2"}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		formaCommand, _ := FormaCommandFromForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, pkgmodel.CommandApply, m.Datastore, "test", resource_update.FormaCommandSourceUser)

		assert.Equal(t, formaCommand.ResourceUpdates[0].State, forma_command.CommandStateNotStarted)

		for i := range formaCommand.ResourceUpdates {
			formaCommand.ResourceUpdates[i].State = resource_update.ResourceUpdateStateSuccess
		}

		err = m.Datastore.StoreFormaCommand(formaCommand, formaCommand.ID)
		assert.NoError(t, err)
		assert.Equal(t, formaCommand.ResourceUpdates[0].State, resource_update.ResourceUpdateStateSuccess)
	})
}

func TestMetastructure_StackForApplyImplicitReplaceModeWithRenameLabelOfResourceShouldCreateAndDestroy(t *testing.T) {
	t.Skip("Needs to be fixed after FSM - test needs to call apply")
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// This is a test setup that makes it easier to troubleshoot the storeStack
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource-one",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"name": "bucket1"}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		formaCommand, _ := FormaCommandFromForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, pkgmodel.CommandApply, m.Datastore, "test", resource_update.FormaCommandSourceUser)

		assert.Equal(t, formaCommand.ResourceUpdates[0].State, resource_update.ResourceUpdateStateNotStarted)

		for i := range formaCommand.ResourceUpdates {
			formaCommand.ResourceUpdates[i].State = resource_update.ResourceUpdateStateSuccess
		}

		err = m.Datastore.StoreFormaCommand(formaCommand, formaCommand.ID)
		assert.NoError(t, err)
		assert.Equal(t, formaCommand.ResourceUpdates[0].State, resource_update.ResourceUpdateStateSuccess)
	})
}

// TestMetastructure_StackOnlyForma_RejectsEmptyStackCreation tests that creating
// a new stack without any resources is rejected. Empty stacks are automatically
// cleaned up when the last resource is removed, so creating them manually is not allowed.
func TestMetastructure_StackOnlyForma_RejectsEmptyStackCreation(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// Try to apply a forma with only a stack (no resources)
		stackOnlyForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "empty-stack",
					Description: "A stack with only metadata, no resources",
				},
			},
		}

		_, err = m.ApplyForma(stackOnlyForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModePatch,
		}, "test-client")

		// Should be rejected with FormaEmptyStackRejectedError
		require.Error(t, err)
		var emptyStackErr apimodel.FormaEmptyStackRejectedError
		require.ErrorAs(t, err, &emptyStackErr)
		assert.Contains(t, emptyStackErr.EmptyStacks, "empty-stack")

		// Verify no stack was created
		stack, err := m.Datastore.GetStackByLabel("empty-stack")
		require.NoError(t, err)
		assert.Nil(t, stack, "Empty stack should not be created")
	})
}

// TestMetastructure_StackOnlyForma_RejectsEmptyStackCreation_ReconcileMode tests that
// reconcile mode also rejects creating empty stacks.
func TestMetastructure_StackOnlyForma_RejectsEmptyStackCreation_ReconcileMode(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// Try to apply a forma with only a stack (no resources) in reconcile mode
		stackOnlyForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "empty-reconcile-stack",
					Description: "Attempting to create empty stack in reconcile mode",
				},
			},
		}

		_, err = m.ApplyForma(stackOnlyForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")

		// Should be rejected with FormaEmptyStackRejectedError
		require.Error(t, err)
		var emptyStackErr apimodel.FormaEmptyStackRejectedError
		require.ErrorAs(t, err, &emptyStackErr)
		assert.Contains(t, emptyStackErr.EmptyStacks, "empty-reconcile-stack")
	})
}

// TestMetastructure_StackOnlyForma_UpdateDescription tests updating an existing
// stack's description with a stack-only forma in patch mode.
func TestMetastructure_StackOnlyForma_UpdateDescription(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					NativeID:        "test-native-id",
				}}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// First, create a stack with a resource
		initialForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "update-desc-stack",
					Description: "Original description",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      "update-desc-stack",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		m.ApplyForma(initialForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")

		// Wait for initial apply to complete
		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			if len(fas) == 0 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		},
			2*time.Second,
			100*time.Millisecond,
			"Initial apply should complete successfully",
		)

		// Verify initial stack description
		stack, err := m.Datastore.GetStackByLabel("update-desc-stack")
		require.NoError(t, err)
		require.NotNil(t, stack)
		assert.Equal(t, "Original description", stack.Description)

		// Now apply a stack-only forma to update the description
		updateForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "update-desc-stack",
					Description: "Updated description via patch mode",
				},
			},
		}

		m.ApplyForma(updateForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModePatch,
		}, "test-client-2")

		// The update command should complete with Success state
		require.Eventually(t, func() bool {
			fas, _ := m.Datastore.LoadFormaCommands()
			if len(fas) < 2 {
				return false
			}
			return fas[1].State == forma_command.CommandStateSuccess
		},
			2*time.Second,
			100*time.Millisecond,
			"Stack description update should reach Success state",
		)

		// Verify the stack description was updated
		updatedStack, err := m.Datastore.GetStackByLabel("update-desc-stack")
		require.NoError(t, err)
		require.NotNil(t, updatedStack)
		assert.Equal(t, "Updated description via patch mode", updatedStack.Description)

		// Verify the resource still exists (wasn't affected by the stack-only update)
		resources, err := m.Datastore.LoadResourcesByStack("update-desc-stack")
		require.NoError(t, err)
		assert.Len(t, resources, 1)
	})
}
