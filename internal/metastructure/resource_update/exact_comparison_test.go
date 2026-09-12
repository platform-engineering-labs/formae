// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestExactComparisonIsOptInAndKeepsExistingRules(t *testing.T) {
	for _, tc := range []struct {
		name, before, after           string
		ordinaryChanged, exactChanged bool
		schema                        pkgmodel.Schema
	}{
		{name: "adjacent-large-integers", before: `{"n":9007199254740992}`, after: `{"n":9007199254740993}`, ordinaryChanged: false, exactChanged: true},
		{name: "exact-decimal-spellings", before: `{"n":9007199254740993}`, after: `{"n":9.007199254740993e15}`, ordinaryChanged: false, exactChanged: false},
		{name: "arrays-remain-unordered", before: `{"n":[9007199254740993,4]}`, after: `{"n":[4,9007199254740993]}`, ordinaryChanged: false, exactChanged: false},
		{name: "empty-tolerance", before: `{"n":[]}`, after: `{}`, ordinaryChanged: false, exactChanged: false},
		{name: "strict-empty-root", before: `{"n":[]}`, after: `{}`, ordinaryChanged: true, exactChanged: true, schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"n": {PreserveEmptyValues: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := pkgmodel.Resource{Properties: []byte(tc.before), Schema: tc.schema}
			after := pkgmodel.Resource{Properties: []byte(tc.after), Schema: tc.schema}
			ordinary, err := CompareFilteredResourceForUpdate(&before, &after, tc.schema, after.Properties)
			require.NoError(t, err)
			require.Equal(t, tc.ordinaryChanged, ordinary)
			exact, err := CompareFilteredResourceForUpdateExactNumbers(&before, &after, tc.schema, after.Properties)
			require.NoError(t, err)
			require.Equal(t, tc.exactChanged, exact)
		})
	}
}

func TestExactComparisonRetainsOpaqueHashing(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"secret": {Opaque: true}}}
	declared := pkgmodel.Resource{Properties: []byte(`{"secret":"same-secret","n":9007199254740993}`), Schema: schema}
	prior, _, err := transformations.NewPersistValueTransformerWithExactNumbers().ApplyToResource(&declared)
	require.NoError(t, err)
	require.NotContains(t, string(prior.Properties), "same-secret")
	changed, err := CompareFilteredResourceForUpdateExactNumbers(prior, &declared, schema, declared.Properties)
	require.NoError(t, err)
	require.False(t, changed, "hash-vs-original secret comparison must remain a no-op")
	declared.Properties = []byte(`{"secret":"changed-secret","n":9007199254740993}`)
	changed, err = CompareFilteredResourceForUpdateExactNumbers(prior, &declared, schema, declared.Properties)
	require.NoError(t, err)
	require.True(t, changed)
}
