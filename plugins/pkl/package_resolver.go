// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/masterminds/semver"
)

// Package represents a PKL schema package dependency
type Package struct {
	Name      string // Package name (e.g., "formae", "aws", "gcp")
	Plugin    string // Plugin name for remote packages (e.g., "pkl", "aws")
	Version   string // Version for remote packages
	IsLocal   bool   // True if this is a local package
	LocalPath string // Absolute path for local packages
}

// FormatForPklTemplate returns the package in the format expected by PklProjectTemplate.pkl
// Remote: "plugin.name@version" (e.g., "pkl.formae@0.75.1")
// Local: "local:name:/path/to/PklProject" (e.g., "local:gcp:/path/to/PklProject")
func (p Package) FormatForPklTemplate() string {
	if p.IsLocal {
		return fmt.Sprintf("local:%s:%s", p.Name, p.LocalPath)
	}
	return fmt.Sprintf("%s.%s@%s", p.Plugin, p.Name, p.Version)
}

// PackageResolver manages PKL package dependencies.
// By default, packages are resolved remotely. Use WithLocalSchemas() to enable
// local schema resolution from installed plugins.
type PackageResolver struct {
	packages             map[string]Package // name -> Package (lowercase)
	localSchemaBasePath  string             // Base path for local schemas (e.g., ~/.pel/formae/plugins)
	useLocalSchemas      bool               // Whether to resolve schemas locally
}

// NewPackageResolver creates a resolver that resolves packages remotely by default.
func NewPackageResolver() *PackageResolver {
	return &PackageResolver{
		packages: make(map[string]Package),
	}
}

// WithLocalSchemas configures the resolver to use local schemas from installed plugins.
// The basePath should be the plugins directory (e.g., ~/.pel/formae/plugins).
// When enabled, Add() will look for schemas at basePath/<namespace>/v<version>/schema/pkl/PklProject
func (r *PackageResolver) WithLocalSchemas(basePath string) *PackageResolver {
	r.localSchemaBasePath = basePath
	r.useLocalSchemas = true
	return r
}

// Add adds a package with the given namespace, plugin, and version.
// If local schemas are enabled and the package is installed locally, uses the local path.
// Otherwise, adds as a remote package.
func (r *PackageResolver) Add(namespace, plugin, version string) {
	name := strings.ToLower(namespace)

	// If local schemas are enabled, try to find the installed schema
	if r.useLocalSchemas && r.localSchemaBasePath != "" {
		if localPath := r.findLocalSchema(namespace); localPath != "" {
			r.packages[name] = Package{
				Name:      name,
				IsLocal:   true,
				LocalPath: localPath,
			}
			return
		}
	}

	// Add as remote package
	r.packages[name] = Package{
		Name:    name,
		Plugin:  strings.ToLower(plugin),
		Version: version,
		IsLocal: false,
	}
}

// findLocalSchema looks for an installed schema at basePath/<namespace>/v*/schema/pkl/PklProject
// Returns the path to PklProject for the highest installed version, or empty string if not found.
func (r *PackageResolver) findLocalSchema(namespace string) string {
	pluginDir := filepath.Join(r.localSchemaBasePath, strings.ToLower(namespace))

	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return ""
	}

	// Collect version directories
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Version directories start with 'v' (e.g., v0.1.0)
		if strings.HasPrefix(name, "v") {
			versions = append(versions, name)
		}
	}

	if len(versions) == 0 {
		return ""
	}

	// Sort by semver (highest first)
	sort.Slice(versions, func(i, j int) bool {
		vi, errI := semver.NewVersion(versions[i])
		vj, errJ := semver.NewVersion(versions[j])
		if errI != nil || errJ != nil {
			// Fall back to string comparison if parsing fails
			return versions[i] > versions[j]
		}
		return vi.GreaterThan(vj)
	})

	// Use highest version
	highestVersion := versions[0]
	pklProjectPath := filepath.Join(pluginDir, highestVersion, "schema", "pkl", "PklProject")

	// Verify the PklProject file exists
	if _, err := os.Stat(pklProjectPath); err != nil {
		return ""
	}

	return pklProjectPath
}

// AddLocal adds a local package directly (bypasses version lookup).
func (r *PackageResolver) AddLocal(namespace, path string) {
	name := strings.ToLower(namespace)
	r.packages[name] = Package{
		Name:      name,
		IsLocal:   true,
		LocalPath: path,
	}
}

// GetPackageStrings returns all packages formatted for PklProjectTemplate.pkl
func (r *PackageResolver) GetPackageStrings() []string {
	result := make([]string, 0, len(r.packages))
	for _, pkg := range r.packages {
		result = append(result, pkg.FormatForPklTemplate())
	}
	return result
}

// GetPackages returns all packages as Package structs
func (r *PackageResolver) GetPackages() []Package {
	result := make([]Package, 0, len(r.packages))
	for _, pkg := range r.packages {
		result = append(result, pkg)
	}
	return result
}

// IsUsingLocalSchemas returns true if local schema resolution is enabled
func (r *PackageResolver) IsUsingLocalSchemas() bool {
	return r.useLocalSchemas
}

// HasRemotePackages returns true if any packages are remote (need pkl project resolve)
func (r *PackageResolver) HasRemotePackages() bool {
	for _, pkg := range r.packages {
		if !pkg.IsLocal {
			return true
		}
	}
	return false
}
