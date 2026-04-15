// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createFakePlugin creates a fake plugin directory structure:
//
//	<baseDir>/<name>/<version>/<name>        (binary)
//	<baseDir>/<name>/<version>/formae-plugin.pkl  (manifest, if manifestPkl != "")
func createFakePlugin(t *testing.T, baseDir, name, version, manifestPkl string) {
	t.Helper()

	versionDir := filepath.Join(baseDir, name, version)
	require.NoError(t, os.MkdirAll(versionDir, 0755))

	// Create a fake binary (just an empty executable file)
	binaryPath := filepath.Join(versionDir, name)
	require.NoError(t, os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0755))

	// Create manifest if provided
	if manifestPkl != "" {
		manifestPath := filepath.Join(versionDir, plugin.DefaultManifestPath)
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifestPkl), 0644))
	}
}

func resourceManifest(name, namespace string) string {
	return `name = "` + name + `"
version = "1.0.0"
namespace = "` + namespace + `"
license = "Apache-2.0"
minFormaeVersion = "0.80.0"
output { renderer = new JsonRenderer {} }
`
}

func authManifest(name string) string {
	return `name = "` + name + `"
type = "auth"
version = "1.0.0"
license = "Apache-2.0"
minFormaeVersion = "0.80.0"
output { renderer = new JsonRenderer {} }
`
}

func TestDiscoverPlugins_Resource_FindsPluginWithManifest(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "cloudflare-dns", "v1.2.0", resourceManifest("cloudflare-dns", "CLOUDFLARE"))

	results := DiscoverPlugins(baseDir, Resource)

	require.Len(t, results, 1)
	assert.Equal(t, "cloudflare-dns", results[0].Name)
	assert.Equal(t, "CLOUDFLARE", results[0].Namespace)
	assert.Equal(t, "v1.2.0", results[0].Version)
	assert.Equal(t, filepath.Join(baseDir, "cloudflare-dns", "v1.2.0", "cloudflare-dns"), results[0].BinaryPath)
	assert.Equal(t, Resource, results[0].Type)
}

func TestDiscoverPlugins_Resource_SkipsAuthPlugins(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "auth-basic", "v1.0.0", authManifest("auth-basic"))
	createFakePlugin(t, baseDir, "aws", "v2.0.0", resourceManifest("aws", "AWS"))

	results := DiscoverPlugins(baseDir, Resource)

	require.Len(t, results, 1)
	assert.Equal(t, "aws", results[0].Name)
}

func TestDiscoverPlugins_Resource_PicksHighestVersion(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, baseDir, "aws", "v2.3.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, baseDir, "aws", "v1.5.0", resourceManifest("aws", "AWS"))

	results := DiscoverPlugins(baseDir, Resource)

	require.Len(t, results, 1)
	assert.Equal(t, "v2.3.0", results[0].Version)
}

func TestDiscoverPlugins_Resource_FallsBackToPluginNameAsNamespace(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "my-plugin", "v0.1.0", "")

	results := DiscoverPlugins(baseDir, Resource)

	require.Len(t, results, 1)
	assert.Equal(t, "my-plugin", results[0].Name)
	assert.Equal(t, "my-plugin", results[0].Namespace)
	assert.Equal(t, "v0.1.0", results[0].Version)
}

func TestDiscoverPlugins_Resource_EmptyDir(t *testing.T) {
	assert.Nil(t, DiscoverPlugins(t.TempDir(), Resource))
}

func TestDiscoverPlugins_Resource_NonexistentDir(t *testing.T) {
	assert.Nil(t, DiscoverPlugins("/nonexistent/path/to/plugins", Resource))
}

func TestDiscoverPlugins_Resource_EmptyPluginDir(t *testing.T) {
	assert.Nil(t, DiscoverPlugins("", Resource))
}

func TestDiscoverPlugins_Auth_FindsAuthPlugin(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "auth-basic", "v1.0.0", authManifest("auth-basic"))

	results := DiscoverPlugins(baseDir, Auth)

	require.Len(t, results, 1)
	assert.Equal(t, "auth-basic", results[0].Name)
	assert.Equal(t, "v1.0.0", results[0].Version)
	assert.Equal(t, filepath.Join(baseDir, "auth-basic", "v1.0.0", "auth-basic"), results[0].BinaryPath)
	assert.Equal(t, Auth, results[0].Type)
}

func TestDiscoverPlugins_Auth_SkipsResourcePlugins(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "aws", "v2.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, baseDir, "auth-basic", "v1.0.0", authManifest("auth-basic"))

	results := DiscoverPlugins(baseDir, Auth)

	require.Len(t, results, 1)
	assert.Equal(t, "auth-basic", results[0].Name)
}

func TestDiscoverPlugins_Auth_PicksHighestVersion(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "auth-basic", "v0.1.0", authManifest("auth-basic"))
	createFakePlugin(t, baseDir, "auth-basic", "v0.3.0", authManifest("auth-basic"))
	createFakePlugin(t, baseDir, "auth-basic", "v0.2.0", authManifest("auth-basic"))

	results := DiscoverPlugins(baseDir, Auth)

	require.Len(t, results, 1)
	assert.Equal(t, "v0.3.0", results[0].Version)
}

func TestDiscoverPlugins_Auth_EmptyDir(t *testing.T) {
	assert.Nil(t, DiscoverPlugins(t.TempDir(), Auth))
}

func TestDiscoverPlugins_Auth_NonexistentDir(t *testing.T) {
	assert.Nil(t, DiscoverPlugins("/nonexistent/path/to/plugins", Auth))
}

func TestDiscoverPlugins_Auth_EmptyPluginDir(t *testing.T) {
	assert.Nil(t, DiscoverPlugins("", Auth))
}

func TestDiscoverPlugins_Auth_SkipsPluginsWithoutManifest(t *testing.T) {
	baseDir := t.TempDir()
	createFakePlugin(t, baseDir, "auth-basic", "v1.0.0", "")

	assert.Nil(t, DiscoverPlugins(baseDir, Auth))
}

func TestDiscoverPluginsMulti_DevOverridesSystem(t *testing.T) {
	systemDir := t.TempDir()
	devDir := t.TempDir()

	createFakePlugin(t, systemDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, devDir, "aws", "v0.0.1-dev", resourceManifest("aws", "AWS"))

	results := DiscoverPluginsMulti([]string{devDir, systemDir}, Resource)

	require.Len(t, results, 1)
	assert.Equal(t, "aws", results[0].Name)
	assert.Equal(t, "v0.0.1-dev", results[0].Version)
	assert.Contains(t, results[0].BinaryPath, devDir)
}

func TestDiscoverPluginsMulti_SystemUsedWhenNoDevOverride(t *testing.T) {
	systemDir := t.TempDir()
	devDir := t.TempDir()

	createFakePlugin(t, systemDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))
	createFakePlugin(t, systemDir, "azure", "v2.0.0", resourceManifest("azure", "AZURE"))
	createFakePlugin(t, devDir, "aws", "v0.0.1-dev", resourceManifest("aws", "AWS"))

	results := DiscoverPluginsMulti([]string{devDir, systemDir}, Resource)

	require.Len(t, results, 2)

	names := map[string]string{}
	for _, r := range results {
		names[r.Name] = r.Version
	}
	assert.Equal(t, "v0.0.1-dev", names["aws"])
	assert.Equal(t, "v2.0.0", names["azure"])
}

func TestDiscoverPluginsMulti_SkipsEmptyAndNonexistentDirs(t *testing.T) {
	systemDir := t.TempDir()
	createFakePlugin(t, systemDir, "aws", "v1.0.0", resourceManifest("aws", "AWS"))

	results := DiscoverPluginsMulti([]string{"", "/nonexistent", t.TempDir(), systemDir}, Resource)

	require.Len(t, results, 1)
	assert.Equal(t, "aws", results[0].Name)
}

func TestDiscoverPluginsMulti_NilDirs(t *testing.T) {
	assert.Nil(t, DiscoverPluginsMulti(nil, Resource))
}

func TestDiscoverPluginsMulti_Auth(t *testing.T) {
	systemDir := t.TempDir()
	devDir := t.TempDir()

	createFakePlugin(t, systemDir, "auth-basic", "v1.0.0", authManifest("auth-basic"))
	createFakePlugin(t, devDir, "auth-custom", "v0.1.0", authManifest("auth-custom"))

	results := DiscoverPluginsMulti([]string{devDir, systemDir}, Auth)

	require.Len(t, results, 2)
	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
	}
	assert.True(t, names["auth-basic"])
	assert.True(t, names["auth-custom"])
}
