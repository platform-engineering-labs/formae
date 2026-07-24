// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func versionsWithLabel(vs []datastore.ResourceVersion, label string) []datastore.ResourceVersion {
	var out []datastore.ResourceVersion
	for _, v := range vs {
		if v.Resource.Label == label {
			out = append(out, v)
		}
	}
	return out
}

// RunScrubResourceVersions verifies the two methods the one-time secret backfill
// relies on to scrub plaintext from resource history: LoadAllResourceVersions
// returns every stored version (not just the current one), and
// UpdateResourceVersionData rewrites a specific version's data in place without
// appending a new version.
func RunScrubResourceVersions(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadAllResourceVersionsThenUpdateVersionDataInPlace", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{
			Label:     "test-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		}
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		// Two versions of the same resource: changing the data between stores
		// produces a superseded version plus a current one.
		resource := &pkgmodel.Resource{
			NativeID:   "native-scrub",
			Stack:      "stack-1",
			Type:       "type-1",
			Label:      "label-scrub",
			Target:     "test-target",
			Managed:    true,
			Properties: json.RawMessage(`{"secret":"plaintext-v1"}`),
		}
		_, err = ds.StoreResource(resource, "cmd-1")
		require.NoError(t, err)
		resource.Properties = json.RawMessage(`{"secret":"plaintext-v2"}`)
		_, err = ds.StoreResource(resource, "cmd-2")
		require.NoError(t, err)

		// LoadAllResourceVersions returns BOTH versions, unlike LoadAllResources
		// which returns only the latest.
		versions, err := ds.LoadAllResourceVersions()
		require.NoError(t, err)
		mine := versionsWithLabel(versions, "label-scrub")
		require.Len(t, mine, 2, "both versions must be visible, including the superseded one")

		// Rewrite each version's data in place.
		for _, v := range mine {
			v.Resource.Properties = json.RawMessage(`{"secret":"SCRUBBED"}`)
			require.NoError(t, ds.UpdateResourceVersionData(v.URI, v.Version, v.Resource))
		}

		after, err := ds.LoadAllResourceVersions()
		require.NoError(t, err)
		mineAfter := versionsWithLabel(after, "label-scrub")
		require.Len(t, mineAfter, 2, "in-place update must not append a new version")
		for _, v := range mineAfter {
			assert.JSONEq(t, `{"secret":"SCRUBBED"}`, string(v.Resource.Properties),
				"every version's data must reflect the in-place update")
		}
	})
}
