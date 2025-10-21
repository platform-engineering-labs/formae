// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update_test

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelForUnmanagedResource_ReturnsNativeIdWhenNoTagKeysAreFound(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	tags := []pkgmodel.Tag{
		{Key: "Environment", Value: "Production"},
		{Key: "Owner", Value: "Alice"},
	}
	tagLabelKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", tags, tagLabelKeys)
	assert.Equal(t, nativeId, label)
}

func TestLabelForUnmanagedResource_ReturnsTagValuesConcatenatedBySeparator(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	tags := []pkgmodel.Tag{
		{Key: "Name", Value: "MyInstance"},
		{Key: "Project", Value: "Alpha"},
		{Key: "Environment", Value: "Production"},
	}
	tagLabelKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", tags, tagLabelKeys)
	assert.Equal(t, "MyInstance-Alpha", label)
}

func TestLabelForUnmanagedResource_HandlesMissingTagValuesGracefully(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	tags := []pkgmodel.Tag{
		{Key: "Name", Value: "MyInstance"},
		{Key: "Environment", Value: "Production"},
	}
	tagLabelKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t)
	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", tags, tagLabelKeys)
	assert.Equal(t, "MyInstance", label)
}

func TestLabelForUnmanagedResource_AppendsIncrementingNumberForDuplicates(t *testing.T) {
	nativeId := "i-1234567890abcdef0"
	tags := []pkgmodel.Tag{
		{Key: "Name", Value: "MyInstance"},
		{Key: "Project", Value: "Alpha"},
	}
	tagLabelKeys := []string{"Name", "Project"}

	l := newResourceLabelerForTest(t, func(ds datastore.Datastore) {
		_, err := ds.StoreResource(&pkgmodel.Resource{Stack: "$unmanaged", Label: "MyInstance-Alpha", Type: "FakeAWS::S3::Bucket"}, "test-command-id")
		assert.NoError(t, err)
	})

	label := l.LabelForUnmanagedResource(nativeId, "FakeAWS::S3::Bucket", tags, tagLabelKeys)
	assert.Equal(t, "MyInstance-Alpha-1", label)
}

func newResourceLabelerForTest(t *testing.T, setup ...func(datastore.Datastore)) *resource_update.ResourceLabeler {
	t.Helper()

	ds, err := datastore.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{
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
