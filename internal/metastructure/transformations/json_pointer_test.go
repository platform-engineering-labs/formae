// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package transformations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeJSONPointer(t *testing.T) {
	tests := map[string]struct {
		pointer  string
		wantRoot bool
		want     []string
	}{
		"root is the whole document":    {"", true, nil},
		"single empty-name property":    {"/", false, []string{""}},
		"one segment":                   {"/settings", false, []string{"settings"}},
		"two segments":                  {"/settings/password", false, []string{"settings", "password"}},
		"index segment":                 {"/webhooks/0/password", false, []string{"webhooks", "0", "password"}},
		"append token":                  {"/webhooks/-", false, []string{"webhooks", "-"}},
		"embedded empty segment":        {"/a//password", false, []string{"a", "", "password"}},
		"trailing empty segment":        {"/a/", false, []string{"a", ""}},
		"escaped slash":                 {"/a~1b", false, []string{"a/b"}},
		"escaped tilde":                 {"/a~0b", false, []string{"a~b"}},
		"escaped tilde before one":      {"/a~01b", false, []string{"a~1b"}},
		"literal dot is one segment":    {"/hmacConfig.secret", false, []string{"hmacConfig.secret"}},
		"dot split across two segments": {"/hmacConfig/secret", false, []string{"hmacConfig", "secret"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := decodeJSONPointer(tc.pointer)
			require.NoError(t, err)
			assert.Equal(t, tc.wantRoot, got.Root)
			assert.Equal(t, tc.want, got.Segments)
		})
	}
}

// Root ("") and a single empty-named property ("/") collapse to the same thing
// under split-and-rejoin, which is why the decoder returns typed segments.
func TestDecodeJSONPointer_RootIsDistinctFromEmptyNameProperty(t *testing.T) {
	root, err := decodeJSONPointer("")
	require.NoError(t, err)
	emptyName, err := decodeJSONPointer("/")
	require.NoError(t, err)

	assert.True(t, root.Root)
	assert.Empty(t, root.Segments)
	assert.False(t, emptyName.Root)
	assert.Equal(t, []string{""}, emptyName.Segments)
}

func TestDecodeJSONPointer_RejectsMalformedPointers(t *testing.T) {
	for _, pointer := range []string{"settings", "settings/password", "/a~2b", "/a~"} {
		t.Run(pointer, func(t *testing.T) {
			_, err := decodeJSONPointer(pointer)
			assert.Error(t, err)
		})
	}
}

func TestCandidatePrefixes(t *testing.T) {
	tests := map[string]struct {
		segments []string
		want     []string // candidate names, in any order
	}{
		"no collection positions": {
			[]string{"settings"},
			[]string{"settings"},
		},
		"root": {
			nil,
			[]string{""},
		},
		// A hint name is index-free, but a genuine numeric OBJECT key would be
		// part of the name — so an index segment is tried both ways.
		"one index is retained or elided": {
			[]string{"webhooks", "0", "password"},
			[]string{"webhooks.0.password", "webhooks.password"},
		},
		"append token counts as a collection position": {
			[]string{"webhooks", "-"},
			[]string{"webhooks.-", "webhooks"},
		},
		// Neither "elide all" nor "retain all" matches accounts.0.webhooks.password:
		// the required reading keeps the first index and drops the second.
		"mixed retain and elide": {
			[]string{"accounts", "0", "webhooks", "1", "password"},
			[]string{
				"accounts.0.webhooks.1.password",
				"accounts.0.webhooks.password",
				"accounts.webhooks.1.password",
				"accounts.webhooks.password",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, bounded := candidatePrefixes(tc.segments)
			assert.False(t, bounded)

			names := make([]string, 0, len(got))
			for _, p := range got {
				names = append(names, p.name())
			}
			assert.ElementsMatch(t, tc.want, names)
		})
	}
}

// Real patch paths carry 0-2 collection positions. Beyond the bound the
// candidate set is capped at the fully-elided reading and the caller is told,
// rather than silently generating 2^k prefixes.
func TestCandidatePrefixes_BoundedByCollectionSegmentCount(t *testing.T) {
	segments := []string{"a", "0", "b", "1", "c", "2", "d", "3", "e", "4", "f", "5", "g"}

	got, bounded := candidatePrefixes(segments)
	assert.True(t, bounded)
	require.Len(t, got, 1)
	assert.Equal(t, "a.b.c.d.e.f.g", got[0].name(), "the fully-elided reading is kept")
}
