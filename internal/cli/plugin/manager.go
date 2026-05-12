// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
)

// CLIPluginManager runs orbital against the local /opt/pel tree. While
// /opt/pel is root-owned, mutating operations re-exec the CLI under sudo
// via orbital's tree.New flow. Read-only listing does not.
type CLIPluginManager struct {
	orb    *mgr.Manager
	logger *slog.Logger
}

// NewCLIPluginManager constructs a manager from the CLI config. Both
// binary and formae-plugin repositories are loaded so the SAT solver can
// resolve cross-repo dependencies (e.g., a plugin's `depends: formae >=
// 0.85`). When channel is non-empty it overrides the URI fragment for
// every repo.
//
// Returns nil, nil if no repositories are configured — callers treat that
// as "no local install path available" rather than as an error. Returns
// an error if an orbital tree is configured but not initialized at the
// resolved path (orbital leaves cache/pki/lock nil in that case, and
// every later call would panic). Mirrors the agent-side
// plugin_manager.New guard so dev binaries get a clear message rather
// than a nil-pointer crash.
func NewCLIPluginManager(logger *slog.Logger, repos []pkgmodel.Repository, channel string) (*CLIPluginManager, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	orb, err := opsmgr.NewFromRepositories(logger, repos, channel)
	if err != nil {
		return nil, err
	}
	if !orb.Ready() {
		return nil, fmt.Errorf("orbital tree not initialized at %s; install formae via the orbital installer or set %s to point at an initialized tree (e.g. /opt/pel)", orb.Path, opsmgr.FormaePelRootEnv)
	}
	return &CLIPluginManager{orb: orb, logger: logger}, nil
}

// LocalInstall installs packages on the local orbital tree. orbital
// re-execs the process under sudo when the tree path is privileged.
func (pm *CLIPluginManager) LocalInstall(names []string) error {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("failed to refresh local repos", "error", err)
	}
	return pm.orb.Install(names...)
}

// LocalUninstall removes packages from the local orbital tree.
func (pm *CLIPluginManager) LocalUninstall(names []string) error {
	return pm.orb.Remove(names...)
}

// LocalUpdate updates the named packages, or — when names is empty —
// every installed package. orbital's Update treats "no packages" as
// "everything", which matches the user-facing `formae plugin update`
// (no args) semantics.
func (pm *CLIPluginManager) LocalUpdate(names []string) error {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("failed to refresh local repos", "error", err)
	}
	return pm.orb.Update(names...)
}

// ListInstalled returns the plugin packages installed in the local
// orbital tree. Filtering matches the agent-side plugin manager: only
// packages whose Metadata["display"]["kind"] is "plugin" or "metapackage"
// are considered plugins. The formae binary and other tooling are
// skipped.
func (pm *CLIPluginManager) ListInstalled() ([]apimodel.Plugin, error) {
	pkgs, err := pm.orb.List()
	if err != nil {
		return nil, fmt.Errorf("listing installed packages: %w", err)
	}
	plugins := make([]apimodel.Plugin, 0, len(pkgs))
	for _, pkg := range pkgs {
		if !isPluginPackage(pkg) {
			continue
		}
		plugins = append(plugins, pluginFromPackage(pkg))
	}
	return plugins, nil
}

func isPluginPackage(pkg *records.Package) bool {
	if pkg == nil || pkg.Header == nil {
		return false
	}
	disp, ok := pkg.Metadata["display"]
	if !ok {
		return false
	}
	kind := disp["kind"]
	return kind == "plugin" || kind == "metapackage"
}

func pluginFromPackage(pkg *records.Package) apimodel.Plugin {
	p := apimodel.Plugin{
		Name:        pkg.Name,
		Publisher:   pkg.Publisher,
		License:     pkg.License,
		Summary:     pkg.Summary,
		Description: pkg.Description,
		Frozen:      pkg.Frozen,
		Metadata:    pkg.Metadata,
	}
	if disp, ok := pkg.Metadata["display"]; ok {
		p.Category = disp["category"]
		p.Kind = disp["kind"]
	}
	if pi, ok := pkg.Metadata["plugin"]; ok {
		p.Type = pi["type"]
		p.Namespace = pi["namespace"]
	}
	if pkg.Version != nil {
		p.InstalledVersion = versionString(pkg.Version)
	}
	return p
}

// versionString matches plugin_manager.versionString — orbital's
// Version.Short drops PreRelease/Build, but channels live in PreRelease
// so we always render the full identifier.
func versionString(v *ops.Version) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.PreRelease != "" {
		s += "-" + v.PreRelease
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}
