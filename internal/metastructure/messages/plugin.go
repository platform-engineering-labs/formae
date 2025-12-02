// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// PluginAnnouncement is an alias for plugin.PluginAnnouncement
type PluginAnnouncement = plugin.PluginAnnouncement

// GetFilters is an alias for plugin.GetFilters
type GetFilters = plugin.GetFilters

// GetFiltersResponse is an alias for plugin.GetFiltersResponse
type GetFiltersResponse = plugin.GetFiltersResponse

// UnregisterPlugin is sent when a plugin becomes unavailable
type UnregisterPlugin struct {
	Namespace string
	Reason    string // "crashed", "shutdown", "node_down"
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

// SpawnPluginOperatorResult is the response from PluginCoordinator after spawning a PluginOperator
type SpawnPluginOperatorResult struct {
	PID   gen.PID
	Error string
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
	Namespace      string
	RefreshFilters bool // If true, fetch fresh filters from plugin before responding
}

// PluginInfoResponse contains plugin capabilities
type PluginInfoResponse struct {
	Found              bool
	Namespace          string
	SupportedResources []plugin.ResourceDescriptor
	ResourceSchemas    map[string]model.Schema
	MatchFilters       []plugin.MatchFilter
	Error              string // Set if Found is false
}
