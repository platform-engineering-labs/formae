// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package network

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// NetworkPlugin defines the interface that network plugins must implement.
type NetworkPlugin interface {
	Name() string
	Client(config json.RawMessage) (*http.Client, error)
	Listen(config json.RawMessage, port int) (net.Listener, error)
}

// Registry stores network plugins indexed by name.
type Registry struct {
	byName map[string]NetworkPlugin
}

// DefaultRegistry is the package-level registry used by default.
var DefaultRegistry = NewRegistry()

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]NetworkPlugin),
	}
}

// Register adds a network plugin to the registry.
func (r *Registry) Register(plugin NetworkPlugin) {
	r.byName[strings.ToLower(plugin.Name())] = plugin
}

// Get returns the network plugin registered under the given name (case-insensitive),
// or an error if no such plugin exists.
func (r *Registry) Get(name string) (NetworkPlugin, error) {
	plugin, ok := r.byName[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("no network plugin registered with name %q", name)
	}
	return plugin, nil
}

// GetByConfig extracts the "Type" field from the config JSON and looks up the
// corresponding network plugin.
func (r *Registry) GetByConfig(config json.RawMessage) (NetworkPlugin, error) {
	name := gjson.Get(string(config), "Type").String()
	if name == "" {
		return nil, fmt.Errorf("network plugin config missing \"Type\" field")
	}
	return r.Get(name)
}
