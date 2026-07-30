// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package querier

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

// TestBlugeQuerier_QueryResourceSummaries_ParityWithQueryResources seeds two
// resources and checks that QueryResourceSummaries returns the same ksuid set
// as QueryResources for representative queries: empty, stack:x, type wildcard,
// and a combination. This proves the shared query-string translation.
func TestBlugeQuerier_QueryResourceSummaries_ParityWithQueryResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		q := NewBlugeQuerier(ds)

		resources := []pkgmodel.Resource{
			{Label: "bucket-a", Type: "AWS::S3::Bucket", Stack: "stack-x", Properties: json.RawMessage(`{}`)},
			{Label: "instance-a", Type: "AWS::EC2::Instance", Stack: "stack-y", Properties: json.RawMessage(`{}`)},
		}
		for i := range resources {
			_, err := ds.StoreResource(&resources[i], "cmd-1")
			assert.NoError(t, err)
		}

		cases := []struct {
			name  string
			query string
		}{
			{name: "empty", query: ""},
			{name: "stack:stack-x", query: "stack:stack-x"},
			{name: "type wildcard", query: "type:AWS::S3::*"},
			{name: "stack+type combo", query: "stack:stack-x type:AWS::S3::*"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				fullResources, err := q.QueryResources(tc.query)
				assert.NoError(t, err)

				summaries, err := q.QueryResourceSummaries(tc.query)
				assert.NoError(t, err)

				// Collect ksuid sets for comparison
				fullKsuids := make(map[string]struct{}, len(fullResources))
				for _, r := range fullResources {
					fullKsuids[r.Ksuid] = struct{}{}
				}
				summaryKsuids := make(map[string]struct{}, len(summaries))
				for _, s := range summaries {
					summaryKsuids[s.Ksuid] = struct{}{}
				}

				assert.Equal(t, fullKsuids, summaryKsuids,
					"QueryResourceSummaries ksuid set must equal QueryResources ksuid set for query %q", tc.query)
			})
		}
	})
}

// TestBlugeQuerier_QueryResourceSummaries_NilQueryReturnsEmpty verifies that
// a query string that resolves to a nil ResourceQuery (currently unreachable via
// the current parser but defensive) returns an empty slice, mirroring QueryResources.
func TestBlugeQuerier_QueryResourceSummaries_EmptyReturnsAll(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		ds := newTestDatastoreSQLite()
		q := NewBlugeQuerier(ds)

		res := pkgmodel.Resource{Label: "bucket-a", Type: "AWS::S3::Bucket", Stack: "stack-x", Properties: json.RawMessage(`{}`)}
		_, err := ds.StoreResource(&res, "cmd-1")
		assert.NoError(t, err)

		summaries, err := q.QueryResourceSummaries("")
		assert.NoError(t, err)
		assert.Len(t, summaries, 1)
	})
}
