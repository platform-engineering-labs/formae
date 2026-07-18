// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package project

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/util"
)

// pluginChoice is a single entry in the multi-select list.
type pluginChoice struct {
	// Name is the plugin namespace (or name when namespace is empty).
	Name string
	// Description is a human-readable label from the agent; empty for locally-scanned entries.
	Description string
	// Local indicates the entry came from the local plugin-dir scan rather than the agent.
	Local bool
}

// Package-level seams so tests can replace the agent call and the local scan
// without a real agent or filesystem.
var (
	isInteractive      = tui.IsInteractive
	runConfirm         = components.RunConfirm
	runMultiSelect     = defaultRunMultiSelect
	installedPluginsFn = defaultInstalledPlugins
	localScanFn        = defaultLocalScan
)

// defaultInstalledPlugins queries the agent for installed resource plugins.
// Returns (name→description) keyed by namespace (or name).
func defaultInstalledPlugins(a *app.App) (map[string]string, error) {
	plugins, err := a.InstalledResourcePlugins()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(plugins))
	for ns, info := range plugins {
		// InstalledResourcePlugins returns PluginInfo{Version, LocalPath};
		// there is no Description field in PluginInfo. We use the namespace
		// as the display name; description is left empty.
		_ = info
		result[ns] = ""
	}
	return result, nil
}

// defaultLocalScan scans pluginsDir for plugin subdirectories.
// Returns (name→description); description is always empty for local entries.
func defaultLocalScan(pluginsDir string) (map[string]string, error) {
	expanded := util.ExpandHomePath(pluginsDir)
	entries, err := os.ReadDir(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	result := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			result[strings.ToLower(e.Name())] = ""
		}
	}
	return result, nil
}

// pluginChoices resolves the list to present in the multi-select.
// It tries the agent first (via installedPluginsFn); on error it falls
// back to the local plugin-dir scan (via localScanFn) with Local=true.
// Returns the ordered choices, whether they came from the local scan, and any
// hard error (both sources failed or returned nothing is not a hard error).
func pluginChoices(a *app.App, pluginsDir string) (choices []pluginChoice, fromLocalScan bool, err error) {
	// Agent-first.
	agentPlugins, agentErr := installedPluginsFn(a)
	if agentErr == nil && len(agentPlugins) > 0 {
		for ns, desc := range agentPlugins {
			choices = append(choices, pluginChoice{Name: ns, Description: desc})
		}
		sortChoices(choices)
		return choices, false, nil
	}

	// Fall back to local scan.
	localPlugins, localErr := localScanFn(pluginsDir)
	if localErr != nil {
		// Return a combined error only when the agent also failed.
		if agentErr != nil {
			return nil, false, fmt.Errorf("agent unavailable (%v); local scan also failed: %w", agentErr, localErr)
		}
		return nil, false, localErr
	}

	for ns, desc := range localPlugins {
		choices = append(choices, pluginChoice{Name: ns, Description: desc, Local: true})
	}
	sortChoices(choices)
	return choices, true, nil
}

// sortChoices sorts choices alphabetically by Name for stable display.
func sortChoices(choices []pluginChoice) {
	for i := 1; i < len(choices); i++ {
		for j := i; j > 0 && choices[j].Name < choices[j-1].Name; j-- {
			choices[j], choices[j-1] = choices[j-1], choices[j]
		}
	}
}

// choiceLabel returns the display string for a pluginChoice in the multi-select.
func choiceLabel(c pluginChoice) string {
	if c.Description != "" {
		return fmt.Sprintf("%-16s %s", c.Name, c.Description)
	}
	return c.Name
}

// defaultRunMultiSelect runs a huh multi-select inside a themed form.
// Returns the selected plugin names (not suffixed) and any error.
func defaultRunMultiSelect(th *theme.Theme, choices []pluginChoice, fromLocalScan bool, schema string) ([]string, error) {
	title := "Select resource plugins to include:"
	if fromLocalScan {
		title += " (from local plugin directory)"
	}

	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(choiceLabel(c), c.Name)
	}

	var selected []string
	ms := huh.NewMultiSelect[string]().
		Title(title).
		Description("Schema: " + schema).
		Options(opts...).
		Value(&selected)

	form := components.NewThemedForm(th, huh.NewGroup(ms))
	if err := form.Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

// runPluginSelect runs the full interactive plugin-select flow:
//  1. Resolve choices (agent-first, local fallback).
//  2. If no choices at all: show the "no installed plugins" confirm (R17).
//  3. Otherwise: run multi-select; if nothing selected, show consequence confirm.
//
// Returns the include values to pass to projects.Init (may be empty).
// Returns (nil, nil) when the user confirms "continue with no plugins".
// Returns a non-nil error when the user aborts or a hard error occurs.
func runPluginSelect(th *theme.Theme, a *app.App, pluginsDir string) ([]string, error) {
	choices, fromLocalScan, err := pluginChoices(a, pluginsDir)
	if err != nil {
		return nil, err
	}

	// R17: empty plugin list → distinct confirm, then proceed with no plugins.
	if len(choices) == 0 {
		if !isInteractive() {
			// Non-TTY: caller handles --yes / error path; we should not be
			// called, but be safe.
			return []string{}, nil
		}
		ok, err := runConfirm(th,
			"No installed plugins found.",
			"The plugin directory is empty and the agent is unavailable (or has no resource plugins installed). You can add plugins later with --include. Continue?",
		)
		if err != nil || !ok {
			return nil, fmt.Errorf("project initialization aborted")
		}
		return []string{}, nil
	}

	// Run multi-select.
	selected, err := runMultiSelect(th, choices, fromLocalScan, "")
	if err != nil {
		return nil, err
	}

	// Nothing selected → consequence confirm (View 3 copy).
	if len(selected) == 0 {
		if !isInteractive() {
			return []string{}, nil
		}
		ok, err := runConfirm(th,
			"No plugins selected.",
			"The project will not include any resource plugin schemas. You can add plugins later with --include. Continue?",
		)
		if err != nil || !ok {
			return nil, fmt.Errorf("project initialization aborted")
		}
		return []string{}, nil
	}

	// Build include values: local-sourced selections get the @local suffix.
	localSet := make(map[string]bool, len(choices))
	for _, c := range choices {
		if c.Local {
			localSet[c.Name] = true
		}
	}

	includes := make([]string, 0, len(selected))
	for _, name := range selected {
		if localSet[name] {
			includes = append(includes, name+"@local")
		} else {
			includes = append(includes, name)
		}
	}
	return includes, nil
}
