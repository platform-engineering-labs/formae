// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goplugin "plugin"
	"sort"
	"strings"

	"github.com/masterminds/semver"
	"github.com/tidwall/gjson"
)

// ResourcePluginInfo contains metadata for external resource plugins
// that run as separate processes (not loaded via plugin.Open).
// TODO: Move PluginManager out of pkg/plugin package as it should not be exposed to library users.
type ResourcePluginInfo struct {
	Namespace  string
	Version    string
	BinaryPath string
}

type Manager struct {
	pluginPaths       []string
	externalPluginDir string
	plugins           []*Plugin

	authenticationPlugins []*AuthenticationPlugin
	networkPlugins        []*NetworkPlugin
	resourcePlugins       []*FullResourcePlugin
	schemaPlugins         []*SchemaPlugin

	// External resource plugins that run as separate processes
	externalResourcePlugins []ResourcePluginInfo
}

func NewManager(externalPluginDir string, paths ...string) *Manager {
	pluginPaths := detectPluginPaths()

	if len(paths) > 0 {
		// Add custom paths to default paths
		pluginPaths = append(pluginPaths, paths...)
	}
	return &Manager{
		pluginPaths:       pluginPaths,
		externalPluginDir: externalPluginDir,
	}
}

func isDebuggerAttached() bool {
	// Check if process is running under debugger
	return strings.Contains(os.Args[0], "__debug_bin")
}

func detectPluginPaths() []string {
	binPath, _ := os.Executable()

	// ../plugins from binary
	// installed, untared from an archive in any folder
	if path, ok := pluginDirExists(filepath.Join(filepath.Dir(filepath.Dir(binPath)), "plugins")); ok {
		return []string{
			path,
		}
	}

	// ./plugins from binary
	// binary built in source tree
	if path, ok := pluginDirExists(filepath.Join(filepath.Dir(binPath), "plugins")); ok {
		return []string{
			path,
		}
	}

	// only needed for development via go run
	return []string{
		"./",
		"../plugins",
		"./plugins",
	}
}

func pluginDirExists(path string) (string, bool) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return path, false
	}
	if err != nil {
		return path, false
	}
	return path, info.IsDir()
}

func (m *Manager) Load() {
	var found []string

	pattern := "*.so"
	if isDebuggerAttached() {
		pattern = "*-debug.so"
	}

	for _, path := range m.pluginPaths {
		matches, _ := filepath.Glob(path + "/" + pattern)
		found = append(found, matches...)

		matches, _ = filepath.Glob(path + "/*/" + pattern)
		found = append(found, matches...)
	}

	for _, p := range found {
		if strings.Contains(p, "-debug.so") && !isDebuggerAttached() {
			continue
		}

		dl, err := goplugin.Open(p)
		if err != nil {
			// Skip plugins that fail to load (e.g., version mismatch)
			// This allows the system to continue with other plugins
			fmt.Fprintf(os.Stderr, "Warning: Failed to load plugin %s: %v\n", p, err)
			continue
		}

		lookup, err := dl.Lookup("Plugin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Plugin %s missing 'Plugin' symbol: %v\n", p, err)
			continue
		}

		cast, ok := lookup.(Plugin)
		if !ok {
			fmt.Fprintf(os.Stderr, "Warning: Could not cast plugin %s\n", p)
			continue
		}

		m.plugins = append(m.plugins, &cast)
		switch cast.Type() {
		case Authentication:
			cast, ok := lookup.(AuthenticationPlugin)
			if !ok {
				fmt.Fprintf(os.Stderr, "Warning: Could not cast authentication plugin %s\n", p)
				continue
			}
			m.authenticationPlugins = append(m.authenticationPlugins, &cast)
		case Network:
			cast, ok := lookup.(NetworkPlugin)
			if !ok {
				fmt.Fprintf(os.Stderr, "Warning: Could not cast network plugin %s\n", p)
				continue
			}
			m.networkPlugins = append(m.networkPlugins, &cast)
		case Resource:
			// Load in-process resource plugins for local testing (e.g., FakeAWS)
			// Production plugins run as external processes and are discovered separately
			// Built-in plugins must implement FullResourcePlugin (includes identity methods)
			cast, ok := lookup.(FullResourcePlugin)
			if !ok {
				fmt.Fprintf(os.Stderr, "Warning: Could not cast resource plugin %s to FullResourcePlugin\n", p)
				continue
			}
			m.resourcePlugins = append(m.resourcePlugins, &cast)
		case Schema:
			cast, ok := lookup.(SchemaPlugin)
			if !ok {
				fmt.Fprintf(os.Stderr, "Warning: Could not cast schema plugin %s\n", p)
				continue
			}
			m.schemaPlugins = append(m.schemaPlugins, &cast)
		default:
		}
	}

	// Discover external resource plugins that run as separate processes
	m.discoverExternalResourcePlugins()
}

func (m *Manager) discoverExternalResourcePlugins() {
	// Skip discovery if no external plugin directory is configured
	if m.externalPluginDir == "" {
		return
	}

	pluginBaseDir := m.externalPluginDir

	// Check if plugin directory exists
	if _, err := os.Stat(pluginBaseDir); os.IsNotExist(err) {
		return
	}

	// Walk through namespace directories
	namespaceEntries, err := os.ReadDir(pluginBaseDir)
	if err != nil {
		return
	}

	for _, namespaceEntry := range namespaceEntries {
		if !namespaceEntry.IsDir() {
			continue
		}

		namespace := namespaceEntry.Name()
		namespacePath := filepath.Join(pluginBaseDir, namespace)

		// Look for version directories
		versionEntries, err := os.ReadDir(namespacePath)
		if err != nil {
			continue
		}

		// Collect all valid versions with their paths
		type versionCandidate struct {
			version    *semver.Version
			versionStr string
			binaryPath string
		}
		var candidates []versionCandidate

		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() {
				continue
			}

			versionStr := versionEntry.Name()
			binaryPath := filepath.Join(namespacePath, versionStr, namespace)

			// Check if binary exists and is executable
			if info, err := os.Stat(binaryPath); err == nil && !info.IsDir() {
				// Try to parse as semver
				v, err := semver.NewVersion(versionStr)
				if err != nil {
					// Skip directories that aren't valid semver
					continue
				}
				candidates = append(candidates, versionCandidate{
					version:    v,
					versionStr: versionStr,
					binaryPath: binaryPath,
				})
			}
		}

		// Pick the highest version
		if len(candidates) > 0 {
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].version.GreaterThan(candidates[j].version)
			})
			best := candidates[0]
			m.externalResourcePlugins = append(m.externalResourcePlugins, ResourcePluginInfo{
				Namespace:  namespace,
				Version:    best.versionStr,
				BinaryPath: best.binaryPath,
			})
		}
	}
}

func (m *Manager) List() []*Plugin {
	return m.plugins
}

func (m *Manager) ListResourcePlugins() []*FullResourcePlugin {
	return m.resourcePlugins
}

func (m *Manager) ListExternalResourcePlugins() []ResourcePluginInfo {
	return m.externalResourcePlugins
}

func (m *Manager) PluginVersion(name string) *semver.Version {
	// First check external resource plugins (separate processes)
	// These are the distributed/process plugins like AWS that take precedence
	for _, info := range m.externalResourcePlugins {
		if strings.EqualFold(info.Namespace, name) {
			if v, err := semver.NewVersion(info.Version); err == nil {
				return v
			}
		}
	}

	// Then check loaded in-process plugins (.so)
	for _, plugin := range m.plugins {
		if (*plugin).Name() == name {
			return (*plugin).Version()
		}
	}

	return nil
}

func (m *Manager) AuthPlugin(config json.RawMessage) (*AuthenticationPlugin, error) {
	name := gjson.Get(string(config), "Type").String()

	for _, plugin := range m.authenticationPlugins {
		if strings.EqualFold((*plugin).(Plugin).Name(), name) {
			return plugin, nil
		}
	}

	return nil, fmt.Errorf("no plugin found for name: %s", name)
}

func (m *Manager) NetworkPlugin(config json.RawMessage) (*NetworkPlugin, error) {
	name := gjson.Get(string(config), "Type").String()

	for _, plugin := range m.networkPlugins {
		if strings.EqualFold((*plugin).(Plugin).Name(), name) {
			return plugin, nil
		}
	}

	return nil, fmt.Errorf("no plugin found for name: %s", name)
}

func (m *Manager) ResourcePlugin(namespace string) (*FullResourcePlugin, error) {
	for _, plugin := range m.resourcePlugins {
		if strings.EqualFold((*plugin).Namespace(), namespace) {
			return plugin, nil
		}
	}

	return nil, fmt.Errorf("no plugin found for namespace: %s", namespace)
}

func (m *Manager) SchemaPluginByFileExtension(fileExtension string) (*SchemaPlugin, error) {
	for _, plugin := range m.schemaPlugins {
		if (*plugin).FileExtension() == fileExtension {
			return plugin, nil
		}
	}

	return nil, fmt.Errorf("no plugin found for file extension: %s", fileExtension)
}

func (m *Manager) SchemaPlugin(name string) (*SchemaPlugin, error) {
	for _, plugin := range m.schemaPlugins {
		if (*plugin).Name() == name {
			return plugin, nil
		}
	}

	return nil, fmt.Errorf("no plugin found for name: %s", name)
}

func (m *Manager) SupportedFileExtensions() []string {
	var fileExtensions []string

	for _, plugin := range m.schemaPlugins {
		fileExtensions = append(fileExtensions, (*plugin).FileExtension())
	}

	sort.Strings(fileExtensions)

	return fileExtensions
}

func (m *Manager) SupportedSchemas() []string {
	var schemaNames []string

	for _, plugin := range m.schemaPlugins {
		schemaNames = append(schemaNames, (*plugin).Name())
	}

	sort.Strings(schemaNames)

	return schemaNames
}
