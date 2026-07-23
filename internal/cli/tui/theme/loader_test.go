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

func TestLoadBuiltinQuiet(t *testing.T) {
	th, ok := loadBuiltin("quiet")
	require.True(t, ok)
	assert.Equal(t, "quiet", th.Name)
	assert.Equal(t, "#2563EB", th.Palette.PrimaryAccent.Light)
	assert.Equal(t, "+", th.Glyphs.OpCreate)
	assert.Equal(t, "brand", th.ConfirmationBar.Color)
}

func TestLoadBuiltinRichInheritsQuiet(t *testing.T) {
	th, ok := loadBuiltin("rich")
	require.True(t, ok)
	assert.Equal(t, "rich", th.Name)
	// overridden
	assert.Equal(t, "#4ADE80", th.Palette.OpCreate.Dark)
	assert.Equal(t, "severity", th.ConfirmationBar.Color)
	// inherited from quiet
	assert.Equal(t, "#2563EB", th.Palette.PrimaryAccent.Light)
	assert.Equal(t, "↻", th.Glyphs.OpReplace)
}

func TestLoadBuiltinUnknown(t *testing.T) {
	_, ok := loadBuiltin("nope")
	assert.False(t, ok)
}

func TestBuiltinNames(t *testing.T) {
	assert.ElementsMatch(t, []string{"quiet", "rich", "colorblind", "classic"}, builtinNames())
}
