// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import (
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// orbitalClient abstracts the orbital Manager for testing.
type orbitalClient interface {
	Refresh() error
	Install(packages ...string) error
	Remove(packages ...string) error
	Update(packages ...string) error
	Ready() bool
}

// PluginManager manages plugin lifecycle operations via orbital.
type PluginManager struct {
	logger *slog.Logger
	orb    orbitalClient
}

// New creates a PluginManager backed by the orbital repositories in repos.
// Both formae-plugin and binary repos are loaded (the solver needs both for
// cross-repo dependency resolution, e.g. plugin depends on formae >= 0.85).
func New(logger *slog.Logger, repos []pkgmodel.Repository) (*PluginManager, error) {
	orb, err := opsmgr.NewFromRepositories(logger, repos, "")
	if err != nil {
		return nil, err
	}
	return &PluginManager{logger: logger, orb: orb}, nil
}

// newForTesting creates a PluginManager with a fake orbital client.
func newForTesting(logger *slog.Logger, orb orbitalClient) *PluginManager {
	return &PluginManager{logger: logger, orb: orb}
}
