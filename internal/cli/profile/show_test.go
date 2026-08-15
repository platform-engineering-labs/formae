//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

const hostedProfilePkl = `amends "formae:/Config.pkl"

cli {
    connection = new Hosted {
        endpoint = "https://cloud.formae.ai"
        installation = "3f2b8c14-0000-4000-8000-000000000000"
        auth = new Dynamic { type = "oidc" }
    }
}
`

// seedProfileBodies points the store at a temp config dir holding profiles
// with the given contents, and makes active the active pointer.
func seedProfileBodies(t *testing.T, active string, bodies map[string]string) {
	t.Helper()
	cfgDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cfgDir, "profiles"), 0o755))
	for name, body := range bodies {
		require.NoError(t, os.WriteFile(
			filepath.Join(cfgDir, "profiles", name+".pkl"), []byte(body), 0o600))
	}
	if active != "" {
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "active"), []byte(active+"\n"), 0o600))
	}
	t.Setenv("FORMAE_CONFIG_DIR", cfgDir)
}

func runShow(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newShowCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
	return out.String()
}

func TestShowMachineJSONEmitsTheTaggedConnection(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "prod", "--output-consumer", "machine", "--output-schema", "json")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.EqualValues(t, 1, got["schemaVersion"])
	conn := got["cli"].(map[string]any)["connection"].(map[string]any)
	assert.Equal(t, "hosted", conn["mode"])
	assert.Equal(t, "3f2b8c14-0000-4000-8000-000000000000", conn["installation"])
	assert.NotContains(t, out, "\x1b[", "machine output carries no ANSI")
}

func TestShowMachineYAMLIsAccepted(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "prod", "--output-consumer", "machine", "--output-schema", "yaml")

	assert.Contains(t, out, "schemaVersion: 1")
	assert.Contains(t, out, "mode: hosted")
}

func TestShowWithNoArgumentUsesTheActiveProfile(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "--output-consumer", "machine", "--output-schema", "json")

	assert.Contains(t, out, `"profile":"prod"`)
}

func TestShowHumanRendersSections(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "prod")

	assert.Contains(t, out, "Connection")
	assert.Contains(t, out, "cloud.formae.ai")
	assert.NotContains(t, out, "schemaVersion", "the schema version is machine bookkeeping")
	assert.NotRegexp(t, `"[a-zA-Z]+":`, out, "human output carries no JSON key-quote pattern")
}

// A secret Pkl resolves from the environment never existed in the profile
// file, so redaction has to act on the resolved value rather than the text.
func TestShowRedactsASecretResolvedFromTheEnvironment(t *testing.T) {
	const profile = `amends "formae:/Config.pkl"

agent {
    datastore {
        datastoreType = "postgres"
        postgres {
            host = "db.internal"
            password = read("env:FORMAE_TEST_DB_PASSWORD")
        }
    }
}
`
	t.Setenv("FORMAE_TEST_DB_PASSWORD", "hunter2")
	seedProfileBodies(t, "prod", map[string]string{"prod": profile})

	out := runShow(t, "prod", "--output-consumer", "machine", "--output-schema", "json")

	assert.NotContains(t, out, "hunter2")
	assert.Contains(t, out, "db.internal")
}

func TestShowRejectsUnknownProfile(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	cmd := newShowCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"nope"})
	err := cmd.Execute()

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "nope"), "the error names the profile asked for")
}

// The theme follows the active profile rather than the one being displayed:
// it is the user's environment, the same as for every other command in this
// group. Showing a profile must not adopt that profile's theme.
func TestShowRendersInTheActiveProfilesTheme(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{
		"prod": hostedProfilePkl,
		"demo": hostedProfilePkl,
	})

	origTTY := isTerminal
	isTerminal = func(_ io.Writer) bool { return true }
	t.Cleanup(func() { isTerminal = origTTY })

	// The built-in palettes share a section-header colour, so the resolved
	// theme is given a distinctive one: without it the assertions below would
	// hold no matter which theme the command picked.
	resolved := *theme.New("quiet")
	resolved.Palette.SecondaryAccent = lipgloss.AdaptiveColor{Light: "#123456", Dark: "#123456"}

	origApply := applyTheme
	called := false
	applyTheme = func(_ *cobra.Command) *theme.Theme {
		called = true
		return &resolved
	}
	t.Cleanup(func() { applyTheme = origApply })

	out := runShow(t, "demo")

	assert.True(t, called, "show must resolve the theme from the active profile")

	want := components.SectionHeader(&resolved, "Connection")
	def := components.SectionHeader(theme.New("formae"), "Connection")
	require.NotEqual(t, def, want, "the two themes must render differently for this assertion to mean anything")
	assert.Contains(t, out, want, "show must render in the resolved active-profile theme")
	assert.NotContains(t, out, def, "show must not fall back to the default theme")
}

// cleanConfigDir points the store at an empty config dir: a machine where
// formae has never run.
func cleanConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	return dir
}

// `show` with no name answers "the configuration I would use", so it takes the
// config-load path: on a machine where formae has never run it bootstraps the
// default profile exactly as every other config-loading command does, rather
// than reporting a fresh install as an error.
func TestShowWithNoArgumentBootstrapsACleanInstall(t *testing.T) {
	dir := cleanConfigDir(t)

	out := runShow(t, "--output-consumer", "machine", "--output-schema", "json")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "default", got["profile"])
	conn := got["cli"].(map[string]any)["connection"].(map[string]any)
	assert.Equal(t, "classic", conn["mode"], "the stub profile wires the local agent")

	assert.FileExists(t, filepath.Join(dir, "profiles", "default.pkl"))
	assert.FileExists(t, filepath.Join(dir, "active"))
}

// The same path performs the legacy migration, so a user still holding a bare
// formae.conf.pkl is shown their own configuration rather than an error.
func TestShowWithNoArgumentMigratesALegacyConfig(t *testing.T) {
	dir := cleanConfigDir(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "formae.conf.pkl"), []byte(hostedProfilePkl), 0o600))

	out := runShow(t, "--output-consumer", "machine", "--output-schema", "json")

	assert.Contains(t, out, `"profile":"default"`)
	assert.Contains(t, out, "3f2b8c14-0000-4000-8000-000000000000",
		"the migrated profile is what gets shown")
	assert.FileExists(t, filepath.Join(dir, "profiles", "default.pkl"))
	assert.NoFileExists(t, filepath.Join(dir, "formae.conf.pkl"),
		"the legacy file is moved, not copied")
}

// Only the no-name form takes the config-load path. `show <name>` stays a pure
// read: naming a profile that does not exist must not conjure one.
func TestShowWithANameDoesNotBootstrap(t *testing.T) {
	dir := cleanConfigDir(t)

	cmd := newShowCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"prod"})
	require.Error(t, cmd.Execute())

	assert.NoFileExists(t, filepath.Join(dir, "active"))
	assert.NoDirExists(t, filepath.Join(dir, "profiles"))
}
