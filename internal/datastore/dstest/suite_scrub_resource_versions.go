// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
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

	// LoadResourceVersionsPage is what the backfill actually uses so it never
	// loads the whole resources table into memory. This checks the keyset paging
	// returns every stored version exactly once, in ascending (uri, version)
	// order, across multiple page boundaries — on every dialect.
	t.Run("LoadResourceVersionsPagePagesEveryVersionExactlyOnce", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{Label: "pg-target", Namespace: "default", Config: json.RawMessage(`{}`)}
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		const n = 7
		for i := 0; i < n; i++ {
			r := &pkgmodel.Resource{
				NativeID:   fmt.Sprintf("native-page-%02d", i),
				Stack:      "stack-page",
				Type:       "type-page",
				Label:      fmt.Sprintf("label-page-%02d", i),
				Target:     "pg-target",
				Managed:    true,
				Properties: json.RawMessage(`{"k":"v"}`),
			}
			_, err := ds.StoreResource(r, fmt.Sprintf("cmd-page-%02d", i))
			require.NoError(t, err)
		}

		// Ground truth: every version via the load-all method.
		all, err := ds.LoadAllResourceVersions()
		require.NoError(t, err)
		wantKeys := make(map[string]bool)
		for _, v := range all {
			wantKeys[v.URI+"@"+v.Version] = true
		}
		require.GreaterOrEqual(t, len(wantKeys), n, "expected at least the stored versions")

		// Page with a small limit so paging crosses several boundaries.
		gotKeys := make(map[string]bool)
		afterURI, afterVersion := "", ""
		for {
			page, err := ds.LoadResourceVersionsPage(afterURI, afterVersion, 2)
			require.NoError(t, err)
			if len(page) == 0 {
				break
			}
			require.LessOrEqual(t, len(page), 2, "page must honor the limit")
			for _, v := range page {
				key := v.URI + "@" + v.Version
				require.False(t, gotKeys[key], "version %s returned more than once by paging", key)
				gotKeys[key] = true
				afterURI, afterVersion = v.URI, v.Version
			}
			if len(page) < 2 {
				break
			}
		}
		// The keyset predicate and ORDER BY share the columns' collation, so paging
		// is internally consistent whatever the DB collation is — asserting a
		// specific (Go byte-order) sort here would wrongly fail under a locale
		// collation (KSUIDs are mixed-case). The correctness property is simply
		// that every stored version is returned exactly once.
		require.Equal(t, wantKeys, gotKeys, "paging must return exactly every stored version, once")
	})
}
