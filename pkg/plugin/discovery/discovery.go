// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package discovery scans the external plugin directory for installed plugin
// binaries and their manifests. It returns metadata describing the highest
// semver version found for each plugin.
package discovery

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// PluginType identifies the kind of plugin.
type PluginType string

const (
	Resource PluginType = "resource"
	Auth     PluginType = "auth"
)

// PluginInfo holds discovery metadata for an installed plugin.
type PluginInfo struct {
	Name       string
	Namespace  string // from manifest; empty for auth plugins
	Version    string
	BinaryPath string
	Type       PluginType
}

// ToResourcePluginInfo converts to the SDK type used by the metastructure.
func (p PluginInfo) ToResourcePluginInfo() plugin.ResourcePluginInfo {
	return plugin.ResourcePluginInfo{
		Name:       p.Name,
		Namespace:  p.Namespace,
		Version:    p.Version,
		BinaryPath: p.BinaryPath,
	}
}

// ToAuthPluginInfo converts to the SDK type used by the auth subsystem.
func (p PluginInfo) ToAuthPluginInfo() plugin.AuthPluginInfo {
	return plugin.AuthPluginInfo{
		Name:       p.Name,
		Version:    p.Version,
		BinaryPath: p.BinaryPath,
	}
}

// DiscoverPlugins scans pluginDir for external plugin binaries of the given type.
// Each plugin is expected at <pluginDir>/<name>/v<semver>/<name> with an optional
// manifest at <pluginDir>/<name>/v<semver>/formae-plugin.pkl.
//
// When multiple versions exist, the highest semver wins. For resource plugins,
// if no manifest exists the directory name is used as namespace. For auth plugins,
// a manifest with type="auth" is required.
func DiscoverPlugins(pluginDir string, pluginType PluginType) []PluginInfo {
	if pluginDir == "" {
		return nil
	}

	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return nil
	}

	var results []PluginInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginName := entry.Name()
		pluginPath := filepath.Join(pluginDir, pluginName)

		best, manifest, ok := discoverHighestVersion(pluginPath, pluginName, pluginType)
		if !ok {
			continue
		}

		namespace := pluginName
		if manifest != nil && manifest.Namespace != "" {
			namespace = manifest.Namespace
		}

		results = append(results, PluginInfo{
			Name:       pluginName,
			Namespace:  namespace,
			Version:    best.versionStr,
			BinaryPath: best.binaryPath,
			Type:       pluginType,
		})
	}

	return results
}

type versionCandidate struct {
	version    *semver.Version
	versionStr string
	binaryPath string
}

// discoverHighestVersion scans pluginPath for version directories, parses their
// names as semver, checks that a binary named pluginName exists, reads the
// manifest, and returns the candidate with the highest version that matches the
// requested plugin type.
//
// For resource plugins, versions without a manifest are accepted (namespace
// falls back to directory name). Versions with an auth manifest are skipped.
// For auth plugins, only versions with a manifest declaring type="auth" qualify.
func discoverHighestVersion(pluginPath, pluginName string, pluginType PluginType) (versionCandidate, *plugin.Manifest, bool) {
	versionEntries, err := os.ReadDir(pluginPath)
	if err != nil {
		return versionCandidate{}, nil, false
	}

	type candidate struct {
		versionCandidate
		manifest *plugin.Manifest
	}
	var candidates []candidate

	for _, vEntry := range versionEntries {
		if !vEntry.IsDir() {
			continue
		}

		versionStr := vEntry.Name()
		binaryPath := filepath.Join(pluginPath, versionStr, pluginName)

		info, err := os.Stat(binaryPath)
		if err != nil || info.IsDir() {
			continue
		}

		v, err := semver.NewVersion(versionStr)
		if err != nil {
			continue
		}

		manifestPath := filepath.Join(pluginPath, versionStr, plugin.DefaultManifestPath)
		manifest, _ := plugin.ReadManifest(manifestPath)

		switch pluginType {
		case Auth:
			// Auth plugins require a manifest declaring type="auth"
			if manifest == nil || !manifest.IsAuthPlugin() {
				continue
			}
		case Resource:
			// Skip auth plugins from the resource list
			if manifest != nil && manifest.IsAuthPlugin() {
				continue
			}
		}

		candidates = append(candidates, candidate{
			versionCandidate: versionCandidate{
				version:    v,
				versionStr: versionStr,
				binaryPath: binaryPath,
			},
			manifest: manifest,
		})
	}

	if len(candidates) == 0 {
		return versionCandidate{}, nil, false
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].version.GreaterThan(candidates[j].version)
	})

	best := candidates[0]
	return best.versionCandidate, best.manifest, true
}
