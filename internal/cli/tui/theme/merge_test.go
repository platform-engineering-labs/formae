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

func TestMergeChildWinsBaseFills(t *testing.T) {
	base, err := parseThemeFile([]byte(`
name = "base"
[palette]
primary_accent = "#111111"
secondary_accent = "#222222"
[glyphs]
op_create = "+"
op_update = "~"
`))
	require.NoError(t, err)

	child, err := parseThemeFile([]byte(`
name = "child"
extends = "base"
[palette]
primary_accent = "#999999"
[glyphs]
op_update = "≈"
`))
	require.NoError(t, err)

	merged := mergeThemeFiles(base, child)
	assert.Equal(t, "child", merged.Name)
	// child override wins
	assert.Equal(t, "#999999", merged.Palette.PrimaryAccent.Light)
	assert.Equal(t, "≈", *merged.Glyphs.OpUpdate)
	// base fills where child is silent
	require.NotNil(t, merged.Palette.SecondaryAccent)
	assert.Equal(t, "#222222", merged.Palette.SecondaryAccent.Light)
	assert.Equal(t, "+", *merged.Glyphs.OpCreate)
}
