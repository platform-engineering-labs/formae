// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestPackageResolver_Add(t *testing.T) {
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

func TestPackageResolver_LocalOverride(t *testing.T) {
	// Set environment variable
	os.Setenv(LocalPluginPackagesEnvVar, "GCP,/path/to/gcp/PklProject")
	defer os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()

	// Add gcp with remote info - should use local override instead
	resolver.Add("gcp", "gcp", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.Equal(t, "gcp", packages[0].Name)
	assert.True(t, packages[0].IsLocal)
	assert.Equal(t, "/path/to/gcp/PklProject", packages[0].LocalPath)
}

func TestPackageResolver_MultipleLocalOverrides(t *testing.T) {
	// Set environment variable with multiple packages
	os.Setenv(LocalPluginPackagesEnvVar, "GCP,/path/to/gcp/PklProject;AZURE,/path/to/azure/PklProject")
	defer os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()

	// Verify local overrides are loaded
	overrides := resolver.GetLocalOverrides()
	assert.Len(t, overrides, 2)
	assert.Equal(t, "/path/to/gcp/PklProject", overrides["gcp"])
	assert.Equal(t, "/path/to/azure/PklProject", overrides["azure"])
}

func TestPackageResolver_MixedPackages(t *testing.T) {
	// Set local override for GCP only
	os.Setenv(LocalPluginPackagesEnvVar, "GCP,/path/to/gcp/PklProject")
	defer os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()
	resolver.Add("formae", "pkl", "0.75.1")
	resolver.Add("aws", "aws", "0.75.1")   // Should be remote
	resolver.Add("gcp", "gcp", "0.75.1")   // Should be local (override)

	packages := resolver.GetPackages()
	assert.Len(t, packages, 3)

	// Check we have the right mix
	var hasLocalGcp, hasRemoteAws, hasRemoteFormae bool
	for _, pkg := range packages {
		switch pkg.Name {
		case "gcp":
			hasLocalGcp = pkg.IsLocal && pkg.LocalPath == "/path/to/gcp/PklProject"
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
	os.Setenv(LocalPluginPackagesEnvVar, "GCP,/path/to/gcp/PklProject")
	defer os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()
	resolver.Add("formae", "pkl", "0.75.1")
	resolver.Add("gcp", "gcp", "0.75.1") // Local override

	strings := resolver.GetPackageStrings()
	assert.Len(t, strings, 2)

	// Check the formatted strings
	hasFormae := false
	hasGcp := false
	for _, s := range strings {
		if s == "pkl.formae@0.75.1" {
			hasFormae = true
		}
		if s == "local:gcp:/path/to/gcp/PklProject" {
			hasGcp = true
		}
	}
	assert.True(t, hasFormae, "should have pkl.formae@0.75.1")
	assert.True(t, hasGcp, "should have local:gcp:/path/to/gcp/PklProject")
}

func TestPackageResolver_HasLocalOverride(t *testing.T) {
	os.Setenv(LocalPluginPackagesEnvVar, "GCP,/path/to/gcp/PklProject")
	defer os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()

	assert.True(t, resolver.HasLocalOverride("gcp"))
	assert.True(t, resolver.HasLocalOverride("GCP")) // Case insensitive
	assert.False(t, resolver.HasLocalOverride("aws"))
}

func TestPackageResolver_EmptyEnvVar(t *testing.T) {
	os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()
	resolver.Add("gcp", "gcp", "0.75.1")

	packages := resolver.GetPackages()
	assert.Len(t, packages, 1)
	assert.False(t, packages[0].IsLocal) // Should be remote when no override
}

func TestPackageResolver_MalformedEnvVar(t *testing.T) {
	// Malformed entries should be skipped
	os.Setenv(LocalPluginPackagesEnvVar, "INVALID;GCP,/valid/path;;,empty;AZURE")
	defer os.Unsetenv(LocalPluginPackagesEnvVar)

	resolver := NewPackageResolver()

	overrides := resolver.GetLocalOverrides()
	assert.Len(t, overrides, 1) // Only GCP should be valid
	assert.Equal(t, "/valid/path", overrides["gcp"])
}
