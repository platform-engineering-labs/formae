// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package querier

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestBlugeQuerier_statusQuery_SimpleOptional(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "status:Success"
	expectedStatusQuery := &datastore.StatusQuery{
		Status: &datastore.QueryItem[string]{
			Item:       string(forma_command.CommandStateSuccess),
			Constraint: datastore.Optional,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_SimpleRequired(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "+status:Success"
	expectedStatusQuery := &datastore.StatusQuery{
		Status: &datastore.QueryItem[string]{
			Item:       string(forma_command.CommandStateSuccess),
			Constraint: datastore.Required,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_SimpleExcluded(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "-status:Success"
	expectedStatusQuery := &datastore.StatusQuery{
		Status: &datastore.QueryItem[string]{
			Item:       string(forma_command.CommandStateSuccess),
			Constraint: datastore.Excluded,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_ComplexOptionalRequiredExcluded(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "command:apply +status:Success -client:1a23b45d"
	expectedStatusQuery := &datastore.StatusQuery{
		Command: &datastore.QueryItem[string]{
			Item:       string(pkgmodel.CommandApply),
			Constraint: datastore.Optional,
		},
		Status: &datastore.QueryItem[string]{
			Item:       string(forma_command.CommandStateSuccess),
			Constraint: datastore.Required,
		},
		ClientID: &datastore.QueryItem[string]{
			Item:       "1a23b45d",
			Constraint: datastore.Excluded,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_SimpleWithMeClientId(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "client:me"
	expectedStatusQuery := &datastore.StatusQuery{
		ClientID: &datastore.QueryItem[string]{
			Item:       "test-client-id",
			Constraint: datastore.Optional,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_SimpleStack(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "stack:test-stack"
	expectedStatusQuery := &datastore.StatusQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "test-stack",
			Constraint: datastore.Optional,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_RequiredStack(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "+stack:test-stack"
	expectedStatusQuery := &datastore.StatusQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "test-stack",
			Constraint: datastore.Required,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_ExcludedStack(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "-stack:test-stack"
	expectedStatusQuery := &datastore.StatusQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "test-stack",
			Constraint: datastore.Excluded,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_statusQuery_ComplexStackWithOtherFields(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "stack:test-stack +status:Success -client:1a23b45d"
	expectedStatusQuery := &datastore.StatusQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "test-stack",
			Constraint: datastore.Optional,
		},
		Status: &datastore.QueryItem[string]{
			Item:       string(forma_command.CommandStateSuccess),
			Constraint: datastore.Required,
		},
		ClientID: &datastore.QueryItem[string]{
			Item:       "1a23b45d",
			Constraint: datastore.Excluded,
		},
	}

	statusQuery, err := querier.statusQuery(queryString, "test-client-id", 0)
	assert.NoError(t, err)
	assert.Equal(t, expectedStatusQuery, statusQuery)
}

func TestBlugeQuerier_resourceQuery_ManagedResources(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "managed:true"
	expectedResourceQuery := &datastore.ResourceQuery{
		Managed: &datastore.QueryItem[bool]{
			Item:       true,
			Constraint: datastore.Optional,
		},
	}

	resourceQuery, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expectedResourceQuery, resourceQuery)
}

func TestBlugeQuerier_resourceQuery_UnmanagedResources(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "managed:false"
	expectedResourceQuery := &datastore.ResourceQuery{
		Managed: &datastore.QueryItem[bool]{
			Item:       false,
			Constraint: datastore.Optional,
		},
	}

	resourceQuery, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expectedResourceQuery, resourceQuery)
}

func TestBlugeQuerier_resourceQuery_SimpleStack(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "stack:test"
	expectedResourceQuery := &datastore.ResourceQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "test",
			Constraint: datastore.Optional,
		},
	}

	resourceQuery, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expectedResourceQuery, resourceQuery)
}

func TestBlugeQuerier_resourceQuery_ExcludedStack(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "-stack:test"
	expectedResourceQuery := &datastore.ResourceQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "test",
			Constraint: datastore.Excluded,
		},
	}

	resourceQuery, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expectedResourceQuery, resourceQuery)
}

func newTestCfg() *pkgmodel.DatastoreConfig {
	return &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite: pkgmodel.SqliteConfig{
			FilePath: ":memory:",
		},
	}
}

func newTestDatastoreSQLite() datastore.Datastore {
	ds, _ := datastore.NewDatastoreSQLite(context.Background(), newTestCfg(), "test")
	return ds
}

func TestBlugeQuerier_resourceQuery(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		querier := NewBlugeQuerier(ds)
		stack1 := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "test-stack1",
					Description: "This is a test stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource1",
					Type:       "Stub::S3::Bucket",
					Stack:      "test-stack1",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Managed:    true,
				},
			},
		}

		_, err := ds.StoreResource(&stack1.Resources[0], "test")
		assert.NoError(t, err)

		stack2 := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "test-stack2",
					Description: "This is a test stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource2",
					Type:       "Stub::S3::Bucket",
					Stack:      "test-stack2",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
				},
			},
		}

		_, err = ds.StoreResource(&stack2.Resources[0], "test")
		assert.NoError(t, err)

		queryString := "stack:test-stack1"
		resources, err := querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.NotNil(t, resources)
		assert.Len(t, resources, 1)
		assert.Equal(t, "test-resource1", resources[0].Label)

		queryString = "-stack:test-stack1"
		resources, err = querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.Len(t, resources, 1)
		assert.Equal(t, "test-resource2", resources[0].Label)

		queryString = "label:test-resource1"
		resources, err = querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.Len(t, resources, 1)
		assert.Equal(t, "test-resource1", resources[0].Label)

		queryString = "-label:test-resource1"
		resources, err = querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.Len(t, resources, 1)
		assert.Equal(t, "test-resource2", resources[0].Label)

		queryString = "type:Stub::S3::Bucket"
		resources, err = querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.Len(t, resources, 2)

		queryString = "-type:Stub::S3::Bucket"
		resources, err = querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.Len(t, resources, 0)

		queryString = ""
		resources, err = querier.QueryResources(queryString)
		assert.NoError(t, err)
		assert.Len(t, resources, 2)
	})
}
