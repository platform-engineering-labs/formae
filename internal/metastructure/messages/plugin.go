// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// PluginAnnouncement is an alias for plugin.PluginAnnouncement
type PluginAnnouncement = plugin.PluginAnnouncement

// PluginHeartbeat is sent periodically by plugins to indicate they're still healthy
type PluginHeartbeat struct {
	Namespace string // e.g., "AWS"
}

// SpawnPluginOperator is sent to PluginCoordinator to spawn a PluginOperator for a resource operation.
// The coordinator will spawn remotely on the plugin node (distributed) or locally (fallback).
type SpawnPluginOperator struct {
	Namespace   string  // e.g., "AWS", "Azure", "FakeAWS"
	ResourceURI string  // e.g., "formae://ksuid"
	Operation   string  // e.g., "Create", "Read", "Update", "Delete"
	OperationID string  // unique ID for this operation
	RequestedBy gen.PID // PID of the requester (ResourceUpdater) - passed to PluginOperator Init
}

// SpawnPluginOperatorResult is the response from PluginCoordinator after spawning a PluginOperator
type SpawnPluginOperatorResult struct {
	PID   gen.PID // PID of the spawned PluginOperator
	Error string  // Error message if spawn failed (empty on success)
}

// GetPluginNode is sent to PluginCoordinator to get the node name for a plugin namespace.
// This is used by ResourceUpdater to remote spawn PluginOperator on the plugin's node.
type GetPluginNode struct {
	Namespace string // e.g., "AWS", "Azure", "FakeAWS"
}

// PluginNode is the response containing the plugin's Ergo node name
type PluginNode struct {
	NodeName gen.Atom
}
