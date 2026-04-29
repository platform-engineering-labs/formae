// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_manager"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func (s *Server) requirePluginManager(c echo.Context) (*plugin_manager.PluginManager, error) {
	if s.pluginManager == nil {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable, "plugin manager not configured")
	}
	return s.pluginManager, nil
}

func (s *Server) listPluginsHandler(c echo.Context) error {
	pm, err := s.requirePluginManager(c)
	if err != nil {
		return err
	}

	scope := c.QueryParam("scope")
	if scope == "" {
		scope = "installed"
	}

	var plugins []plugin_manager.Plugin
	switch scope {
	case "installed":
		plugins, err = pm.List()
	case "available":
		plugins, err = pm.Available(plugin_manager.AvailableFilter{
			Query:    c.QueryParam("q"),
			Category: c.QueryParam("category"),
			Type:     c.QueryParam("type"),
			Channel:  c.QueryParam("channel"),
		})
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "invalid scope: must be 'installed' or 'available'")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
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

func (s *Server) upgradePluginsHandler(c echo.Context) error {
	pm, err := s.requirePluginManager(c)
	if err != nil {
		return err
	}

	var req apimodel.UpgradePluginsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	pmReq := plugin_manager.UpgradeRequest{
		Packages: toManagerPackageRefs(req.Packages),
		Channel:  req.Channel,
	}
	resp, err := pm.Upgrade(pmReq)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, apimodel.UpgradePluginsResponse{
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
