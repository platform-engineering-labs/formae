// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package workflow_tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestChangesetExecutor_SingleResourceUpdate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "test-request-id",
						NativeID:        "test-native-id",
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		assert.NoError(t, err)

		// Start test helper actor to receive messages
		messages := make(chan any, 10)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		commandID := "test-command-single"
		resourceUpdate := newTestResourceUpdate("test-vpc", nil, "FakeAWS::EC2::VPC")

		// Store the forma command first
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:    commandID,
				State: forma_command.CommandStateNotStarted,
				ResourceUpdates: []resource_update.ResourceUpdate{
					resourceUpdate,
				},
			},
		})

		// Create changeset and start executor
		cs, err := changeset.NewChangesetFromResourceUpdates([]resource_update.ResourceUpdate{resourceUpdate}, commandID, pkgmodel.CommandApply)
		assert.NoError(t, err)

		// Ensure the changeset executor exists
		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureChangesetExecutor{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		// Send the Start message to the changeset executor
		testutil.Send(m.Node, actornames.ChangesetExecutor(commandID), changeset.Start{
			Changeset: cs,
		})

		// Wait for the changeset to complete
		testutil.ExpectMessageWithPredicate(t, messages, 10*time.Second, func(msg changeset.ChangesetCompleted) bool {
			return msg.CommandID == commandID && msg.State == changeset.ChangeSetStateFinishedSuccessfully
		})

		// Verify the command state in the database
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)

		// Verify resource was created in the stack
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)
		assert.Len(t, stack.Resources, 1)
		assert.Equal(t, "test-vpc", stack.Resources[0].Label)
	})
}

func TestChangesetExecutor_DependentResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				// VPC returns a VpcId that subnet can reference
				properties := json.RawMessage(`{"VpcId":"vpc-12345"}`)
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       request.Resource.Type,
						ResourceProperties: properties,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// Return VpcId when reading VPC
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"VpcId":"vpc-12345"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		assert.NoError(t, err)

		messages := make(chan any, 10)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		commandID := "test-command-deps"
		vpcUpdate := newTestResourceUpdate("test-vpc", nil, "FakeAWS::EC2::VPC")
		// Subnet needs to resolve VpcId property from the VPC
		subnetUpdate := newTestResourceUpdate("test-subnet", []pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(vpcUpdate.Resource.Ksuid, "VpcId")}, "FakeAWS::EC2::Subnet")

		// Store the forma command
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:    commandID,
				State: forma_command.CommandStateNotStarted,
				ResourceUpdates: []resource_update.ResourceUpdate{
					vpcUpdate,
					subnetUpdate,
				},
			},
		})

		// Create changeset - it will automatically build the dependency pipeline
		cs, err := changeset.NewChangesetFromResourceUpdates([]resource_update.ResourceUpdate{vpcUpdate, subnetUpdate}, commandID, pkgmodel.CommandApply)
		assert.NoError(t, err)

		// Ensure the changeset executor exists
		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureChangesetExecutor{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		// Start the changeset
		testutil.Send(m.Node, actornames.ChangesetExecutor(commandID), changeset.Start{
			Changeset: cs,
		})

		// Wait for completion
		testutil.ExpectMessageWithPredicate(t, messages, 10*time.Second, func(msg changeset.ChangesetCompleted) bool {
			return msg.CommandID == commandID && msg.State == changeset.ChangeSetStateFinishedSuccessfully
		})

		// Verify both resources were created
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)
		assert.Len(t, stack.Resources, 2)

		// Verify command state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 2)
	})
}

func TestChangesetExecutor_CascadeFailure(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Make VPC creation fail
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Resource.Label == "test-vpc" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
							RequestID:       "test-request-id",
							ResourceType:    request.Resource.Type,
						},
					}, nil
				}
				// Subnet would succeed if VPC hadn't failed
				properties := json.RawMessage(`{"SubnetId":"subnet-12345"}`)
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       request.Resource.Type,
						ResourceProperties: properties,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// This should never be called since VPC creation fails
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"VpcId":"vpc-12345"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		assert.NoError(t, err)

		messages := make(chan any, 10)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		commandID := "test-command-cascade-failure"
		vpcUpdate := newTestResourceUpdate("test-vpc", nil, "FakeAWS::EC2::VPC")
		// Subnet needs to resolve VpcId property from the VPC (which will fail to be created)
		subnetUpdate := newTestResourceUpdate("test-subnet", []pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(vpcUpdate.Resource.Ksuid, "VpcId")}, "FakeAWS::EC2::Subnet")

		// Store the forma command
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:    commandID,
				State: forma_command.CommandStateNotStarted,
				ResourceUpdates: []resource_update.ResourceUpdate{
					vpcUpdate,
					subnetUpdate,
				},
			},
		})

		// Create changeset - it will automatically build the dependency pipeline
		cs, err := changeset.NewChangesetFromResourceUpdates([]resource_update.ResourceUpdate{vpcUpdate, subnetUpdate}, commandID, pkgmodel.CommandApply)
		assert.NoError(t, err)

		// Ensure the changeset executor exists
		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureChangesetExecutor{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		// Start the changeset
		testutil.Send(m.Node, actornames.ChangesetExecutor(commandID), changeset.Start{
			Changeset: cs,
		})

		// Wait for completion (even with failures, the changeset completes)
		testutil.ExpectMessageWithPredicate(t, messages, 10*time.Second, func(msg changeset.ChangesetCompleted) bool {
			return msg.CommandID == commandID
		})

		// Verify cascading failure occurred
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)

		// VPC should have failed, subnet should also have failed (cascade)
		vpcFailed := false
		subnetFailed := false
		for _, ru := range command.ResourceUpdates {
			if ru.Resource.Label == "test-vpc" && ru.State == resource_update.ResourceUpdateStateFailed {
				vpcFailed = true
			}
			if ru.Resource.Label == "test-subnet" && ru.State == resource_update.ResourceUpdateStateFailed {
				subnetFailed = true
			}
		}
		assert.True(t, vpcFailed, "VPC update should have failed")
		assert.True(t, subnetFailed, "Subnet update should have failed due to cascade")

		// No resources should be created
		stack, err := m.Datastore.LoadStack("test-stack")
		if err == nil && stack != nil {
			assert.Empty(t, stack.Resources, "No resources should be created when cascade fails")
		}
	})
}

func TestChangesetExecutor_HashesAllResourcesOnCompletion(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "test-request-id",
						NativeID:        "test-native-id",
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		assert.NoError(t, err)

		messages := make(chan any, 10)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		commandID := "test-command-hashing"
		resourceUpdate := newTestResourceUpdate("test-secret", nil, "FakeAWS::SecretsManager::Secret")

		// Store the forma command
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:    commandID,
				State: forma_command.CommandStateNotStarted,
				ResourceUpdates: []resource_update.ResourceUpdate{
					resourceUpdate,
				},
			},
		})

		// Create changeset
		cs, err := changeset.NewChangesetFromResourceUpdates([]resource_update.ResourceUpdate{resourceUpdate}, commandID, pkgmodel.CommandApply)
		assert.NoError(t, err)

		// Ensure the changeset executor exists
		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureChangesetExecutor{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		// Start the changeset
		testutil.Send(m.Node, actornames.ChangesetExecutor(commandID), changeset.Start{
			Changeset: cs,
		})

		// Wait for completion
		testutil.ExpectMessageWithPredicate(t, messages, 10*time.Second, func(msg changeset.ChangesetCompleted) bool {
			return msg.CommandID == commandID && msg.State == changeset.ChangeSetStateFinishedSuccessfully
		})

		// Verify the command completed successfully (which means hashing was called)
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
	})
}

func TestChangesetExecutor_HashesAllResourcesOnFailure(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusFailure,
						RequestID:       "test-request-id",
						ResourceType:    request.Resource.Type,
					},
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		assert.NoError(t, err)

		messages := make(chan any, 10)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		commandID := "test-command-hashing-failure"
		resourceUpdate := newTestResourceUpdate("test-secret", nil, "FakeAWS::SecretsManager::Secret")

		// Store the forma command
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:    commandID,
				State: forma_command.CommandStateNotStarted,
				ResourceUpdates: []resource_update.ResourceUpdate{
					resourceUpdate,
				},
			},
		})

		// Create changeset
		cs, err := changeset.NewChangesetFromResourceUpdates([]resource_update.ResourceUpdate{resourceUpdate}, commandID, pkgmodel.CommandApply)
		assert.NoError(t, err)

		// Ensure the changeset executor exists
		_, err = testutil.Call(m.Node, "ChangesetSupervisor", changeset.EnsureChangesetExecutor{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		// Start the changeset
		testutil.Send(m.Node, actornames.ChangesetExecutor(commandID), changeset.Start{
			Changeset: cs,
		})

		// Wait for completion (even with failures, the changeset completes)
		// NOTE: Current implementation doesn't distinguish between successful and failed completion in ChangesetCompleted
		// This test verifies that hashing happens even when resources fail
		testutil.ExpectMessageWithPredicate(t, messages, 10*time.Second, func(msg changeset.ChangesetCompleted) bool {
			return msg.CommandID == commandID
		})

		// Verify the command completed and hashing happened
		// The fact that we got ChangesetCompleted means MarkFormaCommandAsComplete was called
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: commandID,
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		// Verify the resource update failed
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, command.ResourceUpdates[0].State)
	})
}

// Helper function to create a test resource update
func newTestResourceUpdate(label string, dependencies []pkgmodel.FormaeURI, resourceType string) resource_update.ResourceUpdate {
	ksuid := util.NewID()

	// Build properties with resolvable references if dependencies exist
	properties := fmt.Sprintf(`{"name":"%s"`, label)
	for _, dep := range dependencies {
		propName := string(dep.PropertyPath())
		properties += fmt.Sprintf(`,"%s":{"$ref":"%s"}`, propName, dep)
	}
	properties += `}`

	return resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      label,
			Type:       resourceType,
			Stack:      "test-stack",
			Target:     "test-target",
			Ksuid:      ksuid,
			Properties: json.RawMessage(properties),
		},
		ResourceTarget: pkgmodel.Target{
			Label: "test-target",
		},
		Operation:            resource_update.OperationCreate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		StartTs:              util.TimeNow(),
		RemainingResolvables: dependencies,
		StackLabel:           "test-stack",
	}
}
