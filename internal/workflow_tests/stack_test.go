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

	. "github.com/platform-engineering-labs/formae/internal/metastructure"
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

func TestMetastructure_StoreNewStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.Resource.Type,
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
			stack, err := m.Datastore.LoadStack("test-stack1")
			return firstApply.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				firstApply.State == forma_command.CommandStateSuccess && err == nil && stack != nil

		},
			2*time.Second,
			100*time.Millisecond,
			"Apply wasn't successfully",
		)
		stack, err := m.Datastore.LoadStack("test-stack1")
		assert.NoError(t, err)
		assert.Equal(t, stack.Resources[0].Properties, json.RawMessage(`{"foo":"bar"}`))
		assert.NotNil(t, stack)
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
					ResourceType:       request.Resource.Type,
					ResourceProperties: request.Resource.Properties,
				}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationUpdate,
					OperationStatus: resource.OperationStatusInProgress,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.Resource.Type,
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
						ResourceType:       request.ResourceType,
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

		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)
		assert.NotNil(t, stack)
		assert.JSONEq(t, string(json.RawMessage(`{"foo":"barbar","baz":"qux","a":[3,4,2,7,8]}`)), string(stack.Resources[0].Properties))
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
					ResourceType:       request.Resource.Type,
					ResourceProperties: request.Resource.Properties,
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
					ResourceType:       request.Resource.Type,
					ResourceProperties: request.Resource.Properties,
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

		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)
		assert.NotNil(t, stack)
		assert.JSONEq(t, `{"foo":"barbar","a":[7,8]}`, string(stack.Resources[1].Properties))
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
