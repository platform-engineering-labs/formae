// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/discovery"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
)

// orbitalClient abstracts the orbital Manager for testing.
type orbitalClient interface {
	Refresh() error
	Install(packages ...string) error
	Remove(packages ...string) error
	Update(packages ...string) error
	Ready() bool
	List() ([]*records.Package, error)
	Available() (map[string]*records.Status, error)
	AvailableFor(name string) (*records.Status, error)
}

// Plugin describes a single plugin from the manager's perspective.
//
// Kind distinguishes regular plugins ("plugin") from curated bundles
// ("metapackage") and is taken straight from Metadata["display"]["kind"].
// Type is only meaningful for kind=="plugin" and reflects the plugin's
// runtime role (resource | auth); it is empty for metapackages.
type Plugin struct {
	Name              string
	Kind              string // "plugin" | "metapackage"
	Type              string // "resource" | "auth"
	Namespace         string
	Category          string
	Summary           string
	Description       string
	Publisher         string
	License           string
	InstalledVersion  string
	AvailableVersions []string
	Channel           string
	Frozen            bool
	ManagedBy         string // "standard" | ""
	// LocalPath is the absolute path to the plugin's PklProject file on
	// the agent's filesystem, populated from the discovery scan. Empty
	// when no on-disk install is found (e.g. orbital recorded the
	// package but the binary tree was wiped, or the plugin is only
	// visible in an unscanned location). Surfaced via the API so the
	// CLI can build local schema URIs for --schema-location local.
	LocalPath string
	Metadata  map[string]map[string]string
}

// AvailableFilter constrains the set of plugins returned by Available.
type AvailableFilter struct {
	Query    string
	Category string
	Type     string
	Channel  string
}

// PackageRef identifies a package by name and optional version.
type PackageRef struct {
	Name    string
	Version string
}

// Operation describes a single action taken on a package.
type Operation struct {
	Name    string
	Type    string
	Version string
	Action  string // "install" | "remove" | "noop"
}

// Response is the result of an Install, Uninstall, or Upgrade call.
type Response struct {
	Operations      []Operation
	RequiresRestart bool
	Warnings        []string
}

// InstallRequest is the input to Install.
type InstallRequest struct {
	Packages []PackageRef
	Channel  string
}

// UninstallRequest is the input to Uninstall.
type UninstallRequest struct{ Packages []PackageRef }

// UpgradeRequest is the input to Upgrade.
type UpgradeRequest struct {
	Packages []PackageRef
	Channel  string
}

// DefaultChannel is the channel queried when none is specified explicitly.
// Operators publishing to a different channel must opt in via --channel.
const DefaultChannel = "stable"

// orbitalFactory builds an orbitalClient for the requested channel. When the
// channel string is non-empty, the URI fragments of every configured repo are
// overridden with that channel before the manager is constructed.
type orbitalFactory func(channel string) (orbitalClient, error)

// PluginManager manages plugin lifecycle operations via orbital.
//
// listOrb is the long-lived client used for inspecting locally-installed
// packages (List/Uninstall) — those operations are channel-agnostic. Per-call
// factory invocations build channel-specific clients for queries that depend
// on a remote channel (Available/Info/Install/Upgrade).
//
// pluginDirs are the directories the agent scans for on-disk plugin
// installs. The same dirs the agent scans at startup to populate the
// PluginProcessSupervisor; the manager re-scans them on List() to
// attach LocalPath to each plugin. Order matters: dirs earlier in the
// slice take priority when the same plugin is found in multiple dirs
// (mirrors discovery.DiscoverPluginsMulti).
type PluginManager struct {
	logger     *slog.Logger
	listOrb    orbitalClient
	factory    orbitalFactory
	pluginDirs []string
}

// New creates a PluginManager backed by the orbital repositories in repos.
// Both formae-plugin and binary repos are loaded (the solver needs both for
// cross-repo dependency resolution, e.g. plugin depends on formae >= 0.85);
// the per-package "is this a plugin?" classification is done at query time
// via Metadata["plugin"] so that binary-only packages (formae itself, pkl
// tooling, etc.) don't surface through List, Available, or Info.
//
// Returns an error if the underlying orbital tree is not initialized at the
// derived tree-root path. The agent treats this as a fatal startup error so
// the operator gets a clear log line rather than CLI users seeing opaque
// 503s on every plugin command.
func New(logger *slog.Logger, repos []pkgmodel.Repository, pluginDirs []string) (*PluginManager, error) {
	factory := func(channel string) (orbitalClient, error) {
		return opsmgr.NewFromRepositories(logger, repos, channel)
	}
	listOrb, err := factory("")
	if err != nil {
		return nil, err
	}
	if !listOrb.Ready() {
		return nil, fmt.Errorf("orbital tree not initialized at the path derived from the formae binary location; install formae via the orbital installer or set up an orbital tree manually")
	}
	// Warm orbital's repository cache at startup. Without this, the first
	// plugin install on a fresh tree fails with "no install candidates
	// found" because the install endpoint doesn't auto-refresh — only
	// Available does. Best-effort: a refresh failure (e.g. hub unreachable)
	// must not block agent startup; subsequent operations will retry.
	if err := listOrb.Refresh(); err != nil {
		logger.Warn("orbital cache refresh failed at startup; plugin operations will rely on cached data until the next refresh", "error", err)
	}
	return &PluginManager{logger: logger, listOrb: listOrb, factory: factory, pluginDirs: pluginDirs}, nil
}

// clientFor returns an orbitalClient configured for the given channel. An
// empty channel resolves to DefaultChannel so callers don't need to special-
// case the default.
func (pm *PluginManager) clientFor(channel string) (orbitalClient, error) {
	if channel == "" {
		channel = DefaultChannel
	}
	return pm.factory(channel)
}

// isPluginPackage reports whether pkg is a formae plugin or a curated
// metapackage. The classification is purely metadata-driven via
// Metadata["display"]["kind"]: regular plugins set kind="plugin", curated
// bundles set kind="metapackage". Binary-only tooling (the formae binary
// itself, pkl) has no display.kind and is filtered out.
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

// versionString renders a Version as Major.Minor.Patch with PreRelease and
// Build appended in semver-2.0 form. Orbital's Version.Short calls Semver
// which drops PreRelease and Build, so e.g. 0.1.0-dev.1 would render as
// 0.1.0 — we always want the full identifier so users can tell channels
// apart at a glance.
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

// List returns every installed plugin. Packages without "plugin" metadata
// (e.g. the formae binary itself, pkl tooling) are filtered out.
//
// Each plugin's LocalPath is populated from a fresh discovery scan of the
// configured pluginDirs. A plugin known to orbital but missing from the
// scan dirs (e.g. a record persisted before the binary tree moved) gets
// LocalPath="" — consumers should treat that as "no on-disk install
// available".
func (pm *PluginManager) List() ([]Plugin, error) {
	pkgs, err := pm.listOrb.List()
	if err != nil {
		return nil, fmt.Errorf("listing installed packages: %w", err)
	}
	localPaths := pm.discoverLocalPaths()
	plugins := make([]Plugin, 0, len(pkgs))
	for _, pkg := range pkgs {
		if !isPluginPackage(pkg) {
			continue
		}
		p := pluginFromPackage(pkg)
		if pkg.Version != nil {
			p.InstalledVersion = versionString(pkg.Version)
		}
		if path, ok := localPaths[strings.ToLower(p.Name)]; ok {
			p.LocalPath = path
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}

// discoverLocalPaths scans the configured plugin dirs and returns a map
// from lowercase plugin name to the absolute path of the plugin's
// PklProject file. Errors during scanning are logged and the partial
// result is returned; an empty map means no on-disk plugins were found
// (which is a valid state, not an error).
func (pm *PluginManager) discoverLocalPaths() map[string]string {
	if len(pm.pluginDirs) == 0 {
		return map[string]string{}
	}
	infos := discovery.DiscoverPluginsMulti(pm.pluginDirs, discovery.Resource)
	authInfos := discovery.DiscoverPluginsMulti(pm.pluginDirs, discovery.Auth)
	infos = append(infos, authInfos...)
	out := make(map[string]string, len(infos))
	for _, info := range infos {
		// info.BinaryPath is `<dir>/<name>/v<ver>/<binary>`; the schema
		// PklProject lives at `<dir>/<name>/v<ver>/schema/pkl/PklProject`.
		base := filepath.Dir(info.BinaryPath)
		pklProject := filepath.Join(base, "schema", "pkl", "PklProject")
		if _, err := os.Stat(pklProject); err != nil {
			// Plugin binary exists but no PKL schema next to it (rare;
			// some auth plugins, or a half-broken install). Skip — the
			// CLI will fall back to remote URIs for this namespace.
			continue
		}
		out[strings.ToLower(info.Name)] = pklProject
	}
	return out
}

// Available returns plugins available for installation, filtered by f.
// Packages without "plugin" metadata are excluded. The channel resolves to
// DefaultChannel when f.Channel is empty so callers see only stable packages
// unless they opt in.
func (pm *PluginManager) Available(f AvailableFilter) ([]Plugin, error) {
	orb, err := pm.clientFor(f.Channel)
	if err != nil {
		return nil, fmt.Errorf("building orbital client for channel: %w", err)
	}
	if err := orb.Refresh(); err != nil {
		pm.logger.Warn("refresh failed, using cached repository data", "error", err)
	}

	avail, err := orb.Available()
	if err != nil {
		return nil, fmt.Errorf("querying available packages: %w", err)
	}

	plugins := make([]Plugin, 0, len(avail))
	for _, status := range avail {
		if len(status.Available) == 0 {
			continue
		}
		// Use the first plugin candidate as the descriptive package; if no
		// candidate carries plugin metadata, skip this Status entirely.
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
		if matchesFilter(p, f) {
			plugins = append(plugins, p)
		}
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})
	return plugins, nil
}

// installedVersionOf returns the version short-string of the package marked
// Installed in pkgs, or "" if none is installed.
func installedVersionOf(pkgs []*records.Package) string {
	for _, pkg := range pkgs {
		if pkg != nil && pkg.Installed && pkg.Version != nil {
			return versionString(pkg.Version)
		}
	}
	return ""
}

// Info returns detailed information about a single package, or nil if it is
// not found in any configured repository or doesn't carry plugin metadata.
// channel selects which channel to query; empty resolves to DefaultChannel.
// The orbital cache is refreshed before the lookup so callers don't see stale
// versions after a recent publish.
func (pm *PluginManager) Info(name, channel string) (*Plugin, error) {
	orb, err := pm.clientFor(channel)
	if err != nil {
		return nil, fmt.Errorf("building orbital client for channel: %w", err)
	}
	if err := orb.Refresh(); err != nil {
		pm.logger.Warn("refresh failed, using cached repository data", "error", err)
	}
	status, err := orb.AvailableFor(name)
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

// uniqueVersions returns the distinct version strings from pkgs in input
// order. Orbital can surface the same package twice when it lives both in
// the local tree (after install) and in a configured repo, so a naive
// append would produce duplicates like "0.1.0-dev.1, 0.1.0-dev.1".
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

// Install installs the requested packages via orbital. req.Channel selects
// which channel to install from; empty resolves to DefaultChannel.
func (pm *PluginManager) Install(req InstallRequest) (Response, error) {
	orb, err := pm.clientFor(req.Channel)
	if err != nil {
		return Response{}, fmt.Errorf("building orbital client for channel: %w", err)
	}
	specs := packageSpecs(req.Packages)
	if err := orb.Install(specs...); err != nil {
		return Response{}, fmt.Errorf("installing packages: %w", err)
	}
	return pm.buildResponse(orb, req.Packages, "install")
}

// Uninstall removes the requested packages via orbital. Uninstall operates on
// already-installed packages and is therefore channel-agnostic.
func (pm *PluginManager) Uninstall(req UninstallRequest) (Response, error) {
	names := make([]string, len(req.Packages))
	for i, p := range req.Packages {
		names[i] = p.Name
	}
	if err := pm.listOrb.Remove(names...); err != nil {
		return Response{}, fmt.Errorf("removing packages: %w", err)
	}
	return pm.buildResponse(pm.listOrb, req.Packages, "remove")
}

// Upgrade updates the requested packages via orbital. req.Channel selects
// which channel to upgrade against; empty resolves to DefaultChannel.
func (pm *PluginManager) Upgrade(req UpgradeRequest) (Response, error) {
	orb, err := pm.clientFor(req.Channel)
	if err != nil {
		return Response{}, fmt.Errorf("building orbital client for channel: %w", err)
	}
	specs := packageSpecs(req.Packages)
	if err := orb.Update(specs...); err != nil {
		return Response{}, fmt.Errorf("upgrading packages: %w", err)
	}
	return pm.buildResponse(orb, req.Packages, "install")
}

// buildResponse constructs a Response by querying the post-action state via
// the same orbital client that performed the action.
func (pm *PluginManager) buildResponse(orb orbitalClient, refs []PackageRef, action string) (Response, error) {
	var resp Response
	resp.RequiresRestart = true
	for _, ref := range refs {
		op := Operation{
			Name:    ref.Name,
			Version: ref.Version,
			Action:  action,
		}
		// Try to fill in type and resolved version from the repo metadata.
		if status, err := orb.AvailableFor(ref.Name); err == nil && status != nil && len(status.Available) > 0 {
			pkg := status.Available[0]
			if pi, ok := pkg.Metadata["plugin"]; ok {
				op.Type = pi["type"]
			}
			if op.Version == "" && pkg.Version != nil {
				op.Version = versionString(pkg.Version)
			}
		}
		resp.Operations = append(resp.Operations, op)
	}
	return resp, nil
}

// packageSpecs converts PackageRefs to orbital spec strings ("name" or "name@version").
func packageSpecs(refs []PackageRef) []string {
	specs := make([]string, len(refs))
	for i, r := range refs {
		if r.Version != "" {
			specs[i] = r.Name + "@" + r.Version
		} else {
			specs[i] = r.Name
		}
	}
	return specs
}

// pluginFromPackage converts an orbital records.Package to a Plugin's
// descriptive fields. It deliberately does NOT populate InstalledVersion —
// that's a per-call concern: List sets it directly from the package (every
// package returned by orb.List() is installed by definition), while
// Available/Info derive it from records.Package.Installed via
// installedVersionOf so the field reflects orbital's actual state rather
// than the first available candidate.
func pluginFromPackage(pkg *records.Package) Plugin {
	p := Plugin{
		Frozen: pkg.Frozen,
	}
	if pkg.Header == nil {
		return p
	}
	p.Name = pkg.Name
	p.Publisher = pkg.Publisher
	p.License = pkg.License
	p.Summary = pkg.Summary
	p.Description = pkg.Description
	p.Metadata = pkg.Metadata
	if disp, ok := pkg.Metadata["display"]; ok {
		p.Category = disp["category"]
		p.Kind = disp["kind"]
	}
	if pi, ok := pkg.Metadata["plugin"]; ok {
		p.Type = pi["type"]
		p.Namespace = pi["namespace"]
	}
	return p
}

// matchesFilter returns true if p satisfies every non-empty field in f.
func matchesFilter(p Plugin, f AvailableFilter) bool {
	if f.Category != "" && p.Category != f.Category {
		return false
	}
	if f.Type != "" && p.Type != f.Type {
		return false
	}
	if f.Query != "" {
		q := strings.ToLower(f.Query)
		haystack := strings.ToLower(p.Name + " " + p.Summary + " " + p.Description)
		if !strings.Contains(haystack, q) {
			return false
		}
	}
	return true
}
