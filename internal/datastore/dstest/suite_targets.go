// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func setupQueryTargetsTestData(ds datastore.Datastore, t *testing.T) {
	t.Helper()
	targets := []*pkgmodel.Target{
		{
			Label:        "prod-us-east-1",
			Namespace:    "AWS",
			Discoverable: true,
			Config:       json.RawMessage(`{"Region":"us-east-1","AccountID":"123"}`),
		},
		{
			Label:        "prod-us-west-2",
			Namespace:    "AWS",
			Discoverable: true,
			Config:       json.RawMessage(`{"Region":"us-west-2","AccountID":"123"}`),
		},
		{
			Label:        "dev-us-east-1",
			Namespace:    "AWS",
			Discoverable: false,
			Config:       json.RawMessage(`{"Region":"us-east-1","AccountID":"456"}`),
		},
		{
			Label:        "tailscale-main",
			Namespace:    "TAILSCALE",
			Discoverable: true,
			Config:       json.RawMessage(`{"Tailnet":"example.com"}`),
		},
	}

	for _, target := range targets {
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)
	}
}

func RunQueryTargetsAll(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_All", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		setupQueryTargetsTestData(ds, t)

		got, err := ds.QueryTargets(&datastore.TargetQuery{})
		assert.NoError(t, err)
		assert.Len(t, got, 4)
	})
}

func RunQueryTargetsByNamespace(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_ByNamespace", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		setupQueryTargetsTestData(ds, t)

		got, err := ds.QueryTargets(&datastore.TargetQuery{
			Namespace: &datastore.QueryItem[string]{
				Item:       "AWS",
				Constraint: datastore.Required,
			},
		})
		assert.NoError(t, err)
		assert.Len(t, got, 3)
		for _, target := range got {
			assert.Equal(t, "AWS", target.Namespace)
		}
	})
}

func RunQueryTargetsByDiscoverable(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_ByDiscoverable", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		setupQueryTargetsTestData(ds, t)

		got, err := ds.QueryTargets(&datastore.TargetQuery{
			Discoverable: &datastore.QueryItem[bool]{
				Item:       true,
				Constraint: datastore.Required,
			},
		})
		assert.NoError(t, err)
		assert.Len(t, got, 3)
		for _, target := range got {
			assert.True(t, target.Discoverable)
		}
	})
}

func RunQueryTargetsByLabel(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_ByLabel", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		setupQueryTargetsTestData(ds, t)

		got, err := ds.QueryTargets(&datastore.TargetQuery{
			Label: &datastore.QueryItem[string]{
				Item:       "prod-us-east-1",
				Constraint: datastore.Required,
			},
		})
		assert.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, "prod-us-east-1", got[0].Label)
	})
}

func RunQueryTargetsDiscoverableAWS(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_DiscoverableAWS", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		setupQueryTargetsTestData(ds, t)

		got, err := ds.QueryTargets(&datastore.TargetQuery{
			Namespace: &datastore.QueryItem[string]{
				Item:       "AWS",
				Constraint: datastore.Required,
			},
			Discoverable: &datastore.QueryItem[bool]{
				Item:       true,
				Constraint: datastore.Required,
			},
		})
		assert.NoError(t, err)
		assert.Len(t, got, 2)
		for _, target := range got {
			assert.Equal(t, "AWS", target.Namespace)
			assert.True(t, target.Discoverable)
		}
	})
}

func RunQueryTargetsNonDiscoverable(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_NonDiscoverable", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		setupQueryTargetsTestData(ds, t)

		got, err := ds.QueryTargets(&datastore.TargetQuery{
			Discoverable: &datastore.QueryItem[bool]{
				Item:       false,
				Constraint: datastore.Required,
			},
		})
		assert.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, "dev-us-east-1", got[0].Label)
		assert.False(t, got[0].Discoverable)
	})
}

func RunQueryTargetsVersioning(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("QueryTargets_Versioning", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{
			Label:        "version-test",
			Namespace:    "AWS",
			Discoverable: false,
			Config:       json.RawMessage(`{"Version":"1"}`),
		}

		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		target.Discoverable = true
		target.Config = json.RawMessage(`{"Version":"2"}`)
		_, err = ds.UpdateTarget(target)
		assert.NoError(t, err)

		results, err := ds.QueryTargets(&datastore.TargetQuery{
			Label: &datastore.QueryItem[string]{
				Item:       "version-test",
				Constraint: datastore.Required,
			},
		})
		assert.NoError(t, err)
		assert.Len(t, results, 1)
		assert.True(t, results[0].Discoverable)
		assert.JSONEq(t, `{"Version":"2"}`, string(results[0].Config))
	})
}

func RunCountResourcesInTarget(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CountResourcesInTarget", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create target
		target := &pkgmodel.Target{
			Label:        "target-count-test",
			Namespace:    "test-namespace",
			Config:       json.RawMessage(`{}`),
			Discoverable: false,
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Initially no resources
		count, err := ds.CountResourcesInTarget("target-count-test")
		assert.NoError(t, err)
		assert.Equal(t, 0, count)

		// Add a resource to the target
		resource := &pkgmodel.Resource{
			Stack:  "default",
			Label:  "test-resource",
			Type:   "AWS::S3::Bucket",
			Target: "target-count-test",
			Ksuid:  mksuid.New().String(),
		}
		_, err = ds.StoreResource(resource, "cmd-1")
		assert.NoError(t, err)

		// Now count should be 1
		count, err = ds.CountResourcesInTarget("target-count-test")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

// RunCountResourcesInTargetUsesByteOrderForVersionComparison pins that
// CountResourcesInTarget's "latest version" subquery compares version strings
// in byte order, not under a case-insensitive collation. Two rows with the
// same uri but versions differing only in case ('MMM...' vs 'mmm...') compare
// equal under CI collation, so a faulty implementation would treat both as
// "latest" and miss the delete tombstone on the byte-greater one.
func RunCountResourcesInTargetUsesByteOrderForVersionComparison(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CountResourcesInTarget_UsesByteOrderForVersionComparison", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawInsertResource == nil {
			t.Skip("backend does not expose RawInsertResource")
		}

		target := &pkgmodel.Target{
			Label:     "target-collation",
			Namespace: "test",
			Config:    json.RawMessage(`{}`),
		}
		if _, err := td.CreateTarget(target); err != nil {
			t.Fatalf("CreateTarget: %v", err)
		}

		// Same uri, two versions differing only in case. The byte-greater
		// (lowercase) row is the delete tombstone — so the correct count is 0.
		uri := "test://collation"
		if err := td.RawInsertResource(uri, "MMMMMMMMMMMMMMMMMMMMMMMMMMMM", "target-collation", "create"); err != nil {
			t.Fatalf("seed create: %v", err)
		}
		if err := td.RawInsertResource(uri, "mmmmmmmmmmmmmmmmmmmmmmmmmmmm", "target-collation", "delete"); err != nil {
			t.Fatalf("seed delete: %v", err)
		}

		count, err := td.CountResourcesInTarget("target-collation")
		assert.NoError(t, err)
		assert.Equal(t, 0, count,
			"latest version is the delete tombstone (byte order: lowercase > uppercase); "+
				"a case-insensitive comparison would treat the rows as equal and miss the delete")
	})
}

func RunDeleteTargetSuccess(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteTarget_Success", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create target
		target := &pkgmodel.Target{
			Label:        "delete-target-test",
			Namespace:    "test-namespace",
			Config:       json.RawMessage(`{}`),
			Discoverable: false,
		}
		_, err := ds.CreateTarget(target)
		assert.NoError(t, err)

		// Verify target exists
		loaded, err := ds.LoadTarget("delete-target-test")
		assert.NoError(t, err)
		assert.NotNil(t, loaded)

		// Delete target
		version, err := ds.DeleteTarget("delete-target-test")
		assert.NoError(t, err)
		assert.Equal(t, "delete-target-test_deleted", version)

		// Verify target no longer exists
		loaded, err = ds.LoadTarget("delete-target-test")
		assert.NoError(t, err)
		assert.Nil(t, loaded)
	})
}

func RunUpdateTargetNotFoundReturnsError(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTarget_NotFound_ReturnsError", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.UpdateTarget(&pkgmodel.Target{
			Label:     "non-existent-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})
}

func RunDeleteTargetNotFound(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteTarget_NotFound", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Try to delete non-existent target
		_, err := ds.DeleteTarget("non-existent-target")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})
}
