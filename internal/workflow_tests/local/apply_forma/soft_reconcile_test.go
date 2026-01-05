// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

// allCommandsSuccessful returns true if all commands in the slice have succeeded
func allCommandsSuccessful(commands []*forma_command.FormaCommand) bool {
	for _, cmd := range commands {
		if cmd.State != forma_command.CommandStateSuccess {
			return false
		}
	}
	return true
}

// Soft reconcile means applying a forma without the `--force` flag. If the resources in the stack have been altered
// since the last reconcile, the apply will be rejected and the last update to the resource will be returned.
func TestApplyForma_SoftReconcile_ReturnsMostRecentChangesPerStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		resource1Reads, resource2Reads := 0, 0
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Resource.Label,
						ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if request.NativeID == "test-resource1" {
					var properties string
					switch resource1Reads {
					case 0:
						properties = `{"foo":"bar"}`
					case 1:
						properties = `{"foo":"baz"}`
					case 2:
						properties = `{"foo":"bazbaz"}`
					}
					resource1Reads++
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   properties,
					}, nil
				} else if request.NativeID == "test-resource2" {
					var properties string
					switch resource2Reads {
					case 0:
						properties = `{"foo":"bar"}`
					case 1:
						properties = `{"foo":"baz"}`
					}
					resource2Reads++

					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   properties,
					}, nil
				}
				return nil, errors.New("unknown resource")
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Resource.NativeID,
						ResourceProperties: json.RawMessage(`{"foo":"baz"}`),
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

		// initial apply to create resources
		initial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Managed:    true,
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			initial,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

		// patch resource1
		firstPatch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:    "test-resource1",
					NativeID: "test-resource1",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"baz"}`),
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			firstPatch,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			// Check all commands are successful (don't rely on order)
			return len(commands) == 2 && allCommandsSuccessful(commands)
		}, 5*time.Second, 100*time.Millisecond)

		// patch both resources
		secondPatch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:    "test-resource1",
					NativeID: "test-resource1",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bazbaz"}`),
					Managed:    true,
				},
				{
					Label:    "test-resource2",
					NativeID: "test-resource2",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"baz"}`),
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			secondPatch,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			// Check all commands are successful (don't rely on order)
			return len(commands) == 3 && allCommandsSuccessful(commands)
		}, 5*time.Second, 100*time.Millisecond)

		// reconcile the stack to a new state, forcing a hard reconcile
		reconcile := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:    "test-resource1",
					NativeID: "test-resource1",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"snarf"}`),
					Managed:    true,
				},
				{
					Label:    "test-resource2",
					NativeID: "test-resource2",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"sandwich"}`),
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			reconcile,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, // '--force' should be false by default
			"test-client-id")
		assert.Error(t, err)

		var rejectedErr apimodel.FormaReconcileRejectedError
		if !errors.As(err, &rejectedErr) {
			t.Fatalf("Expected a ConflctingResourcesError, got: %T", err)
		}

		assert.Contains(t, rejectedErr.ModifiedStacks, "test-stack1")
		assert.Len(t, rejectedErr.ModifiedStacks["test-stack1"].ModifiedResources, 2)
		assert.Contains(t, rejectedErr.ModifiedStacks["test-stack1"].ModifiedResources, apimodel.ResourceModification{
			Stack:     "test-stack1",
			Type:      "FakeAWS::S3::Bucket",
			Label:     "test-resource1",
			Operation: "update",
		})
		assert.Contains(t, rejectedErr.ModifiedStacks["test-stack1"].ModifiedResources, apimodel.ResourceModification{
			Stack:     "test-stack1",
			Type:      "FakeAWS::S3::Bucket",
			Label:     "test-resource2",
			Operation: "update",
		})

		// The command should not have been executed or stored
		commands, err = m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, commands, 3)
	})
}

func TestApplyForma_SoftReconcile_ReturnsEmtpyFormaCommandNoChangesAreDetected(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		resource1Reads, resource2Reads := 0, 0
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Resource.Label,
						ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if request.NativeID == "test-resource1" {
					var properties string
					switch resource1Reads {
					case 0:
						properties = `{"foo":"bar"}`
					case 1:
						properties = `{"foo":"baz"}`
					case 2:
						properties = `{"foo":"bazbaz"}`
					}
					resource1Reads++
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   properties,
					}, nil
				} else if request.NativeID == "test-resource2" {
					var properties string
					switch resource2Reads {
					case 0:
						properties = `{"foo":"bar"}`
					case 1:
						properties = `{"foo":"baz"}`
					}
					resource2Reads++

					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   properties,
					}, nil
				}
				return nil, errors.New("unknown resource")
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Resource.NativeID,
						ResourceProperties: json.RawMessage(`{"foo":"baz"}`),
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

		// initial apply to create resources
		initial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			initial,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

		// patch one of the resources
		patch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:    "test-resource1",
					NativeID: "test-resource1",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"baz"}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			patch,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: false},
			"test-client-id")
		assert.NoError(t, err)

		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			// Check all commands are successful (don't rely on order)
			return len(commands) == 2 && allCommandsSuccessful(commands)
		}, 5*time.Second, 100*time.Millisecond)

		// reconcile the stack with a forma that incorporates the patch (should result in no changes)
		reconcile := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:    "test-resource1",
					NativeID: "test-resource1",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"baz"}`),
				},
				{
					Label:    "test-resource2",
					NativeID: "test-resource2",
					Type:     "FakeAWS::S3::Bucket",
					Stack:    "test-stack1",
					Target:   "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			reconcile,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, // '--force' should be false by default
			"test-client-id")
		assert.NoError(t, err)

		// The command should not have been executed or stored
		commands, err = m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Len(t, commands, 2)
	})
}
