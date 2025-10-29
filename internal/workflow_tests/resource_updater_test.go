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

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestResourceUpdater_RejectsUpdateWhenTheResourceIsOutOfSync(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"bar","baz":"qux","a":[3,4,2,1]}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// start test helper actor to interact with the actors in the metastructure
		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		// store a forma command in the database - USE THE SAME KSUID
		initialResource := successfullyFinishedResourceUpdateCreatingS3Bucket()

		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:    "test-forma-command",
				State: forma_command.CommandStateNotStarted,
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label: "test-resource",
							Type:  "FakeAWS::S3::Bucket",
							Stack: "test-stack",
							Ksuid: initialResource.Resource.Ksuid,
						},
						ExistingResource: pkgmodel.Resource{
							Label: "test-resource",
							Type:  "FakeAWS::S3::Bucket",
							Stack: "test-stack",
							Ksuid: initialResource.Resource.Ksuid,
						},
					},
				},
			},
		})

		// store an S3 bucket
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *initialResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		// start the resource updater
		updateResource := resourceUpdateModifyingS3Bucket(initialResource.Resource.Ksuid)
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: initialResource.Resource.URI(),
			Operation:   string(updateResource.Operation),
			CommandID:   "test-forma-command",
		})
		assert.NoError(t, err)

		// send any update to the resource updater, which should be rejected
		testutil.Send(m.Node, actornames.ResourceUpdater(initialResource.Resource.URI(), string(updateResource.Operation), "test-forma-command"), resource_update.StartResourceUpdate{
			ResourceUpdate: *updateResource,
			CommandID:      "test-forma-command",
		})

		// assert that we receive a message that the resource update was rejected
		testutil.ExpectMessageWithPredicate(t, messages, 5*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == initialResource.Resource.URI() &&
				msg.State == resource_update.ResourceUpdateStateRejected
		})

		// assert that the resource is correctly updated
		stack, err := m.Datastore.LoadStack(initialResource.Resource.Stack)
		assert.NoError(t, err)

		assert.Len(t, stack.Resources, 1)
		assert.JSONEq(t, `{"foo":"bar","baz":"qux","a":[3,4,2,1]}`, string(stack.Resources[0].Properties))

		// assert that the forma command in the database is updated with the rejected state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command",
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateFailed, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateRejected, command.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullySynchronizesAResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"lucky","baz":"robot","a":[4,3,2]}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		initialResource := successfullyFinishedResourceUpdateCreatingS3Bucket()
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *initialResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:      "test-forma-command-sync",
				State:   forma_command.CommandStateNotStarted,
				StartTs: util.TimeNow(),
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label:      "test-resource",
							Type:       "FakeAWS::S3::Bucket",
							Stack:      "test-stack",
							Properties: json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
							Ksuid:      initialResource.Resource.Ksuid,
						},
					},
				},
			},
		})

		// start the resource update
		syncResource := resourceUpdateReadingS3Bucket(initialResource.Resource.Ksuid)
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: initialResource.Resource.URI(),
			CommandID:   "test-forma-command-sync",
			Operation:   string(syncResource.Operation),
		})
		assert.NoError(t, err)

		// send the sync operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(initialResource.Resource.URI(), string(syncResource.Operation), "test-forma-command-sync"), resource_update.StartResourceUpdate{
			ResourceUpdate: *syncResource,
			CommandID:      "test-forma-command-sync",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, messages, 5*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == initialResource.Resource.URI() && msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// assert that the resource is created
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)

		assert.Len(t, stack.Resources, 1)
		assert.Equal(t, "test-resource", stack.Resources[0].Label)
		assert.Equal(t, "FakeAWS::S3::Bucket", stack.Resources[0].Type)
		assert.JSONEq(t, `{"foo":"lucky","baz":"robot","a":[4,3,2]}`, string(stack.Resources[0].Properties))

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-sync",
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullyDeletesAResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {

		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"bar","baz":"qux","a":[3,4,2]}`,
				}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationDelete,
						OperationStatus:    resource.OperationStatusInProgress,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
						Attempts:           1,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationDelete,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
						Attempts:           1,
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

		// start test helper actor to interact with the actors in the metastructure
		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		// store the forma command in the database
		initialResource := successfullyFinishedResourceUpdateCreatingS3Bucket()
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:      "test-forma-command-delete",
				State:   forma_command.CommandStateNotStarted,
				StartTs: util.TimeNow(),
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label: "test-resource",
							Type:  "FakeAWS::S3::Bucket",
							Stack: "test-stack",
							Ksuid: initialResource.Resource.Ksuid,
						},
					},
				},
			},
		})

		// store an S3 bucket resource
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *initialResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		// start the resource updater
		updateResource := resourceUpdateDeletingS3Bucket(initialResource.Resource.Ksuid)
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: initialResource.Resource.URI(),
			CommandID:   "test-forma-command-delete",
			Operation:   string(updateResource.Operation),
		})
		assert.NoError(t, err)

		// send the delete operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(initialResource.Resource.URI(), string(updateResource.Operation), "test-forma-command-delete"), resource_update.StartResourceUpdate{
			ResourceUpdate: *updateResource,
			CommandID:      "test-forma-command-delete",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, messages, 5*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == initialResource.Resource.URI() &&
				msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// assert that the resource is deleted
		stack, err := m.Datastore.LoadStack(initialResource.Resource.Stack)
		assert.NoError(t, err)
		// there was only one resource in the stack, so the stack should be deleted
		assert.Nil(t, stack)

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-delete",
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullyCreatesAResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::EC2::VPC",
					Properties:   `{"VpcId":"vpc-12345678"}`,
				}, nil
			},
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusInProgress,
						RequestID:          "test-request-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: request.Resource.Properties,
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
						Attempts:           1,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"VpcId":"vpc-12345678","baz":"qux","a":[3,4,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
						Attempts:           1,
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

		received := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, received)
		assert.NoError(t, err)

		// ensure the resolve cache is started
		_, err = testutil.Call(m.Node, "ChangesetSupervisor",
			changeset.EnsureResolveCache{
				CommandID: "test-forma-command-create"},
		)
		assert.NoError(t, err)

		vpcKsuid := util.NewID()
		bucketKsuid := util.NewID()

		_, err = testutil.Call(m.Node, "ResourcePersister", target_update.PersistTargetUpdates{
			CommandID: "test-forma-command-create",
			TargetUpdates: []target_update.TargetUpdate{{
				Target: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				Operation: target_update.TargetOperationCreate,
			}},
		})
		assert.NoError(t, err)

		// store the ref resource on the stack
		referencedResource := resource_update.ResourceUpdate{
			Resource: pkgmodel.Resource{
				Label:      "test-vpc",
				Type:       "FakeAWS::EC2::VPC",
				Stack:      "test-stack",
				Properties: json.RawMessage(`{"VpcId":"vpc-12345678"}`),
				Target:     "test-target",
				Managed:    true,
				Ksuid:      vpcKsuid,
			},
			ResourceTarget: pkgmodel.Target{
				Label:     "test-target",
				Namespace: "test-namespace",
			},
			ProgressResult: []resource.ProgressResult{
				{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "vpc-12345678",
					ResourceType:       "FakeAWS::EC2::VPC",
					ResourceProperties: json.RawMessage(`{"VpcId":"vpc-12345678"}`),
				},
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		}
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  referencedResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		// store the forma command in the database
		command := forma_command.FormaCommand{
			ID:      "test-forma-command-create",
			State:   forma_command.CommandStateNotStarted,
			StartTs: util.TimeNow(),
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					Operation: resource_update.OperationCreate,
					Resource: pkgmodel.Resource{
						Label:      "test-resource",
						Type:       "FakeAWS::S3::Bucket",
						Stack:      "test-stack",
						Properties: json.RawMessage(fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId"},"baz":"qux","a":[3,4,2]}`, vpcKsuid)),
						Target:     "test-target",
						Managed:    true,
						Ksuid:      bucketKsuid,
					},
					ResourceTarget: pkgmodel.Target{
						Label:     "test-target",
						Namespace: "test-namespace",
					},
					RemainingResolvables: []pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(vpcKsuid, "VpcId")},
				},
			},
		}
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: command,
		})

		// start the resource update
		newResourceUri := command.ResourceUpdates[0].Resource.URI()
		createResource := resourceUpdateCreatingS3Bucket(bucketKsuid, vpcKsuid)
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: createResource.Resource.URI(),
			CommandID:   "test-forma-command-create",
			Operation:   string(createResource.Operation),
		})
		assert.NoError(t, err)

		// send the create operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(createResource.Resource.URI(), string(createResource.Operation), "test-forma-command-create"), resource_update.StartResourceUpdate{
			ResourceUpdate: *createResource,
			CommandID:      "test-forma-command-create",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, received, 50*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == newResourceUri && msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// load the resources from the stacker
		loadResult, err := testutil.Call(m.Node, "ResourcePersister", messages.LoadResource{
			ResourceURI: newResourceUri,
		})
		assert.NoError(t, err)
		loadedResult, ok := loadResult.(messages.LoadResourceResult)
		newResource := loadedResult.Resource
		assert.True(t, ok)

		assert.Equal(t, "test-resource", newResource.Label)
		assert.Equal(t, "FakeAWS::S3::Bucket", newResource.Type)
		assert.JSONEq(t, fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId", "$value":"vpc-12345678"},"baz":"qux","a":[3,4,2]}`, vpcKsuid), string(newResource.Properties))
		assert.True(t, newResource.Managed)

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-create",
		})
		assert.NoError(t, err)

		loadedCommand, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, loadedCommand.State)
		assert.Len(t, loadedCommand.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, loadedCommand.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullyUpdatesAResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"bar","baz":"qux","a":[3,4,2]}`,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationUpdate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "test-request-id",
						ResourceType:    "FakeAWS::S3::Bucket",
						StartTs:         util.TimeNow(),
						ModifiedTs:      util.TimeNow(),
						Attempts:        1,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
						Attempts:           1,
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

		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		initialResource := successfullyFinishedResourceUpdateCreatingS3Bucket()
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *initialResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:      "test-forma-command-update",
				State:   forma_command.CommandStateNotStarted,
				StartTs: util.TimeNow(),
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label:      "test-resource",
							Type:       "FakeAWS::S3::Bucket",
							Stack:      "test-stack",
							Properties: json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
							Ksuid:      initialResource.Resource.Ksuid,
						},
					},
				},
			},
		})

		// start the resource update
		updateResource := resourceUpdateModifyingS3Bucket(initialResource.Resource.Ksuid)
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: initialResource.Resource.URI(),
			CommandID:   "test-forma-command-update",
			Operation:   string(updateResource.Operation),
		})
		assert.NoError(t, err)

		// send the create operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(initialResource.Resource.URI(), string(updateResource.Operation), "test-forma-command-update"), resource_update.StartResourceUpdate{
			ResourceUpdate: *updateResource,
			CommandID:      "test-forma-command-update",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, messages, 5*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == initialResource.Resource.URI() && msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// assert that the resource is created
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)

		assert.Len(t, stack.Resources, 1)
		assert.Equal(t, "test-resource", stack.Resources[0].Label)
		assert.Equal(t, "FakeAWS::S3::Bucket", stack.Resources[0].Type)
		assert.JSONEq(t, `{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`, string(stack.Resources[0].Properties))

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-update",
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullyRecoversFromADeleteOperationLeftInInProgressState_AndFinishedinTheCloud(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"bar","baz":"qux","a":[3,4,2]}`,
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationDelete,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
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

		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		initialResource := successfullyFinishedResourceUpdateCreatingS3Bucket()
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *initialResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		s, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)
		assert.NotNil(t, s)

		// store the forma command in the database
		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:      "test-forma-command-recover-delete",
				State:   forma_command.CommandStateInProgress,
				StartTs: util.TimeNow(),
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label: "test-resource",
							Type:  "FakeAWS::S3::Bucket",
							Stack: "test-stack",
							Ksuid: initialResource.Resource.Ksuid,
						},
						ProgressResult: []resource.ProgressResult{
							{
								Operation:          resource.OperationDelete,
								OperationStatus:    resource.OperationStatusInProgress,
								RequestID:          "test-request-id",
								NativeID:           "test-native-id",
								ResourceType:       "FakeAWS::S3::Bucket",
								ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
								StartTs:            util.TimeNow(),
								ModifiedTs:         util.TimeNow(),
								Attempts:           1,
								MaxAttempts:        3,
							},
						},
					},
				},
			},
		})

		// start the resource update
		updateResource := partiallyCompletedResourceUpdateDeletingS3Bucket(initialResource.Resource.Ksuid)
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: initialResource.Resource.URI(),
			CommandID:   "test-forma-command-recover-delete",
			Operation:   string(updateResource.Operation),
		})
		assert.NoError(t, err)

		// send the delete operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(initialResource.Resource.URI(), string(updateResource.Operation), "test-forma-command-recover-delete"), resource_update.StartResourceUpdate{
			ResourceUpdate: *updateResource,
			CommandID:      "test-forma-command-recover-delete",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, messages, 5*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == initialResource.Resource.URI() && msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// assert that the resource is created
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)

		assert.Nil(t, stack)

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-recover-delete",
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullyRecoversFromACreateOperationLeftInInProgressState_AndIsStillRunningInTheCloud(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var statusCnt = 0
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"bar","baz":"qux","a":[3,4,2]}`,
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				if statusCnt == 0 {
					statusCnt++
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							Operation:          resource.OperationCreate,
							OperationStatus:    resource.OperationStatusInProgress,
							RequestID:          "test-request-id",
							ResourceType:       "FakeAWS::S3::Bucket",
							ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
							StartTs:            util.TimeNow(),
							ModifiedTs:         util.TimeNow(),
						},
					}, nil
				}
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
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

		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		// store the forma command in the database
		updateResource := partiallyCompletedResourceUpdateCreatingS3Bucket()
		command := forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:      "test-forma-command-recover-create",
				State:   forma_command.CommandStateInProgress,
				StartTs: util.TimeNow(),
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label: "test-resource",
							Type:  "FakeAWS::S3::Bucket",
							Stack: "test-stack",
							Ksuid: updateResource.Resource.Ksuid,
						},
						ProgressResult: []resource.ProgressResult{
							{
								Operation:          resource.OperationCreate,
								OperationStatus:    resource.OperationStatusInProgress,
								RequestID:          "test-request-id",
								NativeID:           "test-native-id",
								ResourceType:       "FakeAWS::S3::Bucket",
								ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
								StartTs:            util.TimeNow(),
								ModifiedTs:         util.TimeNow(),
								Attempts:           1,
								MaxAttempts:        3,
							},
						},
					},
				},
			},
		}
		testutil.Call(m.Node, "FormaCommandPersister", command)

		// start the resource update
		resourceURI := command.Command.ResourceUpdates[0].Resource.URI()
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: resourceURI,
			Operation:   string(updateResource.Operation),
			CommandID:   "test-forma-command-recover-create",
		})
		assert.NoError(t, err)

		// send the create operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(resourceURI, string(updateResource.Operation), "test-forma-command-recover-create"), resource_update.StartResourceUpdate{
			ResourceUpdate: *updateResource,
			CommandID:      "test-forma-command-recover-create",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, messages, 5*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == resourceURI && msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// assert that the resource is created
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)

		assert.Len(t, stack.Resources, 1)
		assert.Equal(t, "test-resource", stack.Resources[0].Label)
		assert.Equal(t, "FakeAWS::S3::Bucket", stack.Resources[0].Type)
		assert.JSONEq(t, `{"foo":"bar","baz":"qux","a":[3,4,2]}`, string(stack.Resources[0].Properties))

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-recover-create",
		})
		assert.NoError(t, err)

		loadFormaCommand, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, loadFormaCommand.State)
		assert.Len(t, loadFormaCommand.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, loadFormaCommand.ResourceUpdates[0].State)
	})
}

func TestResourceUpdater_SuccessfullyRecoversFromAnUpdateOperationLeftInInFailedState_WithAttemptsRemaining(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var statusCnt = 0
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"foo":"bar","baz":"qux","a":[3,4,2]}`,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationUpdate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "test-request-id",
						NativeID:        "test-native-id",
						ResourceType:    "FakeAWS::S3::Bucket",
						StartTs:         util.TimeNow(),
						ModifiedTs:      util.TimeNow(),
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				if statusCnt == 0 {
					statusCnt++
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							Operation:          resource.OperationUpdate,
							OperationStatus:    resource.OperationStatusFailure,
							ErrorCode:          resource.OperationErrorCodeNetworkFailure,
							RequestID:          "test-request-id",
							NativeID:           "test-native-id",
							ResourceType:       "FakeAWS::S3::Bucket",
							ResourceProperties: json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
							StartTs:            util.TimeNow(),
							ModifiedTs:         util.TimeNow(),
						},
					}, nil
				}
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "test-request-id",
						NativeID:           "test-native-id",
						ResourceType:       "FakeAWS::S3::Bucket",
						ResourceProperties: json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
						StartTs:            util.TimeNow(),
						ModifiedTs:         util.TimeNow(),
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

		messages := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, messages)
		assert.NoError(t, err)

		initialResource := successfullyFinishedResourceUpdateCreatingS3Bucket()
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *initialResource,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		s, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)
		assert.NotNil(t, s)

		// store the forma command in the database
		updateResource := partiallyCompletedResourceUpdateModifyingS3Bucket()
		updateResource.Resource.Ksuid = initialResource.Resource.Ksuid

		testutil.Call(m.Node, "FormaCommandPersister", forma_persister.StoreNewFormaCommand{
			Command: forma_command.FormaCommand{
				ID:      "test-forma-command-recover-update",
				State:   forma_command.CommandStateInProgress,
				StartTs: util.TimeNow(),
				ResourceUpdates: []resource_update.ResourceUpdate{
					{
						Operation: resource_update.OperationUpdate,
						Resource: pkgmodel.Resource{
							Label: "test-resource",
							Type:  "FakeAWS::S3::Bucket",
							Stack: "test-stack",
							Ksuid: updateResource.Resource.Ksuid,
						},
						ProgressResult: []resource.ProgressResult{
							{
								Operation:          resource.OperationUpdate,
								OperationStatus:    resource.OperationStatusInProgress,
								RequestID:          "test-request-id",
								NativeID:           "test-native-id",
								ResourceType:       "FakeAWS::S3::Bucket",
								ResourceProperties: json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
								StartTs:            util.TimeNow(),
								ModifiedTs:         util.TimeNow(),
								Attempts:           1,
								MaxAttempts:        3,
							},
						},
					},
				},
			},
		})

		// start the resource update
		_, err = testutil.Call(m.Node, "ResourceUpdaterSupervisor", resource_update.EnsureResourceUpdater{
			ResourceURI: initialResource.Resource.URI(),
			Operation:   string(updateResource.Operation),
			CommandID:   "test-forma-command-recover-update",
		})
		assert.NoError(t, err)

		// send the update operation to the resource updater
		testutil.Send(m.Node, actornames.ResourceUpdater(initialResource.Resource.URI(), string(updateResource.Operation), "test-forma-command-recover-update"), resource_update.StartResourceUpdate{
			ResourceUpdate: *updateResource,
			CommandID:      "test-forma-command-recover-update",
		})

		// assert that the resource updater has sent a resource updated finished message with the state success
		testutil.ExpectMessageWithPredicate(t, messages, 500*time.Second, func(msg resource_update.ResourceUpdateFinished) bool {
			return msg.Uri == initialResource.Resource.URI() && msg.State == resource_update.ResourceUpdateStateSuccess
		})

		// assert that the resource is created
		stack, err := m.Datastore.LoadStack("test-stack")
		assert.NoError(t, err)

		assert.Len(t, stack.Resources, 1)
		assert.Equal(t, "test-resource", stack.Resources[0].Label)
		assert.Equal(t, "FakeAWS::S3::Bucket", stack.Resources[0].Type)
		assert.JSONEq(t, `{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`, string(stack.Resources[0].Properties))

		// assert that the forma command in the database is updated with the success state
		commandRes, err := testutil.Call(m.Node, "FormaCommandPersister", forma_persister.LoadFormaCommand{
			CommandID: "test-forma-command-recover-update",
		})
		assert.NoError(t, err)

		command, ok := commandRes.(*forma_command.FormaCommand)
		assert.True(t, ok)
		assert.Equal(t, forma_command.CommandStateSuccess, command.State)
		assert.Len(t, command.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, command.ResourceUpdates[0].State)

		assert.Equal(t, 2, command.ResourceUpdates[0].MostRecentProgressResult.Attempts)
		assert.Equal(t, 2, command.ResourceUpdates[0].ProgressResult[0].Attempts)

		assert.Equal(t, 3, command.ResourceUpdates[0].MostRecentProgressResult.MaxAttempts)
		assert.Equal(t, 3, command.ResourceUpdates[0].ProgressResult[0].MaxAttempts)

	})
}

func successfullyFinishedResourceUpdateCreatingS3Bucket() *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateSuccess,
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
				MaxAttempts:        3,
			},
		},
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func resourceUpdateModifyingS3Bucket(ksuid string) *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties:    json.RawMessage(`{"foo":"bar","baz":"qux","a":[4,3,2]}`),
			PatchDocument: json.RawMessage(`[{"op":"replace","path":"/foo", "value":"snarf"}, {"op":"replace","path":"/baz", "value":"sandwich"}]`),
			Stack:         "test-stack",
			Ksuid:         ksuid,
		},
		ExistingResource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties:    json.RawMessage(`{"foo":"bar","baz":"qux","a":[4,3,2]}`),
			PatchDocument: json.RawMessage(`[{"op":"replace","path":"/foo", "value":"snarf"}, {"op":"replace","path":"/baz", "value":"sandwich"}]`),
			Stack:         "test-stack",
			NativeID:      "test-native-id-1",
			Ksuid:         ksuid,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation:            resource_update.OperationUpdate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func resourceUpdateDeletingS3Bucket(ksuid string) *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      ksuid,
		},
		ExistingResource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      ksuid,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation:            resource_update.OperationDelete,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func resourceUpdateReadingS3Bucket(ksuid string) *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      ksuid,
		},
		ExistingResource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      ksuid,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation:            resource_update.OperationRead,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func resourceUpdateCreatingS3Bucket(bucketKsuid, vpcKsuid string) *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId"},"baz":"qux","a":[3,4,2]}`, vpcKsuid)),
			Stack:      "test-stack",
			Target:     "test-target",
			Managed:    true,
			Ksuid:      bucketKsuid,
		},
		RemainingResolvables: []pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(vpcKsuid, "VpcId")},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation:      resource_update.OperationCreate,
		State:          resource_update.ResourceUpdateStateNotStarted,
		ProgressResult: []resource.ProgressResult{},
		StackLabel:     "test-stack",
		GroupID:        "test-group-id",
	}
}

func partiallyCompletedResourceUpdateDeletingS3Bucket(ksuid string) *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      ksuid,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation: resource_update.OperationDelete,
		State:     resource_update.ResourceUpdateStateInProgress,
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationDelete,
				OperationStatus:    resource.OperationStatusInProgress,
				RequestID:          "test-request-id",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
				MaxAttempts:        3,
			},
		},
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func partiallyCompletedResourceUpdateCreatingS3Bucket() *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateInProgress,
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusInProgress,
				RequestID:          "test-request-id",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
				MaxAttempts:        3,
			},
		},
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func partiallyCompletedResourceUpdateModifyingS3Bucket() *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties:    json.RawMessage(`{"foo":"snarf","baz":"sandwich","a":[4,3,2]}`),
			PatchDocument: json.RawMessage(`[{"op":"replace","path":"/foo", "value":"snarf"}, {"op":"replace","path":"/baz", "value":"sandwich"}]`),
			Stack:         "test-stack",
			Ksuid:         util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		Operation: resource_update.OperationUpdate,
		State:     resource_update.ResourceUpdateStateInProgress,
		ProgressResult: []resource.ProgressResult{
			{
				Operation:       resource.OperationUpdate,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeThrottling,
				RequestID:       "test-request-id",
				NativeID:        "test-native-id",
				ResourceType:    "FakeAWS::S3::Bucket",
				StartTs:         util.TimeNow(),
				ModifiedTs:      util.TimeNow(),
				Attempts:        1,
				MaxAttempts:     3,
			},
		},
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}
