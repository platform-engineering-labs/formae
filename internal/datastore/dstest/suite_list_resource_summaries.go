// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedListSummariesFixture seeds a set of resources designed to exercise every
// filter operator and edge-case that ListResourceSummaries must handle:
//
//   - Multiple version rows per URI (only the latest-version row is visible).
//   - A resource whose latest row is a delete (invisible to live queries).
//   - A resource whose latest row is reaped (invisible to live queries); requires
//     MarkResourceReapedForTest — the test skips if the backend does not provide it.
//   - An unmanaged resource (stack == constants.UnmanagedStack).
//   - A resource with an empty NativeID.
//
// Returns the slice of resources that are expected to be visible (i.e. not
// deleted or reaped).
func seedListSummariesFixture(t *testing.T, td TestDatastore) []*pkgmodel.Resource {
	t.Helper()
	ds := td.Datastore

	// Create targets so StoreResource's foreign-key check passes.
	for _, lbl := range []string{"target-a", "target-b"} {
		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     lbl,
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// r0: two version rows — only the latest is visible.
	r0v1 := &pkgmodel.Resource{
		NativeID:   "native-r0",
		Stack:      "stack-a",
		Type:       "AWS::S3::Bucket",
		Label:      "bucket-r0",
		Target:     "target-a",
		Managed:    true,
		Properties: json.RawMessage(`{"k":"v1"}`),
	}
	_, err := ds.StoreResource(r0v1, "cmd-r0-v1")
	require.NoError(t, err)
	// Update — writes a new version row under the same KSUID.
	r0v2 := *r0v1
	r0v2.Properties = json.RawMessage(`{"k":"v2"}`)
	_, err = ds.StoreResource(&r0v2, "cmd-r0-v2")
	require.NoError(t, err)

	// r1: managed, stack-a, type-a, non-empty NativeID.
	r1 := &pkgmodel.Resource{
		NativeID:   "native-r1",
		Stack:      "stack-a",
		Type:       "AWS::EC2::VPC",
		Label:      "vpc-r1",
		Target:     "target-a",
		Managed:    true,
		Properties: json.RawMessage(`{"cidr":"10.0.0.0/16"}`),
	}
	_, err = ds.StoreResource(r1, "cmd-r1")
	require.NoError(t, err)

	// r2: unmanaged, lives on the unmanaged stack.
	r2 := &pkgmodel.Resource{
		NativeID:   "native-r2",
		Stack:      constants.UnmanagedStack,
		Type:       "AWS::IAM::Role",
		Label:      "role-r2",
		Target:     "target-b",
		Managed:    false,
		Properties: json.RawMessage(`{"rn":"my-role"}`),
	}
	_, err = ds.StoreResource(r2, "cmd-r2")
	require.NoError(t, err)

	// r3: empty NativeID.
	r3 := &pkgmodel.Resource{
		NativeID:   "",
		Stack:      "stack-b",
		Type:       "AWS::EC2::Subnet",
		Label:      "subnet-r3",
		Target:     "target-b",
		Managed:    true,
		Properties: json.RawMessage(`{"az":"us-east-1a"}`),
	}
	_, err = ds.StoreResource(r3, "cmd-r3")
	require.NoError(t, err)

	// r4: will be deleted — invisible to live queries.
	r4 := &pkgmodel.Resource{
		NativeID:   "native-r4",
		Stack:      "stack-a",
		Type:       "AWS::S3::Bucket",
		Label:      "bucket-r4-deleted",
		Target:     "target-a",
		Managed:    true,
		Properties: json.RawMessage(`{"k":"v"}`),
	}
	_, err = ds.StoreResource(r4, "cmd-r4-create")
	require.NoError(t, err)
	_, err = ds.DeleteResource(r4, "cmd-r4-delete")
	require.NoError(t, err)

	// r5: will be reaped — invisible to live queries.
	// Skip if the backend does not provide MarkResourceReapedForTest.
	var r5 *pkgmodel.Resource
	if td.MarkResourceReapedForTest != nil {
		r5 = &pkgmodel.Resource{
			NativeID:   "native-r5",
			Stack:      "stack-a",
			Type:       "AWS::EC2::Instance",
			Label:      "instance-r5-reaped",
			Target:     "target-a",
			Managed:    true,
			Properties: json.RawMessage(`{"it":"t3.micro"}`),
		}
		_, err = ds.StoreResource(r5, "cmd-r5-create")
		require.NoError(t, err)
		require.NoError(t, td.MarkResourceReapedForTest(string(r5.URI())))
	}

	// Reload r0 (with Ksuid populated), r1, r2, r3 to build the expected set.
	visible := make([]*pkgmodel.Resource, 0, 4)
	for _, nativeID := range []string{"native-r0", "native-r1", "native-r2"} {
		resourceType := map[string]string{
			"native-r0": "AWS::S3::Bucket",
			"native-r1": "AWS::EC2::VPC",
			"native-r2": "AWS::IAM::Role",
		}[nativeID]
		r, err := ds.LoadResourceByNativeID(nativeID, resourceType)
		require.NoError(t, err)
		require.NotNil(t, r, "expected resource %s to be visible", nativeID)
		visible = append(visible, r)
	}
	// r3 has empty NativeID — find it via QueryResources.
	r3Results, err := ds.QueryResources(&datastore.ResourceQuery{
		Label: &datastore.QueryItem[string]{Item: "subnet-r3", Constraint: datastore.Required},
	})
	require.NoError(t, err)
	require.Len(t, r3Results, 1)
	visible = append(visible, r3Results[0])

	return visible
}

// ksuidSet builds a set of KSUIDs from a slice of ResourceSummary.
func ksuidSet(summaries []pkgmodel.ResourceSummary) map[string]struct{} {
	m := make(map[string]struct{}, len(summaries))
	for _, s := range summaries {
		m[s.Ksuid] = struct{}{}
	}
	return m
}

// ksuidSetFromResources builds a set of KSUIDs from a slice of *Resource.
func ksuidSetFromResources(resources []*pkgmodel.Resource) map[string]struct{} {
	m := make(map[string]struct{}, len(resources))
	for _, r := range resources {
		m[r.Ksuid] = struct{}{}
	}
	return m
}

// RunListResourceSummaries is the primary parity suite for ListResourceSummaries.
// It seeds a fixture with multiple versions, deleted rows, reaped rows, an
// unmanaged row, and a row with empty NativeID, then asserts:
//
//  1. Row-set parity: for every query (each single operator, combinations, and the
//     empty query), ListResourceSummaries returns the same KSUID set as
//     QueryResources.
//  2. Column-vs-blob parity: each summary's Label/Stack/Type/NativeID matches the
//     values the full-blob path (QueryResources) produces for the same KSUID.
//  3. Stable ordering: results are ordered by (type, label).
func RunListResourceSummaries(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ListResourceSummaries", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		seedListSummariesFixture(t, td)

		queries := []struct {
			name  string
			query *datastore.ResourceQuery
		}{
			{
				name:  "empty query (all visible)",
				query: &datastore.ResourceQuery{},
			},
			{
				name: "stack=stack-a",
				query: &datastore.ResourceQuery{
					Stack: &datastore.QueryItem[string]{Item: "stack-a", Constraint: datastore.Required},
				},
			},
			{
				name: "stack=stack-b",
				query: &datastore.ResourceQuery{
					Stack: &datastore.QueryItem[string]{Item: "stack-b", Constraint: datastore.Required},
				},
			},
			{
				name: "stack=unmanaged",
				query: &datastore.ResourceQuery{
					Stack: &datastore.QueryItem[string]{Item: constants.UnmanagedStack, Constraint: datastore.Required},
				},
			},
			{
				name: "type=AWS::S3::Bucket",
				query: &datastore.ResourceQuery{
					Type: &datastore.QueryItem[string]{Item: "AWS::S3::Bucket", Constraint: datastore.Required},
				},
			},
			{
				name: "type=AWS::EC2::VPC",
				query: &datastore.ResourceQuery{
					Type: &datastore.QueryItem[string]{Item: "AWS::EC2::VPC", Constraint: datastore.Required},
				},
			},
			{
				name: "label=bucket-r0",
				query: &datastore.ResourceQuery{
					Label: &datastore.QueryItem[string]{Item: "bucket-r0", Constraint: datastore.Required},
				},
			},
			{
				name: "target=target-a",
				query: &datastore.ResourceQuery{
					Target: &datastore.QueryItem[string]{Item: "target-a", Constraint: datastore.Required},
				},
			},
			{
				name: "target=target-b",
				query: &datastore.ResourceQuery{
					Target: &datastore.QueryItem[string]{Item: "target-b", Constraint: datastore.Required},
				},
			},
			{
				name: "managed=true",
				query: &datastore.ResourceQuery{
					Managed: &datastore.QueryItem[bool]{Item: true, Constraint: datastore.Required},
				},
			},
			{
				name: "managed=false",
				query: &datastore.ResourceQuery{
					Managed: &datastore.QueryItem[bool]{Item: false, Constraint: datastore.Required},
				},
			},
			{
				name: "native_id=native-r1",
				query: &datastore.ResourceQuery{
					NativeID: &datastore.QueryItem[string]{Item: "native-r1", Constraint: datastore.Required},
				},
			},
			{
				name: "combination: stack-a + managed=true",
				query: &datastore.ResourceQuery{
					Stack:   &datastore.QueryItem[string]{Item: "stack-a", Constraint: datastore.Required},
					Managed: &datastore.QueryItem[bool]{Item: true, Constraint: datastore.Required},
				},
			},
			{
				name: "combination: type wildcard AWS::*",
				query: &datastore.ResourceQuery{
					Type: &datastore.QueryItem[string]{Item: "AWS::*", Constraint: datastore.Optional},
				},
			},
			{
				name: "excluded: stack != stack-a",
				query: &datastore.ResourceQuery{
					Stack: &datastore.QueryItem[string]{Item: "stack-a", Constraint: datastore.Excluded},
				},
			},
		}

		for _, tc := range queries {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				fullResources, err := ds.QueryResources(tc.query)
				require.NoError(t, err)

				summaries, err := ds.ListResourceSummaries(tc.query)
				require.NoError(t, err)

				// Row-set parity: same KSUIDs.
				assert.Equal(t,
					ksuidSetFromResources(fullResources),
					ksuidSet(summaries),
					"ListResourceSummaries must return the same KSUID set as QueryResources for query %q", tc.name,
				)

				// Column-vs-blob parity: build a map from KSUID→Resource for the
				// full-blob path, then check each summary's fields.
				byKsuid := make(map[string]*pkgmodel.Resource, len(fullResources))
				for _, r := range fullResources {
					byKsuid[r.Ksuid] = r
				}
				for _, s := range summaries {
					r, ok := byKsuid[s.Ksuid]
					if !assert.True(t, ok, "summary KSUID %s not found in QueryResources result", s.Ksuid) {
						continue
					}
					assert.Equal(t, r.Label, s.Label, "Label mismatch for ksuid %s", s.Ksuid)
					assert.Equal(t, r.Stack, s.Stack, "Stack mismatch for ksuid %s", s.Ksuid)
					assert.Equal(t, r.Type, s.Type, "Type mismatch for ksuid %s", s.Ksuid)
					assert.Equal(t, r.NativeID, s.NativeID, "NativeID mismatch for ksuid %s", s.Ksuid)
				}
			})
		}
	})
}

// RunListResourceSummaries_StableOrdering verifies that results are ordered by
// (type, label) across a range of resources with varied type/label values.
func RunListResourceSummaries_StableOrdering(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ListResourceSummaries_StableOrdering", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "ord-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		// The (type, label) pairs to seed. Deliberately listed in an insertion
		// order that is NOT sorted by (type, label): types are interleaved and
		// labels within a type are out of order, so a query that dropped its
		// ORDER BY would return rows in an order that fails the assertion below.
		type pair struct{ typ, label string }
		insertionOrder := []pair{
			{"AWS::S3::Bucket", "label-c"},
			{"AWS::IAM::Role", "label-b"},
			{"AWS::EC2::Instance", "label-a"},
			{"AWS::S3::Bucket", "label-a"},
			{"AWS::IAM::Role", "label-a"},
			{"AWS::EC2::Instance", "label-c"},
			{"AWS::S3::Bucket", "label-b"},
			{"AWS::IAM::Role", "label-c"},
			{"AWS::EC2::Instance", "label-b"},
		}
		for i, p := range insertionOrder {
			r := &pkgmodel.Resource{
				NativeID:   fmt.Sprintf("native-%d", i),
				Stack:      "ord-stack",
				Type:       p.typ,
				Label:      p.label,
				Target:     "ord-target",
				Managed:    true,
				Properties: json.RawMessage(`{}`),
			}
			_, err := ds.StoreResource(r, "cmd-ord")
			require.NoError(t, err)
		}

		// The statically-known expected order: the same pairs sorted by
		// (type, label). This is derived from the INPUT data, independently of
		// whatever ListResourceSummaries returns, so the assertion can fail if
		// the query drops its ORDER BY.
		wantOrder := []pair{
			{"AWS::EC2::Instance", "label-a"},
			{"AWS::EC2::Instance", "label-b"},
			{"AWS::EC2::Instance", "label-c"},
			{"AWS::IAM::Role", "label-a"},
			{"AWS::IAM::Role", "label-b"},
			{"AWS::IAM::Role", "label-c"},
			{"AWS::S3::Bucket", "label-a"},
			{"AWS::S3::Bucket", "label-b"},
			{"AWS::S3::Bucket", "label-c"},
		}

		summaries, err := ds.ListResourceSummaries(&datastore.ResourceQuery{
			Stack: &datastore.QueryItem[string]{Item: "ord-stack", Constraint: datastore.Required},
		})
		require.NoError(t, err)
		require.Len(t, summaries, len(wantOrder))

		for i, want := range wantOrder {
			assert.Equal(t, want.typ, summaries[i].Type, "ordering mismatch at position %d: type", i)
			assert.Equal(t, want.label, summaries[i].Label, "ordering mismatch at position %d: label", i)
		}
	})
}

// RunListResourceSummaries_EmptyNativeID verifies that a resource stored with an
// empty NativeID is returned correctly by ListResourceSummaries and that its
// NativeID field is the empty string (not null or a placeholder).
func RunListResourceSummaries_EmptyNativeID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ListResourceSummaries_EmptyNativeID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "enid-target",
			Namespace: "default",
			Config:    json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		r := &pkgmodel.Resource{
			NativeID:   "",
			Stack:      "enid-stack",
			Type:       "AWS::EC2::Subnet",
			Label:      "subnet-no-id",
			Target:     "enid-target",
			Managed:    true,
			Properties: json.RawMessage(`{}`),
		}
		_, err = ds.StoreResource(r, "cmd-enid")
		require.NoError(t, err)

		summaries, err := ds.ListResourceSummaries(&datastore.ResourceQuery{
			Stack: &datastore.QueryItem[string]{Item: "enid-stack", Constraint: datastore.Required},
		})
		require.NoError(t, err)
		require.Len(t, summaries, 1)
		assert.Equal(t, "", summaries[0].NativeID, "NativeID should be empty string, not null")
		assert.Equal(t, "subnet-no-id", summaries[0].Label)
	})
}
