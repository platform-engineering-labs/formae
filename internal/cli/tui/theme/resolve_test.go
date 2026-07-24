//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func silent(string) {}

func TestResolveBuiltin(t *testing.T) {
	assert.Equal(t, "rich", resolveWithDir("rich", "", silent).Name)
}

func TestResolveFormaeAlias(t *testing.T) {
	assert.Equal(t, "quiet", resolveWithDir("formae", "", silent).Name)
}

func TestResolveClassicAlias(t *testing.T) {
	assert.Equal(t, "classic", resolveWithDir("classic", "", silent).Name)
}

func TestResolveEmptyDefaultsQuiet(t *testing.T) {
	assert.Equal(t, "quiet", resolveWithDir("", "", silent).Name)
}

func TestResolveUnknownWarnsAndFallsBack(t *testing.T) {
	var warned string
	th := resolveWithDir("nope", "", func(m string) { warned = m })
	assert.Equal(t, "quiet", th.Name)
	assert.Contains(t, warned, "nope")
}

func TestResolveUserDirOverride(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine.toml"), []byte(`
name = "mine"
extends = "quiet"
[palette]
primary_accent = "#ABCDEF"
`), 0o600))
	th := resolveWithDir("mine", dir, silent)
	assert.Equal(t, "mine", th.Name)
	assert.Equal(t, "#ABCDEF", th.Palette.PrimaryAccent.Light)
	// inherited from quiet base
	assert.Equal(t, "+", th.Glyphs.OpCreate)
}

func TestResolveUserDirShadowsBuiltin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rich.toml"), []byte(`
name = "rich"
extends = "quiet"
[palette]
primary_accent = "#000000"
`), 0o600))
	th := resolveWithDir("rich", dir, silent)
	assert.Equal(t, "#000000", th.Palette.PrimaryAccent.Light)
}

// TestResolveUserThemeExtendsNonQuietBuiltin proves the point of making every
// built-in a self-contained root (Change A): a user theme can now extend a
// non-quiet built-in like "rich" and still resolve fully, because rich no
// longer itself extends quiet (which would have made this two levels deep
// and hit the one-level-only rule in resolveExtends).
func TestResolveUserThemeExtendsNonQuietBuiltin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine.toml"), []byte(`
name = "mine"
extends = "rich"
[palette]
primary_accent = "#123456"
`), 0o600))

	var warnings []string
	th := resolveWithDir("mine", dir, func(m string) { warnings = append(warnings, m) })

	assert.Empty(t, warnings)
	assert.Equal(t, "mine", th.Name)
	// the one override applied
	assert.Equal(t, "#123456", th.Palette.PrimaryAccent.Light)
	// rich's op colors came along, proving rich (not quiet) supplied the rest
	assert.Equal(t, "#16A34A", th.Palette.OpCreate.Light)
	assert.Equal(t, "severity", th.ConfirmationBar.Color)
}

// TestResolveIncompleteUserThemeWarnsAndFallsBack is the P2 fix: a user
// theme file with no `extends` that only sets one field must not be used
// as-is (that used to produce blank glyphs/progress/spinner). It must warn,
// explain what's missing, and fall back to a complete theme instead of
// returning a half-populated one.
func TestResolveIncompleteUserThemeWarnsAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine.toml"), []byte(`
name = "mine"
[palette]
primary_accent = "#ABCDEF"
`), 0o600))

	var warnings []string
	th := resolveWithDir("mine", dir, func(m string) { warnings = append(warnings, m) })

	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], "incomplete")
	assert.Contains(t, warnings[0], "mine.toml")
	// "mine" is not a built-in name, so resolution falls all the way through
	// to the step-4 quiet fallback, which warns a second time.
	assert.Contains(t, warnings[1], "unknown cli.theme")

	// The fallback theme must not be blank: quiet's glyphs/progress/spinner
	// must be present, not the empty zero values an incomplete Theme would
	// have produced before this fix.
	assert.Equal(t, "quiet", th.Name)
	assert.NotEmpty(t, th.Glyphs.OpCreate)
	assert.NotEmpty(t, th.Progress.FillDone)
	assert.NotEmpty(t, th.Spinner.Frames)
}

// TestResolveCompleteUserThemeUsedAsIs is the counterpart to the incomplete
// case: a user theme with no extends that DOES set every field quiet has is
// complete and must be used as written, with no warning.
func TestResolveCompleteUserThemeUsedAsIs(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("themes", "quiet.toml"))
	require.NoError(t, err)

	// Reuse quiet.toml verbatim under a different name: it is complete by
	// definition (it's the completeness template itself).
	complete := strings.Replace(string(data), `name = "quiet"`, `name = "mine"`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine.toml"), []byte(complete), 0o600))

	var warnings []string
	th := resolveWithDir("mine", dir, func(m string) { warnings = append(warnings, m) })

	assert.Empty(t, warnings)
	assert.Equal(t, "mine", th.Name)
	assert.Equal(t, "+", th.Glyphs.OpCreate)
}
