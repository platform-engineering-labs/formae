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

// GetPluginOperator is sent to PluginRegistry to request a remote PluginOperator PID
type GetPluginOperator struct {
	Namespace   string // e.g., "AWS", "Azure"
	ResourceURI string // e.g., "formae://ksuid"
	Operation   string // e.g., "Create", "Read", "Update", "Delete"
	OperationID string // unique ID for this operation
}

// PluginOperatorPID is the response from PluginRegistry containing a remote plugin operator PID
type PluginOperatorPID struct {
	PID gen.ProcessID
}
