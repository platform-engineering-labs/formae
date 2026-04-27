//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import "log/slog"

// newForTesting creates a PluginManager whose factory returns the same fake
// for every channel. Use newForTestingWithFactory when a test needs to model
// per-channel behavior.
func newForTesting(logger *slog.Logger, orb orbitalClient) *PluginManager {
	return newForTestingWithFactory(logger, orb, func(string) (orbitalClient, error) {
		return orb, nil
	})
}

// newForTestingWithFactory creates a PluginManager with an explicit factory
// for channel-specific tests. listOrb is used for List/Uninstall (channel-
// agnostic), while factory(channel) is consulted for Available/Info/Install/
// Upgrade.
func newForTestingWithFactory(logger *slog.Logger, listOrb orbitalClient, factory orbitalFactory) *PluginManager {
	return &PluginManager{logger: logger, listOrb: listOrb, factory: factory}
}
