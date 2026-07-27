// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package theme

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// omarchyThemeSources lists the built-in themes generated from a vendored
// Omarchy palette (palettes/<name>.colors.toml) plus attribution. Their glyphs,
// spinner, progress, and behavior come from the "quiet" base; only the palette
// is Omarchy-derived.
var omarchyThemeSources = []struct{ name, credit string }{
	{"nord", "Nord color scheme (https://www.nordtheme.com)"},
	{"gruvbox", "Gruvbox color scheme by Pavel Pertsev (https://github.com/morhetz/gruvbox)"},
	{"tokyo-night", "Tokyo Night color scheme by enkia (https://github.com/enkia/tokyo-night-vscode-theme)"},
	{"rose-pine", "Rosé Pine color scheme (https://rosepinetheme.com)"},
	{"catppuccin-latte", "Catppuccin Latte color scheme (https://catppuccin.com)"},
}

// resolvedBaseFile returns a built-in's fully resolved themeFile (glyphs,
// progress, spinner, behavior), used as the non-palette scaffold for a
// generated theme.
func resolvedBaseFile(t *testing.T, name string) *themeFile {
	t.Helper()
	f, err := readBuiltin(name)
	require.NoError(t, err)
	m, err := resolveExtends(f)
	require.NoError(t, err)
	return m
}

// generateOmarchyTheme builds the complete-root theme TOML (attribution header +
// body) for one Omarchy source, mapping its vendored palette onto the quiet base.
func generateOmarchyTheme(t *testing.T, name, credit string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("palettes", name+".colors.toml"))
	require.NoError(t, err, "vendored palette for %q", name)
	oc, err := parseOmarchyColors(data)
	require.NoError(t, err)

	base := resolvedBaseFile(t, "quiet")
	overlay := &themeFile{Name: name, Palette: mapOmarchyPalette(oc)}
	merged := mergeThemeFiles(base, overlay)
	merged.Extends = "" // built-in themes are complete, self-contained roots

	var body bytes.Buffer
	require.NoError(t, toml.NewEncoder(&body).Encode(merged))

	header := fmt.Sprintf(`# %s — built-in formae CLI theme (generated; do not edit by hand).
# Palette derived from the Omarchy %q theme, based on the %s.
# Glyphs, spinner, progress, and behavior follow the built-in "quiet" theme.
# Regenerate: FORMAE_UPDATE_THEMES=1 go test -tags=unit -run TestOmarchyThemesUpToDate ./internal/cli/tui/theme

`, name, name, credit)
	return append([]byte(header), body.Bytes()...)
}

// TestOmarchyThemesUpToDate guards that the committed built-in Omarchy themes
// match what the generator produces from the vendored palettes. Set
// FORMAE_UPDATE_THEMES=1 to (re)write them instead of asserting.
func TestOmarchyThemesUpToDate(t *testing.T) {
	update := os.Getenv("FORMAE_UPDATE_THEMES") != ""
	for _, s := range omarchyThemeSources {
		t.Run(s.name, func(t *testing.T) {
			want := generateOmarchyTheme(t, s.name, s.credit)

			// The generated file must re-parse and be complete against quiet.
			reparsed, err := parseThemeFile(want)
			require.NoError(t, err)
			assert.Empty(t, reparsed.missingAgainst(quietRequiredFields()),
				"generated %q is incomplete", s.name)

			path := filepath.Join("themes", s.name+".toml")
			if update {
				require.NoError(t, os.WriteFile(path, want, 0o644))
				t.Logf("wrote %s", path)
				return
			}
			got, err := os.ReadFile(path)
			require.NoError(t, err, "missing built-in theme %s; run with FORMAE_UPDATE_THEMES=1", path)
			assert.Equal(t, string(want), string(got),
				"%s is stale; regenerate with FORMAE_UPDATE_THEMES=1", path)
		})
	}
}
