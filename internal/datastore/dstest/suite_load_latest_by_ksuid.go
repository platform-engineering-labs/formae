// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// latestByKsuidTestResource returns a resource pinned to the given ksuid
// suitable for the LoadLatestResourceByKsuid suite tests.
func latestByKsuidTestResource(ksuid, label string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Ksuid:      ksuid,
		NativeID:   "native-" + label,
		Stack:      "stack-lbk",
		Type:       "AWS::S3::Bucket",
		Label:      label,
		Target:     "target-lbk",
		Managed:    true,
		Properties: json.RawMessage(`{"key":"value"}`),
	}
}

func createLatestByKsuidTarget(t *testing.T, ds interface {
	CreateTarget(*pkgmodel.Target) (string, error)
}) {
	t.Helper()
	_, err := ds.CreateTarget(&pkgmodel.Target{
		Label:     "target-lbk",
		Namespace: "default",
		Config:    json.RawMessage(`{}`),
	})
	require.NoError(t, err)
}

// RunLoadLatestResourceByKsuid_DeletedReturnsNil verifies that a resource whose
// latest version is a delete tombstone returns nil from LoadLatestResourceByKsuid
// rather than exposing the prior live revision.
func RunLoadLatestResourceByKsuid_DeletedReturnsNil(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadLatestResourceByKsuid_DeletedReturnsNil", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		createLatestByKsuidTarget(t, ds)

		ksuid := util.NewID()
		res := latestByKsuidTestResource(ksuid, "bucket-deleted")

		// v1: create
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		// Verify v1 is visible via LoadLatestResourceByKsuid.
		got, err := ds.LoadLatestResourceByKsuid(ksuid)
		require.NoError(t, err)
		require.NotNil(t, got, "resource should be visible after create")
		assert.Equal(t, ksuid, got.Ksuid)

		// v2: delete
		_, err = ds.DeleteResource(res, "cmd-delete")
		require.NoError(t, err)

		// Latest version is now a delete tombstone — must return nil.
		got, err = ds.LoadLatestResourceByKsuid(ksuid)
		require.NoError(t, err)
		assert.Nil(t, got, "deleted resource must return nil from LoadLatestResourceByKsuid")
	})
}

// RunLoadLatestResourceByKsuid_LiveReturnsLatest verifies that a resource with
// multiple live versions (create → update) returns the latest live revision.
func RunLoadLatestResourceByKsuid_LiveReturnsLatest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadLatestResourceByKsuid_LiveReturnsLatest", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		createLatestByKsuidTarget(t, ds)

		ksuid := util.NewID()
		res := latestByKsuidTestResource(ksuid, "bucket-live")

		// v1: create
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		// v2: update (change a property so a new version is stored)
		updated := *res
		updated.Properties = json.RawMessage(`{"key":"updated"}`)
		_, err = ds.StoreResource(&updated, "cmd-update")
		require.NoError(t, err)

		// LoadLatestResourceByKsuid must return v2 (the update).
		got, err := ds.LoadLatestResourceByKsuid(ksuid)
		require.NoError(t, err)
		require.NotNil(t, got, "updated resource must be visible")
		assert.Equal(t, ksuid, got.Ksuid)
	})
}

// RunLoadLatestResourceByKsuid_MissingReturnsNil verifies that a completely
// absent ksuid returns nil without error.
func RunLoadLatestResourceByKsuid_MissingReturnsNil(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadLatestResourceByKsuid_MissingReturnsNil", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		got, err := ds.LoadLatestResourceByKsuid("no-such-ksuid")
		require.NoError(t, err)
		assert.Nil(t, got, "absent ksuid must return nil")
	})
}
