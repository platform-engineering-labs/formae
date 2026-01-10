// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
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

func TestMetastructure_ApplyFormaSuccess(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
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
				{
					Label: "test-stack2",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-resource1",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack1",
					Target:  "test-target1",
					Managed: true,
				},
				{
					Label:   "test-resource2",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack2",
					Target:  "test-target2",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
				},
				{
					Label:     "test-target2",
					Namespace: "test-namespace2",
				},
			},
		}

		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			incomplete_commands, err := m.Datastore.LoadIncompleteFormaCommands()
			assert.NoError(t, err)

			return len(commands) == 1 && len(incomplete_commands) == 0 &&
				len(commands[0].ResourceUpdates) >= 2 &&
				commands[0].ResourceUpdates[0].DesiredState.Managed &&
				commands[0].ResourceUpdates[1].DesiredState.Managed
		}, 5*time.Second, 100*time.Millisecond)

		assert.Equal(t, 1, len(commands))
		assert.GreaterOrEqual(t, len(commands[0].ResourceUpdates), 2)
		assert.True(t, commands[0].ResourceUpdates[0].DesiredState.Managed)
		assert.True(t, commands[0].ResourceUpdates[1].DesiredState.Managed)
	})
}

func TestMetastructure_ApplyFormaCommandSuccess(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack",
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
		}

		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			fas_incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			assert.NoError(t, err)

			return len(fas) == 1 && len(fas_incomplete) == 0
		}, 5*time.Second, 100*time.Millisecond)
	})
}

func TestMetastructure_ApplyFormaImplicitReplaceMode(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.DesiredState.Type,
				}}, nil

			},

			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.ResourceType,
				}}, nil

			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()

		assert.NoError(t, err, "Failed to create metastructure")

		resource1 := &pkgmodel.Resource{
			Label:      "test-resource-one",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "bucket1"}`),
			Stack:      "test-stack1",
			Target:     "test-target",
		}

		f0 := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				*resource1,
				{
					Label:      "test-resource-delete",
					Type:       "FakeAWS::S3::BucketDestroy",
					Properties: json.RawMessage(`{"name": "bucket2"}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
					Config:    json.RawMessage(`{}`),
				},
			},
		}
		m.ApplyForma(f0, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			return (len(fas) == 1 &&
				len(fas[0].ResourceUpdates) == 2 &&
				fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				fas[0].ResourceUpdates[1].State == resource_update.ResourceUpdateStateSuccess)
		}, 10*time.Second, 100*time.Millisecond)

		_f1 := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				*resource1,
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
					Config:    json.RawMessage(`{}`),
				},
			},
		}
		m.ApplyForma(_f1, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		assert.NoError(t, err)
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			// Commands are ordered by timestamp DESC, so the newer delete command is at index 0
			return (len(fas) == 2 &&
				len(fas[0].ResourceUpdates) == 1 &&
				fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				fas[0].ResourceUpdates[0].Operation == resource_update.OperationDelete &&
				fas[0].ResourceUpdates[0].DesiredState.Label == "test-resource-delete")
		}, 5*time.Second, 100*time.Millisecond)
	})
}

func TestMetastructure_ApplyFormaCreateReplaceUpdate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		readCnt := 0
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.DesiredState.Type,
				}}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.ResourceType,
				}}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "5678",
					ResourceType:       request.DesiredState.Type,
					ResourceProperties: json.RawMessage(`{"name": "bucket1-renamed", "versioning": "Enabled"}`),
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// First read the original bucket. Next read the renamed bucket
				if readCnt == 0 {
					readCnt++
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"name": "bucket1", "versioning": "Disabled"}`,
					}, nil
				}
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"name": "bucket1-renamed", "versioning": "Disabled"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}
		// Step 1: Create initial resource
		initialResource := &pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "bucket1", "versioning": "Disabled"}`),
			Stack:      "test-stack",
			Target:     "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints: map[string]pkgmodel.FieldHint{
					"name": {
						CreateOnly: true,
					},
				},
				Fields: []string{"name", "versioning"},
			},
		}

		initialForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				*initialResource,
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		// Apply initial resource
		m.ApplyForma(initialForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		// Verify initial creation
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			return (len(fas) == 1 &&
				len(fas[0].ResourceUpdates) == 1 &&
				fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				fas[0].ResourceUpdates[0].Operation == resource_update.OperationCreate)
		}, 5*time.Second, 100*time.Millisecond)

		// Step 2: Replace resource by changing a CreateOnly property (name)
		replaceResource := &pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "bucket1-renamed", "versioning": "Disabled"}`),
			Stack:      "test-stack",
			Target:     "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints: map[string]pkgmodel.FieldHint{
					"name": {
						CreateOnly: true,
					},
				},
				Fields: []string{"name", "versioning"},
			},
		}

		replaceForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				*replaceResource,
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		// Apply replacement (should delete and recreate)
		m.ApplyForma(replaceForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		// Verify replacement (delete + create)
		// Commands are ordered by timestamp DESC, so the newest (replace) command is at index 0
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)

			if len(fas) != 2 || len(fas[0].ResourceUpdates) != 2 {
				return false
			}
			// Check for delete and create operations (order-agnostic within the command)
			hasDelete := false
			hasCreate := false
			for _, ru := range fas[0].ResourceUpdates {
				if ru.State == resource_update.ResourceUpdateStateSuccess {
					if ru.Operation == resource_update.OperationDelete {
						hasDelete = true
					}
					if ru.Operation == resource_update.OperationCreate {
						hasCreate = true
					}
				}
			}
			return hasDelete && hasCreate
		}, 5*time.Second, 100*time.Millisecond)

		// Verify the replaced resource has the new name
		fas, err := m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		// Find the create operation in the newest command
		for _, ru := range fas[0].ResourceUpdates {
			if ru.Operation == resource_update.OperationCreate {
				assert.Contains(t, string(ru.DesiredState.Properties), "bucket1-renamed")
				break
			}
		}

		// Step 3: Update resource by changing a non-CreateOnly property (versioning)
		updateResource := &pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "bucket1-renamed", "versioning": "Enabled"}`),
			Stack:      "test-stack",
			Target:     "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints: map[string]pkgmodel.FieldHint{
					"name": {
						CreateOnly: true,
					},
				},
				Fields: []string{"name", "versioning"},
			},
		}

		updateForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				*updateResource,
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		// Apply update (should update in place)
		m.ApplyForma(updateForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		// Verify update
		// Commands are ordered by timestamp DESC, so the newest (update) command is at index 0
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			return (len(fas) == 3 &&
				len(fas[0].ResourceUpdates) == 1 &&
				fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				fas[0].ResourceUpdates[0].Operation == resource_update.OperationUpdate)
		}, 5*time.Second, 100*time.Millisecond)

		fas, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Contains(t, string(fas[0].ResourceUpdates[0].DesiredState.Properties), "Enabled")
	})
}
