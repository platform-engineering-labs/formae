// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profile

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// seedProfiles points the store at a temp config dir holding the given profiles
// (empty .pkl files are enough for listing) with active as the active pointer.
func seedProfiles(t *testing.T, active string, names ...string) {
	t.Helper()
	cfgDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cfgDir, "profiles"), 0o755))
	for _, n := range names {
		require.NoError(t, os.WriteFile(
			filepath.Join(cfgDir, "profiles", n+".pkl"),
			[]byte("amends \"formae:/Config.pkl\"\n"), 0o600))
	}
	if active != "" {
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "active"), []byte(active+"\n"), 0o600))
	}
	t.Setenv("FORMAE_CONFIG_DIR", cfgDir)
}

// TestListCmd_RendersBodyInResolvedTheme verifies that `profile list` renders
// its output in the theme it resolves from config, not a hardcoded default. We
// stub the theme resolver to a non-default theme and assert the rendered body
// matches that theme (and differs from the default), proving the command no
// longer pins theme.New("formae").
func TestListCmd_RendersBodyInResolvedTheme(t *testing.T) {
	seedProfiles(t, "prod", "prod", "staging")

	origTTY := isTerminal
	isTerminal = func(_ io.Writer) bool { return true }
	t.Cleanup(func() { isTerminal = origTTY })

	origApply := applyTheme
	rich := theme.New("rich")
	applyTheme = func(_ *cobra.Command) *theme.Theme { return rich }
	t.Cleanup(func() { applyTheme = origApply })

	var buf bytes.Buffer
	c := newListCmd()
	c.SetOut(&buf)
	c.SetErr(&buf)
	require.NoError(t, c.Execute())

	want := renderProfileList(rich, []string{"prod", "staging"}, "prod")
	assert.Contains(t, buf.String(), want, "list must render in the resolved theme")

	// The default theme renders differently, so the assertion above is meaningful.
	def := renderProfileList(theme.New("formae"), []string{"prod", "staging"}, "prod")
	assert.NotEqual(t, def, want, "rich and default renderings must differ for this test to be meaningful")
	assert.NotContains(t, buf.String(), def, "list must not fall back to the default theme")
}

// TestListCmd_PipedDoesNotResolveTheme verifies that piped `profile list` is a
// pure read: it never resolves the theme (which would evaluate the profile
// config and mutate the plugin-wrapper dir), and emits plain, unstyled output.
func TestListCmd_PipedDoesNotResolveTheme(t *testing.T) {
	seedProfiles(t, "prod", "prod", "staging")

	origTTY := isTerminal
	isTerminal = func(_ io.Writer) bool { return false }
	t.Cleanup(func() { isTerminal = origTTY })

	origApply := applyTheme
	called := false
	applyTheme = func(_ *cobra.Command) *theme.Theme {
		called = true
		return theme.New("formae")
	}
	t.Cleanup(func() { applyTheme = origApply })

	var buf bytes.Buffer
	c := newListCmd()
	c.SetOut(&buf)
	c.SetErr(&buf)
	require.NoError(t, c.Execute())

	assert.False(t, called, "piped list must not resolve the theme (pure read)")
	assert.NotContains(t, buf.String(), "\x1b[", "piped output must have no ANSI escapes")
	assert.Contains(t, buf.String(), "prod", "piped output must still list the profiles")
}
