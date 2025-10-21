// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package resource_persister

import (
	"context"
	"encoding/json"
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestResourcePersister_StoresResourceUpdate(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	resourceUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"foo":"bar"}`),
			Stack:      "test-stack",
			Ksuid:      util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "aws",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
	}

	// Send PersistResourceUpdate message to the actor
	result := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    resourceUpdate,
	})

	assert.NoError(t, result.Error)
	hash, ok := result.Response.(string)
	assert.True(t, ok)
	assert.NotEmpty(t, hash)

	// Verify the resource was stored in the datastore
	loadedStack, err := ds.LoadStack("test-stack")
	assert.NoError(t, err)
	assert.NotNil(t, loadedStack)
	assert.Equal(t, 1, len(loadedStack.Resources))
	assert.Equal(t, "test-resource", loadedStack.Resources[0].Label)
}

func TestResourcePersister_LoadsResource(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	// First, store a resource
	target := pkgmodel.Target{
		Label:     "test-target",
		Namespace: "aws",
	}
	_, err = ds.StoreTarget(&target)
	assert.NoError(t, err)

	resourceKsuid := util.NewID()
	resourceURI := pkgmodel.NewFormaeURI(resourceKsuid, "")

	resourceUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"foo":"bar"}`),
			Stack:      "test-stack",
			Target:     "test-target",
			Ksuid:      resourceKsuid,
		},
		ResourceTarget: target,
		State:          resource_update.ResourceUpdateStateSuccess,
		StackLabel:     "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
	}

	storeResult := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    resourceUpdate,
	})
	assert.NoError(t, storeResult.Error)

	// Now load the resource via the actor
	loadResult := persister.Call(sender, messages.LoadResource{
		ResourceURI: resourceURI,
	})

	assert.NoError(t, loadResult.Error)
	loaded, ok := loadResult.Response.(messages.LoadResourceResult)
	assert.True(t, ok)
	assert.Equal(t, "test-resource", loaded.Resource.Label)
	assert.Equal(t, "test-target", loaded.Target.Label)
}

func TestResourcePersister_Create(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	resourceUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
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
			},
		},
		GroupID: "test-group-id",
	}

	result := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    resourceUpdate,
	})

	assert.NoError(t, result.Error)
	hash, ok := result.Response.(string)
	assert.True(t, ok)
	assert.NotEmpty(t, hash)

	// Verify the stack was stored correctly
	loadedStack, err := ds.LoadStack("test-stack")
	assert.NoError(t, err)
	assert.NotNil(t, loadedStack)
	assert.Equal(t, "test-stack", loadedStack.SingleStackLabel())
	assert.Equal(t, 1, len(loadedStack.Resources))
	assert.Equal(t, "test-resource", loadedStack.Resources[0].Label)
	assert.JSONEq(t, `{"foo":"bar","baz":"qux","a":[3,4,2]}`, string(loadedStack.Resources[0].Properties))
}

func TestResourcePersister_Update(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	resourceKsuid := util.NewID()
	initialResource := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
			Ksuid:      resourceKsuid,
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "a", "baz"},
			},
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
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
			},
		},
		GroupID: "test-group-id",
	}

	createResult := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    initialResource,
	})
	assert.NoError(t, createResult.Error)
	hash, ok := createResult.Response.(string)
	assert.True(t, ok)
	assert.NotEmpty(t, hash)

	// Now update with different properties
	updateResource := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"foo":"barbar","a":[7,8]}`),
			Stack:      "test-stack",
			Ksuid:      resourceKsuid,
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "a", "baz"},
			},
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationUpdate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id-2",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"barbar","a":[7,8]}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
		GroupID: "test-group-id",
	}

	updateResult := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id-2",
		ResourceOperation: resource_update.OperationUpdate,
		PluginOperation:   resource.OperationUpdate,
		ResourceUpdate:    updateResource,
	})

	assert.NoError(t, updateResult.Error)
	latestHash, ok := updateResult.Response.(string)
	assert.True(t, ok)
	assert.NotEmpty(t, latestHash)

	// Verify properties were updated correctly
	loadedStack, err := ds.LoadStack("test-stack")
	assert.NoError(t, err)
	assert.NotNil(t, loadedStack)
	assert.Equal(t, 1, len(loadedStack.Resources))

	// Convert properties to maps for easier comparison
	var props map[string]interface{}
	err = json.Unmarshal(loadedStack.Resources[0].Properties, &props)
	assert.NoError(t, err)

	// Check updated properties
	assert.Equal(t, "barbar", props["foo"])
	assert.Contains(t, props, "a")
	assert.Equal(t, []interface{}{float64(7), float64(8)}, props["a"])
}

func TestResourcePersister_Delete(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	resource1KSUID := util.NewID()
	resource2KSUID := util.NewID()

	// Create two resources
	createResource1 := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "resource-1",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket1"}`),
			Stack:      "test-stack",
			Ksuid:      resource1KSUID,
			Managed:    true,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id-1",
				NativeID:           "test-native-id-1",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"name":"bucket1"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
		GroupID: "test-group-id-1",
	}

	result1 := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id-1",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    createResource1,
	})
	assert.NoError(t, result1.Error)

	createResource2 := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "resource-2",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket2"}`),
			Stack:      "test-stack",
			Ksuid:      resource2KSUID,
			Managed:    true,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id-2",
				NativeID:           "test-native-id-2",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"name":"bucket2"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
		GroupID: "test-group-id-2",
	}

	result2 := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id-2",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    createResource2,
	})
	assert.NoError(t, result2.Error)

	stack, err := ds.LoadStack("test-stack")
	assert.NoError(t, err)
	assert.Len(t, stack.Resources, 2)

	// Delete resource-2
	deleteResource2 := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "resource-2",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket2"}`),
			Stack:      "test-stack",
			Ksuid:      resource2KSUID,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationDelete,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id-3",
				NativeID:           "test-native-id-2",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"name":"bucket2"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
		GroupID: "test-group-id-2",
	}

	deleteResult := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id-3",
		ResourceOperation: resource_update.OperationDelete,
		PluginOperation:   resource.OperationDelete,
		ResourceUpdate:    deleteResource2,
	})
	assert.NoError(t, deleteResult.Error)

	stack, err = ds.LoadStack("test-stack")
	assert.NoError(t, err)
	assert.Len(t, stack.Resources, 1)
	assert.Equal(t, "resource-1", stack.Resources[0].Label)

	// Delete resource-1
	deleteResource1 := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "resource-1",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket1"}`),
			Stack:      "test-stack",
			Ksuid:      resource1KSUID,
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationDelete,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id-4",
				NativeID:           "test-native-id-1",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"name":"bucket1"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
		GroupID: "test-group-id-1",
	}

	deleteResult2 := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id-4",
		ResourceOperation: resource_update.OperationDelete,
		PluginOperation:   resource.OperationDelete,
		ResourceUpdate:    deleteResource1,
	})
	assert.NoError(t, deleteResult2.Error)

	// Verify the stack was completely removed
	emptyStack, err := ds.LoadStack("test-stack")
	assert.NoError(t, err)
	assert.Nil(t, emptyStack)
}

func TestResourcePersister_MissingRequiredFields(t *testing.T) {
	persister, sender, _, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	resourceKsuid := util.NewID()
	resourceUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "resource-1",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name":"bucket1"}`),
			Stack:      "test-stack",
			Target:     "test-target",
			Ksuid:      resourceKsuid,
			Schema: pkgmodel.Schema{
				Fields: []string{"name", "required_field"},
				Hints: map[string]pkgmodel.FieldHint{
					"name":           {},
					"required_field": {Required: true},
				},
			},
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "test-namespace",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id-1",
				NativeID:           "test-native-id-1",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"name":"bucket1"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
		GroupID: "test-group-id-1",
	}

	result := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    resourceUpdate,
	})

	// Validation should fail, returning empty hash
	assert.NoError(t, result.Error)
	hash, ok := result.Response.(string)
	assert.True(t, ok)
	assert.Empty(t, hash)

	// Verify the resource was not loaded
	loadResult := persister.Call(sender, messages.LoadResource{
		ResourceURI: resourceUpdate.URI(),
	})
	assert.Error(t, loadResult.Error)
}

func TestResourcePersister_IdempotentCreate(t *testing.T) {
	persister, sender, _, err := newResourcePersisterForTest(t)
	assert.NoError(t, err)

	resourceUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:      "test-resource",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"foo":"bar"}`),
			Stack:      "test-stack",
			Ksuid:      util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "test-target",
			Namespace: "aws",
		},
		State:      resource_update.ResourceUpdateStateSuccess,
		StackLabel: "test-stack",
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id",
				NativeID:           "test-native-id",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
			},
		},
	}

	// Store the same resource twice
	result1 := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    resourceUpdate,
	})
	assert.NoError(t, result1.Error)
	hash1, ok := result1.Response.(string)
	assert.True(t, ok)
	assert.NotEmpty(t, hash1)

	result2 := persister.Call(sender, resource_update.PersistResourceUpdate{
		CommandID:         "test-command-id",
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate:    resourceUpdate,
	})
	assert.NoError(t, result2.Error)
	hash2, ok := result2.Response.(string)
	assert.True(t, ok)
	assert.NotEmpty(t, hash2)

	// Hashes should be the same for idempotent operations
	assert.Equal(t, hash1, hash2, "Hashes should be the same for idempotent operations")
}

// newResourcePersisterForTest creates a ResourcePersister actor for testing.
// This follows the same pattern as FormaCommandPersister tests.
func newResourcePersisterForTest(t *testing.T) (*unit.TestActor, gen.PID, datastore.Datastore, error) {
	// Create an in-memory datastore for testing
	ds, err := newTestDatastore()
	if err != nil {
		return nil, gen.PID{}, nil, err
	}

	env := map[gen.Env]any{
		"Datastore": ds,
	}

	sender := gen.PID{Node: "test", ID: 100}

	actor, err := unit.Spawn(t, NewResourcePersister, unit.WithEnv(env))
	if err != nil {
		return nil, gen.PID{}, nil, err
	}

	return actor, sender, ds, nil
}

func newTestDatastore() (datastore.Datastore, error) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite: pkgmodel.SqliteConfig{
			FilePath: ":memory:",
		},
	}

	ds, err := datastore.NewDatastoreSQLite(context.Background(), cfg, "test")
	if err != nil {
		return nil, err
	}

	return ds, nil
}
