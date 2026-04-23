// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/orbital/mgr"
)

// CLIPluginManager manages plugin operations on the CLI host's local
// orbital tree. Used for dual-installing auth plugins that need to
// run on both the agent and the CLI.
type CLIPluginManager struct {
	orb    *mgr.Manager
	logger *slog.Logger
}

// NewCLIPluginManager creates a local plugin manager from the CLI's config.
// Returns nil, nil if no plugin repositories are configured.
func NewCLIPluginManager(logger *slog.Logger, repos []pkgmodel.Repository) (*CLIPluginManager, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	orb, err := opsmgr.NewFromRepositories(logger, repos, "")
	if err != nil {
		return nil, err
	}
	return &CLIPluginManager{orb: orb, logger: logger}, nil
}

// LocalInstall installs packages on the CLI host's orbital tree.
func (pm *CLIPluginManager) LocalInstall(names []string) error {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("failed to refresh local repos", "error", err)
	}
	return pm.orb.Install(names...)
}

// LocalUninstall removes packages from the CLI host's orbital tree.
func (pm *CLIPluginManager) LocalUninstall(names []string) error {
	return pm.orb.Remove(names...)
}

// LocalUpgrade upgrades packages on the CLI host's orbital tree.
func (pm *CLIPluginManager) LocalUpgrade(names []string) error {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("failed to refresh local repos", "error", err)
	}
	return pm.orb.Update(names...)
}
