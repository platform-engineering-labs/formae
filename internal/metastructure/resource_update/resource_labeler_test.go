// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create properties JSON from tags
func tagsToProperties(tags []pkgmodel.Tag) json.RawMessage {
	props := map[string]any{"Tags": tags}
	data, _ := json.Marshal(props)
	return data
}

// Tests for JSONPath-based label extraction (new LabelConfig)

func TestLabelForUnmanagedResource_UsesJSONPathQuery(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"MyInstance"}]}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	assert.Equal(t, "MyInstance", label)
}

func TestLabelForUnmanagedResource_UsesResourceOverride(t *testing.T) {
	nativeId := "arn:aws:iam::123456789012:policy/MyPolicy"
	properties := json.RawMessage(`{"PolicyName":"MyPolicy","Tags":[{"Key":"Name","Value":"ShouldNotUseThis"}]}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
		ResourceOverrides: map[string]string{
			"AWS::IAM::Policy": "$.PolicyName",
		},
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::IAM::Policy", properties, labelConfig, nil)
	assert.Equal(t, "MyPolicy", label)
}

func TestLabelForUnmanagedResource_FallsBackToNativeID_WhenQueryReturnsNoResult(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Environment","Value":"Production"}]}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	assert.Equal(t, nativeId, label)
}

func TestLabelForUnmanagedResource_FallsBackToNativeID_WhenNoLabelConfig(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"MyInstance"}]}`)
	labelConfig := pkgmodel.LabelConfig{} // Empty config

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	assert.Equal(t, nativeId, label)
}

func TestLabelForUnmanagedResource_HandlesEmptyProperties(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	assert.Equal(t, nativeId, label)
}

func TestLabelForUnmanagedResource_HandlesNilProperties(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", nil, labelConfig, nil)
	assert.Equal(t, nativeId, label)
}

func TestLabelForUnmanagedResource_ConcatenatesMultipleQueryResults(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"MyInstance"},{"Key":"Environment","Value":"Production"},{"Key":"Owner","Value":"Alice"}]}`)
	// Query that matches multiple tags using OR condition
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name' || @.Key=='Environment')].Value`,
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	// Should concatenate both matching tag values with separator
	assert.Equal(t, "MyInstance-Production", label)
}

// Tests for legacy tag-based label extraction (backwards compatibility)

func TestLabelForUnmanagedResource_FallsBackToLegacyTagKeys(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := tagsToProperties([]pkgmodel.Tag{
		{Key: "Name", Value: "MyInstance"},
		{Key: "Project", Value: "Alpha"},
	})
	labelConfig := pkgmodel.LabelConfig{} // Empty config, should fall back to legacy
	legacyTagKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", properties, labelConfig, legacyTagKeys)
	assert.Equal(t, "MyInstance-Alpha", label)
}

func TestLabelForUnmanagedResource_ReturnsNativeIdWhenNoTagKeysAreFound(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := tagsToProperties([]pkgmodel.Tag{
		{Key: "Environment", Value: "Production"},
		{Key: "Owner", Value: "Alice"},
	})
	labelConfig := pkgmodel.LabelConfig{}
	legacyTagKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", properties, labelConfig, legacyTagKeys)
	assert.Equal(t, nativeId, label)
}

func TestLabelForUnmanagedResource_HandlesMissingTagValuesGracefully(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := tagsToProperties([]pkgmodel.Tag{
		{Key: "Name", Value: "MyInstance"},
		{Key: "Environment", Value: "Production"},
	})
	labelConfig := pkgmodel.LabelConfig{}
	legacyTagKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", properties, labelConfig, legacyTagKeys)
	assert.Equal(t, "MyInstance", label)
}

// Tests for label uniqueness

func TestLabelForUnmanagedResource_AppendsIncrementingNumberForDuplicates(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"MyInstance"}]}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}

	l := newResourceLabelerForTest(t, func(ds datastore.Datastore) {
		_, err := ds.StoreResource(&pkgmodel.Resource{Stack: "$unmanaged", Label: "MyInstance", Type: "AWS::EC2::Instance"}, "test-command-id")
		assert.NoError(t, err)
	})

	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	assert.Equal(t, "MyInstance-1", label)
}

func TestLabelForUnmanagedResource_IncrementsExistingVersion(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"MyInstance"}]}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}

	l := newResourceLabelerForTest(t, func(ds datastore.Datastore) {
		_, err := ds.StoreResource(&pkgmodel.Resource{Stack: "$unmanaged", Label: "MyInstance-5", Type: "AWS::EC2::Instance"}, "test-command-id")
		assert.NoError(t, err)
	})

	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, nil)
	assert.Equal(t, "MyInstance-6", label)
}

// Tests for LabelConfig priority (JSONPath query takes precedence over legacy tag keys)

func TestLabelForUnmanagedResource_JSONPathQueryTakesPrecedenceOverLegacyTagKeys(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	properties := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"FromJSONPath"},{"Key":"LegacyTag","Value":"FromLegacy"}]}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: `$.Tags[?(@.Key=='Name')].Value`,
	}
	legacyTagKeys := []string{"LegacyTag"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "AWS::EC2::Instance", properties, labelConfig, legacyTagKeys)
	// Should use JSONPath query result, not legacy tag keys
	assert.Equal(t, "FromJSONPath", label)
}

func TestLabelForUnmanagedResource_CommaSeparatedQueryJoinsResults(t *testing.T) {
	nativeId := "canary-defaults/canary-svc"
	properties := json.RawMessage(`{"metadata":{"namespace":"canary-defaults","name":"canary-svc"}}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: "$.metadata.namespace,$.metadata.name",
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "K8S::Core::Service", properties, labelConfig, nil)
	assert.Equal(t, "canary-defaults-canary-svc", label)
}

func TestLabelForUnmanagedResource_CommaSeparatedQuerySingleResult(t *testing.T) {
	nativeId := "canary-clusterrole"
	properties := json.RawMessage(`{"metadata":{"name":"canary-clusterrole"}}`)
	labelConfig := pkgmodel.LabelConfig{
		DefaultQuery: "$.metadata.namespace,$.metadata.name",
	}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "K8S::Rbac::ClusterRole", properties, labelConfig, nil)
	// Cluster-scoped: no namespace, only name
	assert.Equal(t, "canary-clusterrole", label)
}

func newResourceLabelerForTest(t *testing.T, setup ...func(datastore.Datastore)) *resource_update.ResourceLabeler {
	t.Helper()

	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite: pkgmodel.SqliteConfig{
			FilePath: ":memory:",
		},
	}, "test")
	require.NoError(t, err, "Failed to create test datastore")

	for _, fn := range setup {
		fn(ds)
	}

	return resource_update.NewResourceLabeler(ds)
}
