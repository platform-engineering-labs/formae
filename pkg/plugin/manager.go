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
	pluginPaths []string
	plugins     []*Plugin

	authenticationPlugins []*AuthenticationPlugin
	networkPlugins        []*NetworkPlugin
	resourcePlugins       []*ResourcePlugin
	schemaPlugins         []*SchemaPlugin

	// External resource plugins that run as separate processes
	externalResourcePlugins []ResourcePluginInfo
}

func NewManager(paths ...string) *Manager {
	pluginPaths := detectPluginPaths()

	if len(paths) > 0 {
		// Add custom paths to default paths
		pluginPaths = append(pluginPaths, paths...)
	}
	return &Manager{
		pluginPaths: pluginPaths,
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
			// Skip in-process resource plugins - they will be handled as external plugins
			// External resource plugins are discovered via discoverExternalResourcePlugins()
			continue
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
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// If we can't get home dir, skip external plugin discovery
		return
	}

	pluginBaseDir := filepath.Join(homeDir, ".pel", "formae", "plugins")

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

		// Use the first version found (version selection can be enhanced later)
		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() {
				continue
			}

			version := versionEntry.Name()
			binaryPath := filepath.Join(namespacePath, version, namespace+"-plugin")

			// Check if binary exists and is executable
			if info, err := os.Stat(binaryPath); err == nil && !info.IsDir() {
				m.externalResourcePlugins = append(m.externalResourcePlugins, ResourcePluginInfo{
					Namespace:  namespace,
					Version:    version,
					BinaryPath: binaryPath,
				})
				break // Only use first version
			}
		}
	}
}

func (m *Manager) List() []*Plugin {
	return m.plugins
}

func (m *Manager) ListResourcePlugins() []*ResourcePlugin {
	return m.resourcePlugins
}

func (m *Manager) ListExternalResourcePlugins() []ResourcePluginInfo {
	return m.externalResourcePlugins
}

func (m *Manager) PluginVersion(name string) *semver.Version {
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

func (m *Manager) ResourcePlugin(namespace string) (*ResourcePlugin, error) {
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
