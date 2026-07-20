// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
)

// CLIPluginManager wraps the local plugin store on this host. When the
// store path is root-owned (the default install layout), orbital's
// tree.New re-execs the CLI under sudo so the store can be mutated
// safely. This type is internal to the CLI; user-facing strings do not
// mention "orbital" or "tree" — those are implementation details.
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
func NewCLIPluginManager(logger *slog.Logger, repos []pkgmodel.Repository, channel string, sudo bool, writable bool) (*CLIPluginManager, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	orb, err := opsmgr.NewFromRepositories(logger, repos, channel, sudo, writable)
	if err != nil {
		return nil, err
	}
	if !orb.Ready() {
		return nil, fmt.Errorf("plugin store at %s is not initialized; install formae from the official installer, or set %s to point at an existing install (e.g. /opt/pel)", orb.Path(), opsmgr.FormaePelRootEnv)
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

// LocalSearch returns plugins available for installation from the
// configured repositories, filtered by query/category/type. Mirrors the
// agent-side PluginManager.Available shape (sorted by name, with
// AvailableVersions populated) so the CLI can stand alone without an
// agent.
func (pm *CLIPluginManager) LocalSearch(query, category, typ string) ([]apimodel.Plugin, error) {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("refresh failed, using cached repository data", "error", err)
	}
	avail, err := pm.orb.Available()
	if err != nil {
		return nil, fmt.Errorf("querying available packages: %w", err)
	}

	plugins := make([]apimodel.Plugin, 0, len(avail))
	for _, status := range avail {
		if status == nil || len(status.Available) == 0 {
			continue
		}
		var src *records.Package
		for _, pkg := range status.Available {
			if isPluginPackage(pkg) {
				src = pkg
				break
			}
		}
		if src == nil {
			continue
		}
		p := pluginFromPackage(src)
		p.InstalledVersion = installedVersionOf(status.Available)
		p.AvailableVersions = uniqueVersions(status.Available)
		if matchesPluginFilter(p, query, category, typ) {
			plugins = append(plugins, p)
		}
	}
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})
	return plugins, nil
}

// LocalInfo returns detailed information about a single plugin from the
// configured repositories, or nil if no candidate carries plugin
// metadata or the package is unknown. Mirrors the agent-side
// PluginManager.Info.
func (pm *CLIPluginManager) LocalInfo(name string) (*apimodel.Plugin, error) {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("refresh failed, using cached repository data", "error", err)
	}
	status, err := pm.orb.AvailableFor(name)
	if err != nil {
		// orbital's AvailableFor returns "no available packages for: <name>"
		// when the package isn't in any of the requested channel's repos.
		// That's a normal not-found, not an internal error.
		if strings.Contains(err.Error(), "no available packages for") {
			return nil, nil
		}
		return nil, fmt.Errorf("querying package info for %s: %w", name, err)
	}
	if status == nil || len(status.Available) == 0 {
		return nil, nil
	}

	var src *records.Package
	for _, pkg := range status.Available {
		if isPluginPackage(pkg) {
			src = pkg
			break
		}
	}
	if src == nil {
		return nil, nil
	}
	p := pluginFromPackage(src)
	p.InstalledVersion = installedVersionOf(status.Available)
	p.AvailableVersions = uniqueVersions(status.Available)
	return &p, nil
}

// installedVersionOf returns the version string of the package marked
// Installed in pkgs, or "" if none is installed. Lets search/info show
// the user which available version is currently on disk.
func installedVersionOf(pkgs []*records.Package) string {
	for _, pkg := range pkgs {
		if pkg != nil && pkg.Installed && pkg.Version != nil {
			return versionString(pkg.Version)
		}
	}
	return ""
}

// uniqueVersions returns the distinct version strings from pkgs in
// input order. Orbital can surface the same package twice when it
// lives both in the local tree (after install) and in a configured
// repo, so a naive append would produce duplicates.
func uniqueVersions(pkgs []*records.Package) []string {
	seen := make(map[string]bool, len(pkgs))
	versions := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		if pkg == nil || pkg.Header == nil || pkg.Version == nil {
			continue
		}
		v := versionString(pkg.Version)
		if seen[v] {
			continue
		}
		seen[v] = true
		versions = append(versions, v)
	}
	return versions
}

// matchesPluginFilter returns true if p satisfies every non-empty
// search filter. query matches the lowercased
// name+summary+description haystack.
func matchesPluginFilter(p apimodel.Plugin, query, category, typ string) bool {
	if category != "" && p.Category != category {
		return false
	}
	if typ != "" && p.Type != typ {
		return false
	}
	if query != "" {
		q := strings.ToLower(query)
		haystack := strings.ToLower(p.Name + " " + p.Summary + " " + p.Description)
		if !strings.Contains(haystack, q) {
			return false
		}
	}
	return true
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
