// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/orbital/opm/records"
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
type Plugin struct {
	Name              string
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
	Metadata          map[string]map[string]string
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
type InstallRequest struct{ Packages []PackageRef }

// UninstallRequest is the input to Uninstall.
type UninstallRequest struct{ Packages []PackageRef }

// UpgradeRequest is the input to Upgrade.
type UpgradeRequest struct{ Packages []PackageRef }

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

// List returns every installed plugin.
func (pm *PluginManager) List() ([]Plugin, error) {
	pkgs, err := pm.orb.List()
	if err != nil {
		return nil, fmt.Errorf("listing installed packages: %w", err)
	}
	plugins := make([]Plugin, 0, len(pkgs))
	for _, pkg := range pkgs {
		plugins = append(plugins, pluginFromPackage(pkg))
	}
	return plugins, nil
}

// Available returns plugins available for installation, filtered by f.
func (pm *PluginManager) Available(f AvailableFilter) ([]Plugin, error) {
	if err := pm.orb.Refresh(); err != nil {
		pm.logger.Warn("refresh failed, using cached repository data", "error", err)
	}

	avail, err := pm.orb.Available()
	if err != nil {
		return nil, fmt.Errorf("querying available packages: %w", err)
	}

	plugins := make([]Plugin, 0, len(avail))
	for _, status := range avail {
		if len(status.Available) == 0 {
			continue
		}
		p := pluginFromPackage(status.Available[0])
		// Collect all available versions.
		versions := make([]string, 0, len(status.Available))
		for _, pkg := range status.Available {
			if pkg.Header != nil && pkg.Version != nil {
				versions = append(versions, pkg.Version.Short())
			}
		}
		p.AvailableVersions = versions
		if matchesFilter(p, f) {
			plugins = append(plugins, p)
		}
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})
	return plugins, nil
}

// Info returns detailed information about a single package, or nil if it is
// not found in any configured repository.
func (pm *PluginManager) Info(name string) (*Plugin, error) {
	status, err := pm.orb.AvailableFor(name)
	if err != nil {
		return nil, fmt.Errorf("querying package info for %s: %w", name, err)
	}
	if status == nil || len(status.Available) == 0 {
		return nil, nil
	}

	p := pluginFromPackage(status.Available[0])
	versions := make([]string, 0, len(status.Available))
	for _, pkg := range status.Available {
		if pkg.Header != nil && pkg.Version != nil {
			versions = append(versions, pkg.Version.Short())
		}
	}
	p.AvailableVersions = versions
	return &p, nil
}

// Install installs the requested packages via orbital.
func (pm *PluginManager) Install(req InstallRequest) (Response, error) {
	specs := packageSpecs(req.Packages)
	if err := pm.orb.Install(specs...); err != nil {
		return Response{}, fmt.Errorf("installing packages: %w", err)
	}
	return pm.buildResponse(req.Packages, "install")
}

// Uninstall removes the requested packages via orbital.
func (pm *PluginManager) Uninstall(req UninstallRequest) (Response, error) {
	names := make([]string, len(req.Packages))
	for i, p := range req.Packages {
		names[i] = p.Name
	}
	if err := pm.orb.Remove(names...); err != nil {
		return Response{}, fmt.Errorf("removing packages: %w", err)
	}
	return pm.buildResponse(req.Packages, "remove")
}

// Upgrade updates the requested packages via orbital.
func (pm *PluginManager) Upgrade(req UpgradeRequest) (Response, error) {
	specs := packageSpecs(req.Packages)
	if err := pm.orb.Update(specs...); err != nil {
		return Response{}, fmt.Errorf("upgrading packages: %w", err)
	}
	return pm.buildResponse(req.Packages, "install")
}

// buildResponse constructs a Response by querying the post-action state.
func (pm *PluginManager) buildResponse(refs []PackageRef, action string) (Response, error) {
	var resp Response
	resp.RequiresRestart = true
	for _, ref := range refs {
		op := Operation{
			Name:    ref.Name,
			Version: ref.Version,
			Action:  action,
		}
		// Try to fill in type and resolved version from the repo metadata.
		if status, err := pm.orb.AvailableFor(ref.Name); err == nil && status != nil && len(status.Available) > 0 {
			pkg := status.Available[0]
			if pi, ok := pkg.Metadata["plugin"]; ok {
				op.Type = pi["type"]
			}
			if op.Version == "" && pkg.Version != nil {
				op.Version = pkg.Version.Short()
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

// pluginFromPackage converts an orbital records.Package to a Plugin.
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
	if pkg.Version != nil {
		p.InstalledVersion = pkg.Version.Short()
	}
	if disp, ok := pkg.Metadata["display"]; ok {
		p.Category = disp["category"]
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
