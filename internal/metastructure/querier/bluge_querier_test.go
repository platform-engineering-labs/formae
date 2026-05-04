// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package querier

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
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

func TestBlugeQuerier_resourceQuery_RepeatedFieldIsMultiValue(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "stack:alpha stack:beta"
	expected := &datastore.ResourceQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "alpha",
			ExtraItems: []string{"beta"},
			Constraint: datastore.Optional,
		},
	}

	got, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestBlugeQuerier_resourceQuery_RepeatedExcludedField(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "-stack:alpha -stack:beta"
	expected := &datastore.ResourceQuery{
		Stack: &datastore.QueryItem[string]{
			Item:       "alpha",
			ExtraItems: []string{"beta"},
			Constraint: datastore.Excluded,
		},
	}

	got, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expected, got)
}

// Bluge's query_string library wraps any term containing `*` or `?` in a
// *bluge.WildcardQuery before our walker sees it. The walker has to unwrap
// it back to a plain field/value with the `*` preserved so the SQL renderer
// can translate to LIKE.
func TestBlugeQuerier_resourceQuery_TrailingWildcard(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "label:prod-*"
	expected := &datastore.ResourceQuery{
		Label: &datastore.QueryItem[string]{
			Item:       "prod-*",
			Constraint: datastore.Optional,
		},
	}

	got, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expected, got)
}

// `::` in resource type values needs the QueryResources escape to survive
// Bluge's tokenizer, then come back out alongside the wildcard. Verify the
// full path end-to-end.
func TestBlugeQuerier_QueryResources_TypeWithWildcardSurvivesColonEscape(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		querier := NewBlugeQuerier(ds)

		stack := &pkgmodel.Forma{
			Resources: []pkgmodel.Resource{
				{Label: "bucket-a", Type: "AWS::S3::Bucket", Stack: "test", Properties: json.RawMessage(`{}`)},
				{Label: "instance-a", Type: "AWS::EC2::Instance", Stack: "test", Properties: json.RawMessage(`{}`)},
			},
		}
		for i := range stack.Resources {
			_, err := ds.StoreResource(&stack.Resources[i], "test")
			assert.NoError(t, err)
		}

		results, err := querier.QueryResources("type:AWS::S3::*")
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		if len(results) == 1 {
			assert.Equal(t, "bucket-a", results[0].Label)
		}
	})
}

func TestBlugeQuerier_resourceQuery_LeadingWildcard(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "label:*-prod"
	expected := &datastore.ResourceQuery{
		Label: &datastore.QueryItem[string]{
			Item:       "*-prod",
			Constraint: datastore.Optional,
		},
	}

	got, err := querier.resourceQuery(queryString)
	assert.NoError(t, err)
	assert.Equal(t, expected, got)
}

// `?` (single-char wildcard), middle wildcards, and other patterns aren't
// supported yet — the flag annotations say "coming soon" — so reject them
// with a clear error rather than silently producing a literal-match.
func TestBlugeQuerier_resourceQuery_QuestionMarkRejected(t *testing.T) {
	querier := &BlugeQuerier{}

	_, err := querier.resourceQuery("label:foo?")
	assert.Error(t, err)
}

func TestBlugeQuerier_resourceQuery_MiddleWildcardRejected(t *testing.T) {
	querier := &BlugeQuerier{}

	_, err := querier.resourceQuery("label:foo*bar")
	assert.Error(t, err)
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
	ds, _ := dssqlite.NewDatastoreSQLite(context.Background(), newTestCfg(), "test")
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

func TestBlugeQuerier_QueryResourcesForDestroy_RejectsManagedTrue(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "managed:true"
	_, err := querier.QueryResourcesForDestroy(queryString)

	assert.Error(t, err)
	var invalidQueryErr apimodel.InvalidQueryError
	assert.ErrorAs(t, err, &invalidQueryErr)
	assert.Contains(t, invalidQueryErr.Reason, "managed field cannot be used in destroy queries")
}

func TestBlugeQuerier_QueryResourcesForDestroy_RejectsManagedFalse(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "managed:false"
	_, err := querier.QueryResourcesForDestroy(queryString)

	assert.Error(t, err)
	var invalidQueryErr apimodel.InvalidQueryError
	assert.ErrorAs(t, err, &invalidQueryErr)
	assert.Contains(t, invalidQueryErr.Reason, "managed field cannot be used in destroy queries")
}

func TestBlugeQuerier_QueryResourcesForDestroy_RejectsRequiredManaged(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "+managed:true"
	_, err := querier.QueryResourcesForDestroy(queryString)

	assert.Error(t, err)
	var invalidQueryErr apimodel.InvalidQueryError
	assert.ErrorAs(t, err, &invalidQueryErr)
	assert.Contains(t, invalidQueryErr.Reason, "managed field cannot be used in destroy queries")
}

func TestBlugeQuerier_QueryResourcesForDestroy_RejectsExcludedManaged(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "-managed:true"
	_, err := querier.QueryResourcesForDestroy(queryString)

	assert.Error(t, err)
	var invalidQueryErr apimodel.InvalidQueryError
	assert.ErrorAs(t, err, &invalidQueryErr)
	assert.Contains(t, invalidQueryErr.Reason, "managed field cannot be used in destroy queries")
}

func TestBlugeQuerier_QueryResourcesForDestroy_RejectsManagedInComplexQuery(t *testing.T) {
	querier := &BlugeQuerier{}

	queryString := "stack:test-stack managed:true type:Bucket"
	_, err := querier.QueryResourcesForDestroy(queryString)

	assert.Error(t, err)
	var invalidQueryErr apimodel.InvalidQueryError
	assert.ErrorAs(t, err, &invalidQueryErr)
	assert.Contains(t, invalidQueryErr.Reason, "managed field cannot be used in destroy queries")
}

func TestBlugeQuerier_QueryResourcesForDestroy_AcceptsQueryWithoutManaged(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		querier := NewBlugeQuerier(ds)

		queryString := "stack:test-stack type:Bucket"
		_, err := querier.QueryResourcesForDestroy(queryString)

		assert.NoError(t, err)
	})
}

func TestBlugeQuerier_QueryResourcesForDestroy_AcceptsEmptyQuery(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		querier := NewBlugeQuerier(ds)

		queryString := ""
		_, err := querier.QueryResourcesForDestroy(queryString)

		assert.NoError(t, err)
	})
}

func TestBlugeQuerier_QueryResourcesForDestroy_AcceptsComplexQueryWithoutManaged(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		querier := NewBlugeQuerier(ds)

		queryString := "stack:prod +type:Bucket -label:test"
		_, err := querier.QueryResourcesForDestroy(queryString)

		assert.NoError(t, err)
	})
}
