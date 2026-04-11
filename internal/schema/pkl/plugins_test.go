// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package pkl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePluginWrappers_CreatesWrapperForPluginWithSchema(t *testing.T) {
	pluginDir := t.TempDir()

	// Create a fake plugin with schema/pkl/Config.pkl
	configDir := filepath.Join(pluginDir, "auth-basic", "v0.1.0", "schema", "pkl")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "Config.pkl"), []byte("open module authBasic.Config\n"), 0644))

	err := GeneratePluginWrappers(pluginDir)
	require.NoError(t, err)

	wrapperPath := filepath.Join(pluginDir, "AuthBasic.pkl")
	content, err := os.ReadFile(wrapperPath)
	require.NoError(t, err)

	assert.Contains(t, string(content), `extends "./auth-basic/v0.1.0/schema/pkl/Config.pkl"`)
	assert.Contains(t, string(content), "Auto-generated wrapper for auth-basic plugin")
}

func TestGeneratePluginWrappers_SkipsPluginsWithoutSchema(t *testing.T) {
	pluginDir := t.TempDir()

	// Create a plugin directory without schema/pkl/Config.pkl
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "aws", "v0.1.3", "schema", "pkl"), 0755))
	// No Config.pkl written

	err := GeneratePluginWrappers(pluginDir)
	require.NoError(t, err)

	wrapperPath := filepath.Join(pluginDir, "Aws.pkl")
	_, err = os.Stat(wrapperPath)
	assert.True(t, os.IsNotExist(err), "expected no wrapper to be created for plugin without Config.pkl")
}

func TestGeneratePluginWrappers_EmptyDir(t *testing.T) {
	pluginDir := t.TempDir()

	err := GeneratePluginWrappers(pluginDir)
	require.NoError(t, err)

	entries, err := os.ReadDir(pluginDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "expected no files in an empty plugin directory")
}

func TestGeneratePluginWrappers_PicksHighestVersion(t *testing.T) {
	pluginDir := t.TempDir()

	// Create two versions — only v0.2.0 has Config.pkl but both are version dirs
	for _, ver := range []string{"v0.1.0", "v0.2.0"} {
		configDir := filepath.Join(pluginDir, "my-plugin", ver, "schema", "pkl")
		require.NoError(t, os.MkdirAll(configDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(configDir, "Config.pkl"), []byte("open module myPlugin.Config\n"), 0644))
	}

	err := GeneratePluginWrappers(pluginDir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(pluginDir, "MyPlugin.pkl"))
	require.NoError(t, err)

	assert.Contains(t, string(content), `extends "./my-plugin/v0.2.0/schema/pkl/Config.pkl"`)
}

func TestGeneratePluginWrappers_SkipsNonDirectoryEntries(t *testing.T) {
	pluginDir := t.TempDir()

	// Create a regular file in the plugin directory
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "readme.txt"), []byte("not a plugin"), 0644))

	err := GeneratePluginWrappers(pluginDir)
	require.NoError(t, err)
	// No panic, no error — just skipped
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"auth-basic", "AuthBasic"},
		{"gcp", "Gcp"},
		{"my-cool-plugin", "MyCoolPlugin"},
		{"aws", "Aws"},
		{"a", "A"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toPascalCase(tt.input))
		})
	}
}

func TestFindHighestVersionDir(t *testing.T) {
	dir := t.TempDir()

	// No version dirs
	assert.Equal(t, "", findHighestVersionDir(dir))

	// Add some version dirs
	for _, v := range []string{"v0.1.0", "v0.2.0", "v0.1.5"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, v), 0755))
	}

	assert.Equal(t, "v0.2.0", findHighestVersionDir(dir))
}

func TestFindHighestVersionDir_NonExistentDir(t *testing.T) {
	assert.Equal(t, "", findHighestVersionDir("/nonexistent/path"))
}
