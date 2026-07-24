//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const miniTheme = `
name = "mini"
[palette]
primary_accent = "#2563EB"
op_create = { light = "#16A34A", dark = "#4ADE80" }
[glyphs]
op_create = "+"
[progress]
fill_done = "█"
animation = "pulse"
[spinner]
frames = ["◐", "◓"]
interval_ms = 120
static_frame = "◐"
[confirmation_bar]
color = "severity"
`

func TestParseThemeFile(t *testing.T) {
	f, err := parseThemeFile([]byte(miniTheme))
	require.NoError(t, err)
	assert.Equal(t, "mini", f.Name)
	require.NotNil(t, f.Palette.PrimaryAccent)
	assert.Equal(t, "#2563EB", f.Palette.PrimaryAccent.Light)
	require.NotNil(t, f.Palette.OpCreate)
	assert.Equal(t, "#4ADE80", f.Palette.OpCreate.Dark)
}

// TestSortMarkerNotThemeable guards the removal of the never-read
// glyphs.sort_marker field: a TOML file that still sets it must parse
// without error (an unknown key is silently ignored by the TOML decoder),
// and the Glyphs struct must not expose a SortMarker field for it to land
// in — sort headers hardcode their own glyphs now.
func TestSortMarkerNotThemeable(t *testing.T) {
	f, err := parseThemeFile([]byte(`
name = "mini"
[glyphs]
sort_marker = "▲"
op_create = "+"
`))
	require.NoError(t, err)
	th := f.toTheme()
	assert.Equal(t, "+", th.Glyphs.OpCreate)

	var g Glyphs
	_, ok := reflect.TypeOf(g).FieldByName("SortMarker")
	assert.False(t, ok, "Glyphs must not have a SortMarker field")
}

func TestToTheme(t *testing.T) {
	f, err := parseThemeFile([]byte(miniTheme))
	require.NoError(t, err)
	th := f.toTheme()
	assert.Equal(t, "mini", th.Name)
	assert.Equal(t, "#2563EB", th.Palette.PrimaryAccent.Light)
	assert.Equal(t, "+", th.Glyphs.OpCreate)
	assert.Equal(t, "pulse", th.Progress.Animation)
	assert.Equal(t, []string{"◐", "◓"}, th.Spinner.Frames)
	assert.Equal(t, "severity", th.ConfirmationBar.Color)
}
