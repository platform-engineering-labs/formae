// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reapedTestResource builds a managed resource pinned to a known ksuid so tests
// can address its resources-table row directly through resource.URI().
func reapedTestResource(ksuid, label string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Ksuid:      ksuid,
		NativeID:   "native-" + label,
		Stack:      "reaped-stack",
		Type:       "AWS::S3::Bucket",
		Label:      label,
		Target:     "reaped-target",
		Managed:    true,
		Properties: json.RawMessage(`{"key":"value"}`),
	}
}

func createReapedTestTarget(t *testing.T, ds datastore.Datastore) {
	t.Helper()
	_, err := ds.CreateTarget(&pkgmodel.Target{
		Label:     "reaped-target",
		Namespace: "default",
		Config:    json.RawMessage(`{}`),
	})
	require.NoError(t, err)
}

// RunReapedResourcesInvisibleToLiveQueries verifies that a resource whose
// current (max-version) row carries the reaped tombstone is invisible to every
// live-resource read, yet its row is retained in the table.
func RunReapedResourcesInvisibleToLiveQueries(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ReapedResourcesInvisibleToLiveQueries", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.MarkResourceReapedForTest == nil || td.RawResourceOperationForTest == nil {
			t.Skip("backend does not expose reaped test helpers")
		}

		createReapedTestTarget(t, ds)

		ksuid := util.NewID()
		res := reapedTestResource(ksuid, "bucket-1")
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		uri := string(res.URI())

		// Visible before reaping.
		loaded, err := ds.LoadResourceById(ksuid)
		require.NoError(t, err)
		require.NotNil(t, loaded, "resource should be visible before reaping")

		byStack, err := ds.LoadResourcesByStack("reaped-stack")
		require.NoError(t, err)
		require.Len(t, byStack, 1)

		// Mark the current row reaped.
		require.NoError(t, td.MarkResourceReapedForTest(uri))

		// Invisible to every live read.
		loaded, err = ds.LoadResourceById(ksuid)
		require.NoError(t, err)
		assert.Nil(t, loaded, "reaped resource must be invisible to LoadResourceById")

		byStack, err = ds.LoadResourcesByStack("reaped-stack")
		require.NoError(t, err)
		assert.Empty(t, byStack, "reaped resource must be invisible to LoadResourcesByStack")

		all, err := ds.LoadAllResourcesByStack()
		require.NoError(t, err)
		assert.Empty(t, all["reaped-stack"], "reaped resource must be invisible to LoadAllResourcesByStack")

		queried, err := ds.QueryResources(&datastore.ResourceQuery{
			Stack: &datastore.QueryItem[string]{Item: "reaped-stack", Constraint: datastore.Required},
		})
		require.NoError(t, err)
		assert.Empty(t, queried, "reaped resource must be invisible to QueryResources")

		loadedByURI, err := ds.LoadResource(res.URI())
		require.NoError(t, err)
		assert.Nil(t, loadedByURI, "reaped resource must be invisible to LoadResource")

		count, err := ds.CountResourcesInStack("reaped-stack")
		require.NoError(t, err)
		assert.Equal(t, 0, count, "reaped resource must not be counted in its stack")

		// Retained in the table.
		op, err := td.RawResourceOperationForTest(uri)
		require.NoError(t, err)
		assert.Equal(t, "reaped", op, "reaped row must be retained in the resources table")
	})
}

// RunResourceWriteRejectedWhenTargetReaped verifies that a resource-version
// write is rejected when the resource's current row is a reaped tombstone.
func RunResourceWriteRejectedWhenTargetReaped(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ResourceWriteRejectedWhenTargetReaped", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.MarkResourceReapedForTest == nil {
			t.Skip("backend does not expose MarkResourceReapedForTest")
		}

		createReapedTestTarget(t, ds)

		ksuid := util.NewID()
		res := reapedTestResource(ksuid, "bucket-2")
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		require.NoError(t, td.MarkResourceReapedForTest(string(res.URI())))

		// A subsequent write (with a changed property so no equality
		// short-circuit can mask the guard) must be rejected.
		res.Properties = json.RawMessage(`{"key":"changed"}`)
		_, err = ds.StoreResource(res, "cmd-resurrect")
		require.Error(t, err, "writing to a reaped resource must be rejected")
		assert.True(t, errors.Is(err, datastore.ErrResourceWriteRejected),
			"rejection must be ErrResourceWriteRejected, got: %v", err)
	})
}

// RunResourceWriteRejectedWhenIncarnationChanged verifies that a stale write
// carrying a superseded target incarnation is rejected once the resource's
// current row has advanced to a fresh incarnation (the reaped-then-recovered
// stale-write race).
func RunResourceWriteRejectedWhenIncarnationChanged(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ResourceWriteRejectedWhenIncarnationChanged", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		createReapedTestTarget(t, ds)

		ksuid := util.NewID()
		res := reapedTestResource(ksuid, "bucket-3")

		// The current row is stamped with the fresh incarnation (as a recovery
		// command's write would be).
		_, err := ds.StoreResource(res, "cmd-recovered", "incarnation-fresh")
		require.NoError(t, err)

		// A stale write from the superseded incarnation must be rejected.
		res.Properties = json.RawMessage(`{"key":"stale"}`)
		_, err = ds.StoreResource(res, "cmd-stale", "incarnation-stale")
		require.Error(t, err, "stale-incarnation write must be rejected")
		assert.True(t, errors.Is(err, datastore.ErrResourceWriteRejected),
			"rejection must be ErrResourceWriteRejected, got: %v", err)

		// A write carrying the current incarnation still succeeds.
		res.Properties = json.RawMessage(`{"key":"fresh-update"}`)
		_, err = ds.StoreResource(res, "cmd-fresh", "incarnation-fresh")
		require.NoError(t, err, "write carrying the current incarnation must succeed")
	})
}
