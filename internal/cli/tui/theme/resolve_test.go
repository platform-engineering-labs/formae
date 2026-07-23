//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"os"
	"path/filepath"
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
