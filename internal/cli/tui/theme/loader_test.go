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
	assert.ElementsMatch(t, []string{"quiet", "rich", "colorblind"}, builtinNames())
}

// TestBuiltinsAreCompleteRoots guards the design decision that every built-in
// theme is a self-contained root: none of them may resolve with a field
// quiet has set but the built-in itself leaves unset. This is what makes it
// safe for a user theme to `extends` any built-in, not just quiet.
func TestBuiltinsAreCompleteRoots(t *testing.T) {
	quiet := quietRequiredFields()
	for _, name := range builtinNames() {
		t.Run(name, func(t *testing.T) {
			f, err := readBuiltin(name)
			require.NoError(t, err)
			merged, err := resolveExtends(f)
			require.NoError(t, err)
			assert.Empty(t, merged.missingAgainst(quiet), "built-in %q is missing fields quiet has set", name)
		})
	}
}

// TestColorblindPreservesTexturesAndHues locks the colorblind-specific
// progress textures and op hues now that colorblind.toml is a self-contained
// root instead of an quiet-extends override. The maintainer is adamant these
// stay exact through the root rewrite.
func TestColorblindPreservesTexturesAndHues(t *testing.T) {
	th, ok := loadBuiltin("colorblind")
	require.True(t, ok)

	assert.Equal(t, "▚", th.Progress.FillInProgress)
	assert.Equal(t, "blink", th.Progress.Animation)
	assert.Equal(t, "█", th.Progress.FillDone)
	assert.Equal(t, "░", th.Progress.FillPending)

	assert.Equal(t, "#0072B2", th.Palette.OpCreate.Light)
	assert.Equal(t, "#56B4E9", th.Palette.OpCreate.Dark)
	assert.Equal(t, "#009E73", th.Palette.OpUpdate.Light)
	assert.Equal(t, "#33C295", th.Palette.OpUpdate.Dark)
	assert.Equal(t, "#D55E00", th.Palette.OpDelete.Light)
	assert.Equal(t, "#E69F55", th.Palette.OpDelete.Dark)
	assert.Equal(t, "#CC79A7", th.Palette.OpReplace.Light)
	assert.Equal(t, "#E6A0C4", th.Palette.OpReplace.Dark)
	assert.Equal(t, "#0072B2", th.Palette.Done.Light)
	assert.Equal(t, "#D55E00", th.Palette.Error.Light)
}
