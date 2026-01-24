// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackage_FormatForPklTemplate_Remote(t *testing.T) {
	pkg := Package{
		Name:    "formae",
		Plugin:  "pkl",
		Version: "0.75.1",
		IsLocal: false,
	}
	assert.Equal(t, "pkl.formae@0.75.1", pkg.FormatForPklTemplate())
}

func TestPackage_FormatForPklTemplate_Local(t *testing.T) {
	pkg := Package{
		Name:      "gcp",
		IsLocal:   true,
		LocalPath: "/Users/dev/gcp-plugin/PklProject",
	}
	assert.Equal(t, "local:gcp:/Users/dev/gcp-plugin/PklProject", pkg.FormatForPklTemplate())
}

func TestPackageResolver_Add_Remote(t *testing.T) {
	resolver := NewPackageResolver()

	resolver.Add("aws", "aws", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.Equal(t, "aws", packages[0].Name)
	assert.Equal(t, "aws", packages[0].Plugin)
	assert.Equal(t, "0.75.1", packages[0].Version)
	assert.False(t, packages[0].IsLocal)
}

func TestPackageResolver_AddFormae(t *testing.T) {
	resolver := NewPackageResolver()

	// formae uses pkl as plugin name
	resolver.Add("formae", "pkl", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.Equal(t, "formae", packages[0].Name)
	assert.Equal(t, "pkl", packages[0].Plugin)
	assert.Equal(t, "0.75.1", packages[0].Version)
	assert.False(t, packages[0].IsLocal)
}

func TestPackageResolver_AddLocal(t *testing.T) {
	resolver := NewPackageResolver()

	resolver.AddLocal("gcp", "/path/to/gcp/PklProject")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.Equal(t, "gcp", packages[0].Name)
	assert.True(t, packages[0].IsLocal)
	assert.Equal(t, "/path/to/gcp/PklProject", packages[0].LocalPath)
}

func TestPackageResolver_WithLocalSchemas(t *testing.T) {
	// Create temp directory structure for testing
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "gcp", "v0.75.1", "schema", "pkl")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))
	pklProjectPath := filepath.Join(pluginDir, "PklProject")
	require.NoError(t, os.WriteFile(pklProjectPath, []byte("amends \"pkl:Project\""), 0644))

	resolver := NewPackageResolver().WithLocalSchemas(tmpDir)

	// Add gcp - should use local schema since it exists
	resolver.Add("gcp", "gcp", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.Equal(t, "gcp", packages[0].Name)
	assert.True(t, packages[0].IsLocal)
	assert.Equal(t, pklProjectPath, packages[0].LocalPath)
}

func TestPackageResolver_WithLocalSchemas_FallsBackToRemote(t *testing.T) {
	// Create temp directory without the required plugin
	tmpDir := t.TempDir()

	resolver := NewPackageResolver().WithLocalSchemas(tmpDir)

	// Add aws - should fall back to remote since it's not installed locally
	resolver.Add("aws", "aws", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.Equal(t, "aws", packages[0].Name)
	assert.False(t, packages[0].IsLocal)
	assert.Equal(t, "aws", packages[0].Plugin)
	assert.Equal(t, "0.75.1", packages[0].Version)
}

func TestPackageResolver_WithLocalSchemas_SelectsHighestVersion(t *testing.T) {
	// Create temp directory structure with multiple versions
	tmpDir := t.TempDir()

	// Create v0.1.0
	v010Dir := filepath.Join(tmpDir, "ovh", "v0.1.0", "schema", "pkl")
	require.NoError(t, os.MkdirAll(v010Dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(v010Dir, "PklProject"), []byte("amends \"pkl:Project\""), 0644))

	// Create v0.2.0 (higher)
	v020Dir := filepath.Join(tmpDir, "ovh", "v0.2.0", "schema", "pkl")
	require.NoError(t, os.MkdirAll(v020Dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(v020Dir, "PklProject"), []byte("amends \"pkl:Project\""), 0644))

	// Create v0.1.5 (between)
	v015Dir := filepath.Join(tmpDir, "ovh", "v0.1.5", "schema", "pkl")
	require.NoError(t, os.MkdirAll(v015Dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(v015Dir, "PklProject"), []byte("amends \"pkl:Project\""), 0644))

	resolver := NewPackageResolver().WithLocalSchemas(tmpDir)

	resolver.Add("ovh", "ovh", "0.1.0")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.True(t, packages[0].IsLocal)
	// Should select v0.2.0 (highest version)
	assert.Contains(t, packages[0].LocalPath, "v0.2.0")
}

func TestPackageResolver_MixedPackages(t *testing.T) {
	// Create temp directory with local GCP schema
	tmpDir := t.TempDir()
	gcpDir := filepath.Join(tmpDir, "gcp", "v0.75.1", "schema", "pkl")
	require.NoError(t, os.MkdirAll(gcpDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gcpDir, "PklProject"), []byte("amends \"pkl:Project\""), 0644))

	resolver := NewPackageResolver().WithLocalSchemas(tmpDir)
	resolver.Add("formae", "pkl", "0.75.1") // No local schema, should be remote
	resolver.Add("aws", "aws", "0.75.1")    // No local schema, should be remote
	resolver.Add("gcp", "gcp", "0.75.1")    // Has local schema

	packages := resolver.GetPackages()
	assert.Len(t, packages, 3)

	// Check we have the right mix
	var hasLocalGcp, hasRemoteAws, hasRemoteFormae bool
	for _, pkg := range packages {
		switch pkg.Name {
		case "gcp":
			hasLocalGcp = pkg.IsLocal && pkg.LocalPath != ""
		case "aws":
			hasRemoteAws = !pkg.IsLocal && pkg.Plugin == "aws" && pkg.Version == "0.75.1"
		case "formae":
			hasRemoteFormae = !pkg.IsLocal && pkg.Plugin == "pkl" && pkg.Version == "0.75.1"
		}
	}
	assert.True(t, hasLocalGcp, "gcp should be local")
	assert.True(t, hasRemoteAws, "aws should be remote")
	assert.True(t, hasRemoteFormae, "formae should be remote")
}

func TestPackageResolver_GetPackageStrings(t *testing.T) {
	tmpDir := t.TempDir()
	gcpDir := filepath.Join(tmpDir, "gcp", "v0.75.1", "schema", "pkl")
	require.NoError(t, os.MkdirAll(gcpDir, 0755))
	pklProjectPath := filepath.Join(gcpDir, "PklProject")
	require.NoError(t, os.WriteFile(pklProjectPath, []byte("amends \"pkl:Project\""), 0644))

	resolver := NewPackageResolver().WithLocalSchemas(tmpDir)
	resolver.Add("formae", "pkl", "0.75.1")
	resolver.Add("gcp", "gcp", "0.75.1") // Local

	strings := resolver.GetPackageStrings()
	assert.Len(t, strings, 2)

	// Check the formatted strings
	hasFormae := false
	hasGcp := false
	for _, s := range strings {
		if s == "pkl.formae@0.75.1" {
			hasFormae = true
		}
		if s == "local:gcp:"+pklProjectPath {
			hasGcp = true
		}
	}
	assert.True(t, hasFormae, "should have pkl.formae@0.75.1")
	assert.True(t, hasGcp, "should have local:gcp:/path/to/PklProject")
}

func TestPackageResolver_IsUsingLocalSchemas(t *testing.T) {
	resolver := NewPackageResolver()
	assert.False(t, resolver.IsUsingLocalSchemas())

	resolver.WithLocalSchemas("/tmp/plugins")
	assert.True(t, resolver.IsUsingLocalSchemas())
}

func TestPackageResolver_DefaultRemote(t *testing.T) {
	// Without WithLocalSchemas, everything should be remote
	resolver := NewPackageResolver()
	resolver.Add("gcp", "gcp", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.False(t, packages[0].IsLocal) // Should be remote when local schemas not enabled
}

func TestPackageResolver_WithLocalSchemas_MissingPklProject(t *testing.T) {
	// Create version directory but without PklProject file
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "gcp", "v0.75.1", "schema", "pkl")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))
	// Don't create PklProject file

	resolver := NewPackageResolver().WithLocalSchemas(tmpDir)
	resolver.Add("gcp", "gcp", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.False(t, packages[0].IsLocal) // Should fall back to remote
}
