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
	packages            map[string]Package // name -> Package (lowercase)
	localSchemaBasePath string             // Base path for local schemas (e.g., ~/.pel/formae/plugins)
	useLocalSchemas     bool               // Whether to resolve schemas locally
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
		if localPath, pkgName := r.findLocalSchema(namespace); localPath != "" {
			// Use the package name from PklProject as the dependency alias
			// This ensures import*("@pkgName/**/*.pkl") globs work correctly
			aliasName := pkgName
			if aliasName == "" {
				aliasName = name // fallback to namespace if package name not found
			}
			r.packages[aliasName] = Package{
				Name:      aliasName,
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

// findLocalSchema looks for an installed plugin with the given namespace.
// It iterates through all plugin directories, reads each manifest to find the namespace,
// and returns the schema PklProject path and package name for the matching plugin.
// Returns empty strings if not found.
func (r *PackageResolver) findLocalSchema(namespace string) (string, string) {
	// Iterate through all plugin directories to find one with matching namespace
	pluginDirs, err := os.ReadDir(r.localSchemaBasePath)
	if err != nil {
		return "", ""
	}

	targetNamespace := strings.ToUpper(namespace)

	for _, pluginEntry := range pluginDirs {
		if !pluginEntry.IsDir() {
			continue
		}

		pluginDir := filepath.Join(r.localSchemaBasePath, pluginEntry.Name())

		// Find the highest version for this plugin
		versionPath, _ := r.findHighestVersion(pluginDir)
		if versionPath == "" {
			continue
		}

		// Read the manifest to get the namespace
		manifestPath := filepath.Join(versionPath, "formae-plugin.pkl")
		pluginNamespace := r.readNamespaceFromManifest(manifestPath)
		if pluginNamespace == "" {
			continue
		}

		// Check if namespace matches
		if strings.ToUpper(pluginNamespace) == targetNamespace {
			pklProjectPath := filepath.Join(versionPath, "schema", "pkl", "PklProject")
			if _, err := os.Stat(pklProjectPath); err == nil {
				// Read the package name from PklProject
				pkgName := r.readPackageNameFromPklProject(pklProjectPath)
				return pklProjectPath, pkgName
			}
		}
	}

	return "", ""
}

// findHighestVersion finds the highest version directory in a plugin directory.
// Returns the full path to the version directory and the version string, or empty strings if not found.
func (r *PackageResolver) findHighestVersion(pluginDir string) (string, string) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return "", ""
	}

	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "v") {
			versions = append(versions, name)
		}
	}

	if len(versions) == 0 {
		return "", ""
	}

	// Sort by semver (highest first)
	sort.Slice(versions, func(i, j int) bool {
		vi, errI := semver.NewVersion(versions[i])
		vj, errJ := semver.NewVersion(versions[j])
		if errI != nil || errJ != nil {
			return versions[i] > versions[j]
		}
		return vi.GreaterThan(vj)
	})

	highestVersion := versions[0]
	return filepath.Join(pluginDir, highestVersion), highestVersion
}

// readNamespaceFromManifest reads the namespace field from a formae-plugin.pkl manifest.
// Returns empty string if the file cannot be read or namespace is not found.
func (r *PackageResolver) readNamespaceFromManifest(manifestPath string) string {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ""
	}

	// Simple parsing: look for 'namespace = "VALUE"' pattern
	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "namespace") {
			// Extract value between quotes
			start := strings.Index(line, "\"")
			end := strings.LastIndex(line, "\"")
			if start != -1 && end > start {
				return line[start+1 : end]
			}
		}
	}

	return ""
}

// readPackageNameFromPklProject reads the package name from a PklProject file.
// Returns empty string if the file cannot be read or name is not found.
func (r *PackageResolver) readPackageNameFromPklProject(pklProjectPath string) string {
	data, err := os.ReadFile(pklProjectPath)
	if err != nil {
		return ""
	}

	// Simple parsing: look for 'name = "VALUE"' pattern inside package block
	content := string(data)
	inPackageBlock := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "package {" || strings.HasPrefix(line, "package {") {
			inPackageBlock = true
			continue
		}
		if inPackageBlock && line == "}" {
			break
		}
		if inPackageBlock && strings.HasPrefix(line, "name") {
			// Extract value between quotes
			start := strings.Index(line, "\"")
			end := strings.LastIndex(line, "\"")
			if start != -1 && end > start {
				return line[start+1 : end]
			}
		}
	}

	return ""
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
