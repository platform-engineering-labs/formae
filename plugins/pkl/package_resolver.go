// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"
	"os"
	"strings"
)

// LocalPluginPackagesEnvVar is the environment variable for local package overrides
// Format: "NAME1,/path1;NAME2,/path2" (semicolon-separated pairs of name,path)
const LocalPluginPackagesEnvVar = "LOCAL_PKL_PLUGIN_PACKAGES"

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
// It builds remote packages and allows local overrides from LOCAL_PKL_PLUGIN_PACKAGES env var.
type PackageResolver struct {
	packages       map[string]Package // name -> Package (lowercase)
	localOverrides map[string]string  // name -> local path (lowercase)
}

// NewPackageResolver creates a resolver.
// It automatically loads local overrides from LOCAL_PKL_PLUGIN_PACKAGES environment variable.
func NewPackageResolver() *PackageResolver {
	r := &PackageResolver{
		packages:       make(map[string]Package),
		localOverrides: make(map[string]string),
	}
	r.loadLocalOverrides()
	return r
}

// loadLocalOverrides parses LOCAL_PKL_PLUGIN_PACKAGES env var into the overrides map.
// Format: "NAME1,/path1;NAME2,/path2"
func (r *PackageResolver) loadLocalOverrides() {
	env := os.Getenv(LocalPluginPackagesEnvVar)
	if env == "" {
		return
	}

	entries := strings.Split(env, ";")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, ",", 2)
		if len(parts) != 2 {
			continue // Skip malformed entries
		}

		name := strings.TrimSpace(parts[0])
		path := strings.TrimSpace(parts[1])

		if name == "" || path == "" {
			continue
		}

		r.localOverrides[strings.ToLower(name)] = path
	}
}

// Add adds a remote package with the given namespace, plugin, and version.
// If a local override exists for this namespace, the local path is used instead.
func (r *PackageResolver) Add(namespace, plugin, version string) {
	name := strings.ToLower(namespace)

	// Check if there's a local override
	if localPath, ok := r.localOverrides[name]; ok {
		r.packages[name] = Package{
			Name:      name,
			IsLocal:   true,
			LocalPath: localPath,
		}
		return
	}

	// Add as remote package
	r.packages[name] = Package{
		Name:    name,
		Plugin:  strings.ToLower(plugin),
		Version: version,
		IsLocal: false,
	}
}

// AddLocal adds a local package directly (bypasses override check).
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

// HasLocalOverride returns true if the given namespace has a local override
func (r *PackageResolver) HasLocalOverride(namespace string) bool {
	_, ok := r.localOverrides[strings.ToLower(namespace)]
	return ok
}

// GetLocalOverrides returns a copy of the local overrides map
func (r *PackageResolver) GetLocalOverrides() map[string]string {
	result := make(map[string]string, len(r.localOverrides))
	for k, v := range r.localOverrides {
		result[k] = v
	}
	return result
}
