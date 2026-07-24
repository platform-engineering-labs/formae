//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissingAgainstReportsUnsetFields(t *testing.T) {
	base, err := parseThemeFile([]byte(`
name = "base"
[palette]
primary_accent = "#111111"
secondary_accent = "#222222"
[glyphs]
op_create = "+"
`))
	require.NoError(t, err)

	cand, err := parseThemeFile([]byte(`
name = "cand"
[palette]
primary_accent = "#999999"
`))
	require.NoError(t, err)

	missing := cand.missingAgainst(base)
	assert.ElementsMatch(t, []string{"palette.secondary_accent", "glyphs.op_create"}, missing)
}

func TestMissingAgainstEmptyWhenComplete(t *testing.T) {
	base, err := parseThemeFile([]byte(`
name = "base"
[palette]
primary_accent = "#111111"
`))
	require.NoError(t, err)

	cand, err := parseThemeFile([]byte(`
name = "cand"
[palette]
primary_accent = "#999999"
`))
	require.NoError(t, err)

	assert.Empty(t, cand.missingAgainst(base))
}

// TestMissingAgainstComparesPresenceNotValue guards that missingAgainst never
// leaks base's values into the decision: a candidate whose values differ
// entirely from base's is still complete as long as every field is set.
func TestMissingAgainstComparesPresenceNotValue(t *testing.T) {
	base := quietRequiredFields()
	f, err := readBuiltin("classic")
	require.NoError(t, err)
	merged, err := resolveExtends(f)
	require.NoError(t, err)

	assert.Empty(t, merged.missingAgainst(base))
	// sanity: classic's values really do differ from quiet's.
	assert.NotEqual(t, base.Palette.Done.Light, merged.Palette.Done.Light)
}
