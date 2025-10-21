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

type Manager struct {
	pluginPaths []string
	plugins     []*Plugin

	authenticationPlugins []*AuthenticationPlugin
	networkPlugins        []*NetworkPlugin
	resourcePlugins       []*ResourcePlugin
	schemaPlugins         []*SchemaPlugin
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
			panic(err)
		}

		lookup, err := dl.Lookup("Plugin")
		if err != nil {
			panic(err)
		}

		cast, ok := lookup.(Plugin)
		if !ok {
			panic("Could not cast plugin")
		}

		m.plugins = append(m.plugins, &cast)
		switch cast.Type() {
		case Authentication:
			cast, ok := lookup.(AuthenticationPlugin)
			if !ok {
				panic("Could not cast authentication plugin")
			}
			m.authenticationPlugins = append(m.authenticationPlugins, &cast)
		case Network:
			cast, ok := lookup.(NetworkPlugin)
			if !ok {
				panic("Could not cast network plugin")
			}
			m.networkPlugins = append(m.networkPlugins, &cast)
		case Resource:
			cast, ok := lookup.(ResourcePlugin)
			if !ok {
				panic("Could not cast resource plugin")
			}
			m.resourcePlugins = append(m.resourcePlugins, &cast)
		case Schema:
			cast, ok := lookup.(SchemaPlugin)
			if !ok {
				panic("Could not cast schema plugin")
			}
			m.schemaPlugins = append(m.schemaPlugins, &cast)
		default:
		}
	}
}

func (m *Manager) List() []*Plugin {
	return m.plugins
}

func (m *Manager) ListResourcePlugins() []*ResourcePlugin {
	return m.resourcePlugins
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
