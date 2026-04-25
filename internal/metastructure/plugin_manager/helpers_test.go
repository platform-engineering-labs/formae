//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import "log/slog"

// newForTesting creates a PluginManager with a fake orbital client.
func newForTesting(logger *slog.Logger, orb orbitalClient) *PluginManager {
	return &PluginManager{logger: logger, orb: orb}
}
