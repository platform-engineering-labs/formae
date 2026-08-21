// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// PluginAnnouncement is an alias for plugin.PluginAnnouncement
type PluginAnnouncement = plugin.PluginAnnouncement

// OidcCredentialPluginAnnouncement is an alias for
// credential.OidcCredentialPluginAnnouncement: what a broker sends the
// PluginCoordinator on startup.
type OidcCredentialPluginAnnouncement = credential.OidcCredentialPluginAnnouncement

// UnregisterPlugin is sent when a plugin becomes unavailable
type UnregisterPlugin struct {
	Namespace string
	Reason    string // "crashed", "shutdown", "node_down"
}

// UnregisterOidcCredentialPlugin is sent when a credential broker becomes
// unavailable. SpawnToken is the token the departing process was launched
// with, so a registration made by a newer process of the same broker is not
// torn down by a late unregister from the old one.
type UnregisterOidcCredentialPlugin struct {
	Name       string
	SpawnToken string
	Reason     string // "crashed", "shutdown"
}

// SpawnPluginOperator is sent to PluginCoordinator to spawn a PluginOperator for a resource operation.
// The coordinator will spawn remotely on the plugin node (distributed) or locally (fallback).
type SpawnPluginOperator struct {
	Namespace   string
	ResourceURI string
	Operation   string
	OperationID string
	RequestedBy gen.PID
}

// SpawnPluginOperatorResult is the response from PluginCoordinator after spawning a PluginOperator.
// RetryConfig is the config the spawned operator polls on — the per-plugin
// override where one is configured, the global config otherwise. It is a
// pointer so an absent config is distinguishable from a config whose fields are
// legitimately zero.
type SpawnPluginOperatorResult struct {
	PID         gen.PID
	Error       string
	RetryConfig *model.RetryConfig
}

// GetPluginNode is sent to PluginCoordinator to get the node name for a plugin namespace.
// This is used by ResourceUpdater to remote spawn PluginOperator on the plugin's node.
type GetPluginNode struct {
	Namespace string
}

// PluginNode is the response containing the plugin's Ergo node name
type PluginNode struct {
	NodeName gen.Atom
}

// GetPluginInfo requests plugin metadata from PluginCoordinator
type GetPluginInfo struct {
	Namespace string
}

// PluginInfoResponse contains plugin capabilities
type PluginInfoResponse struct {
	Found                   bool
	Namespace               string
	SupportedResources      []plugin.ResourceDescriptor
	ResourceSchemas         map[string]model.Schema
	MatchFilters            []model.MatchFilter
	LabelConfig             model.LabelConfig
	ResourceTypesToDiscover []string
	Error                   string // Set if Found is false
}

// GetRegisteredPlugins requests a list of all registered plugins from PluginCoordinator
type GetRegisteredPlugins struct{}

// RegisteredPluginInfo contains basic information about a registered plugin
// including the merged config (plugin defaults + user overrides).
type RegisteredPluginInfo struct {
	Name                    string
	Namespace               string
	Version                 string
	NodeName                string
	MaxRequestsPerSecond    int
	ResourceCount           int
	ResourceTypesToDiscover []string
	RetryConfig             *model.RetryConfig
	LabelConfig             model.LabelConfig
	DiscoveryFilters        []model.MatchFilter
}

// GetRegisteredPluginsResult is the response to GetRegisteredPlugins
type GetRegisteredPluginsResult struct {
	Plugins []RegisteredPluginInfo
}
