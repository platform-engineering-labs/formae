// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_manager"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/discovery"
)

func (s *Server) requirePluginManager(c echo.Context) (*plugin_manager.PluginManager, error) {
	if s.pluginManager == nil {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable, "plugin manager not configured")
	}
	return s.pluginManager, nil
}

// discoverLocalPluginPaths scans the configured plugin dirs and returns
// a map from lowercase plugin name to the absolute path of the plugin's
// PklProject file. Mirrors PluginManager.DiscoverLocalPaths, but lives
// in api so the installed-plugin listing path can serve LocalPath
// without depending on an orbital-backed PluginManager.
func discoverLocalPluginPaths(pluginDirs []string) map[string]string {
	if len(pluginDirs) == 0 {
		return map[string]string{}
	}
	infos := discovery.DiscoverPluginsMulti(pluginDirs, discovery.Resource)
	authInfos := discovery.DiscoverPluginsMulti(pluginDirs, discovery.Auth)
	infos = append(infos, authInfos...)
	out := make(map[string]string, len(infos))
	for _, info := range infos {
		base := filepath.Dir(info.BinaryPath)
		pklProject := filepath.Join(base, "schema", "pkl", "PklProject")
		if _, err := os.Stat(pklProject); err != nil {
			continue
		}
		out[strings.ToLower(info.Name)] = pklProject
	}
	return out
}

func (s *Server) listPluginsHandler(c echo.Context) error {
	scope := c.QueryParam("scope")
	if scope == "" {
		scope = "installed"
	}

	var plugins []plugin_manager.Plugin
	switch scope {
	case "installed":
		// Installed listing is served from the in-process plugin
		// registry (populated by PluginProcessSupervisor at startup) and
		// a filesystem scan for PklProject paths. No orbital is required,
		// so the endpoint stays usable even when /opt/pel is root-owned
		// and the agent runs unprivileged.
		localPaths := discoverLocalPluginPaths(s.pluginDirs)
		if s.pluginManager != nil {
			pmPlugins, err := s.pluginManager.ListWithLocalPaths(localPaths)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
			plugins = pmPlugins
		}
		registered, regErr := s.metastructure.RegisteredPlugins()
		if regErr != nil {
			c.Logger().Warnf("plugin registry lookup failed: %v", regErr)
			registered = nil
		}
		plugins = mergeRegisteredPlugins(plugins, registered, localPaths)
	case "available":
		pm, err := s.requirePluginManager(c)
		if err != nil {
			return err
		}
		availablePlugins, err := pm.Available(plugin_manager.AvailableFilter{
			Query:    c.QueryParam("q"),
			Category: c.QueryParam("category"),
			Type:     c.QueryParam("type"),
			Channel:  c.QueryParam("channel"),
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		plugins = availablePlugins
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "invalid scope: must be 'installed' or 'available'")
	}

	return c.JSON(http.StatusOK, apimodel.ListPluginsResponse{
		Plugins: toAPIPlugins(plugins),
	})
}

func (s *Server) getPluginHandler(c echo.Context) error {
	pm, err := s.requirePluginManager(c)
	if err != nil {
		return err
	}

	name := c.Param("name")
	channel := c.QueryParam("channel")
	p, err := pm.Info(name, channel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if p == nil {
		return apiError(c, http.StatusNotFound, apimodel.PluginNotFound, apimodel.PluginNotFoundError{Name: name})
	}
	return c.JSON(http.StatusOK, apimodel.GetPluginResponse{
		Plugin: toAPIPlugin(*p),
	})
}

func (s *Server) installPluginsHandler(c echo.Context) error {
	pm, err := s.requirePluginManager(c)
	if err != nil {
		return err
	}

	var req apimodel.InstallPluginsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if len(req.Packages) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "packages list is required")
	}

	pmReq := plugin_manager.InstallRequest{
		Packages: toManagerPackageRefs(req.Packages),
		Channel:  req.Channel,
	}
	resp, err := pm.Install(pmReq)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, apimodel.InstallPluginsResponse{
		Operations:      toAPIOperations(resp.Operations),
		RequiresRestart: resp.RequiresRestart,
		Warnings:        resp.Warnings,
	})
}

func (s *Server) uninstallPluginsHandler(c echo.Context) error {
	pm, err := s.requirePluginManager(c)
	if err != nil {
		return err
	}

	var req apimodel.UninstallPluginsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if len(req.Packages) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "packages list is required")
	}

	pmReq := plugin_manager.UninstallRequest{
		Packages: toManagerPackageRefs(req.Packages),
	}
	resp, err := pm.Uninstall(pmReq)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, apimodel.UninstallPluginsResponse{
		Operations:      toAPIOperations(resp.Operations),
		RequiresRestart: resp.RequiresRestart,
		Warnings:        resp.Warnings,
	})
}

func (s *Server) updatePluginsHandler(c echo.Context) error {
	pm, err := s.requirePluginManager(c)
	if err != nil {
		return err
	}

	var req apimodel.UpdatePluginsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	pmReq := plugin_manager.UpdateRequest{
		Packages: toManagerPackageRefs(req.Packages),
		Channel:  req.Channel,
	}
	resp, err := pm.Update(pmReq)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, apimodel.UpdatePluginsResponse{
		Operations:      toAPIOperations(resp.Operations),
		RequiresRestart: resp.RequiresRestart,
		Warnings:        resp.Warnings,
	})
}

// Conversion helpers

func toAPIPlugins(plugins []plugin_manager.Plugin) []apimodel.Plugin {
	result := make([]apimodel.Plugin, 0, len(plugins))
	for _, p := range plugins {
		result = append(result, toAPIPlugin(p))
	}
	return result
}

func toAPIPlugin(p plugin_manager.Plugin) apimodel.Plugin {
	return apimodel.Plugin{
		Name:              p.Name,
		Kind:              p.Kind,
		Type:              p.Type,
		Namespace:         p.Namespace,
		Category:          p.Category,
		Summary:           p.Summary,
		Description:       p.Description,
		Publisher:         p.Publisher,
		License:           p.License,
		InstalledVersion:  p.InstalledVersion,
		AvailableVersions: p.AvailableVersions,
		LocalPath:         p.LocalPath,
		Channel:           p.Channel,
		Frozen:            p.Frozen,
		ManagedBy:         p.ManagedBy,
		Metadata:          p.Metadata,
	}
}

func toAPIOperations(ops []plugin_manager.Operation) []apimodel.PluginOperation {
	result := make([]apimodel.PluginOperation, 0, len(ops))
	for _, op := range ops {
		result = append(result, apimodel.PluginOperation{
			Name:    op.Name,
			Type:    op.Type,
			Version: op.Version,
			Action:  op.Action,
		})
	}
	return result
}

func toManagerPackageRefs(refs []apimodel.PackageRef) []plugin_manager.PackageRef {
	result := make([]plugin_manager.PackageRef, 0, len(refs))
	for _, r := range refs {
		result = append(result, plugin_manager.PackageRef{Name: r.Name, Version: r.Version})
	}
	return result
}

// mergeRegisteredPlugins appends synthetic Plugin entries for any
// registered plugin not already represented in the orbital list — the
// `make install` case. Dedupe is by plugin name (multiple plugins may
// share a namespace).
func mergeRegisteredPlugins(orbital []plugin_manager.Plugin, registered []messages.RegisteredPluginInfo, paths map[string]string) []plugin_manager.Plugin {
	seen := make(map[string]bool, len(orbital))
	for _, p := range orbital {
		if name := strings.ToLower(p.Name); name != "" {
			seen[name] = true
		}
	}
	for _, r := range registered {
		name := strings.ToLower(r.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		orbital = append(orbital, plugin_manager.Plugin{
			Name:             r.Name,
			Kind:             "plugin",
			Type:             "resource",
			Namespace:        r.Namespace,
			InstalledVersion: r.Version,
			LocalPath:        paths[name],
		})
	}
	return orbital
}
