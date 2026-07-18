// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

// SearchRenderOpts carries the optional filter echo parameters for renderPluginSearch.
type SearchRenderOpts struct {
	Query    string
	Category string
	Type     string
}

// renderPluginList groups the installed plugins by kind and renders one
// row per plugin (✓ name version, optionally annotated with the bundle
// they came from). The list is sourced from a single local read of the
// orbital tree; there is no agent/CLI divergence to surface here.
func renderPluginList(th *theme.Theme, plugins []apimodel.Plugin) string {
	if len(plugins) == 0 {
		subtleStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		return "No plugins installed.\n" +
			subtleStyle.Render("Search available plugins: formae plugin search <term>") + "\n"
	}

	// Group by kind/type. Bundles get their own section so users can see
	// what curated collections they have installed alongside the
	// individual plugins those bundles pulled in. Internally orbital
	// calls these "metapackages"; we render them as "bundles" in the CLI
	// for friendlier vocabulary.
	var resource, auth, bundles []apimodel.Plugin
	for _, p := range plugins {
		switch {
		case p.Kind == "metapackage":
			bundles = append(bundles, p)
		case p.Type == "auth":
			auth = append(auth, p)
		default:
			resource = append(resource, p)
		}
	}
	sortByName := func(s []apimodel.Plugin) {
		sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
	}
	sortByName(resource)
	sortByName(auth)
	sortByName(bundles)

	headerStyle := lipgloss.NewStyle().Foreground(th.Palette.PrimaryAccent)

	var sb strings.Builder
	emit := func(header string, plugins []apimodel.Plugin, addLeadingNewline bool) {
		if len(plugins) == 0 {
			return
		}
		if addLeadingNewline {
			sb.WriteString("\n")
		}
		sb.WriteString("  " + headerStyle.Render(header) + "\n")
		for _, p := range plugins {
			sb.WriteString(renderPluginRow(th, p) + "\n")
		}
	}

	emit("Resource plugins", resource, false)
	emit("Auth plugins", auth, len(resource) > 0)
	emit("Bundles", bundles, len(resource) > 0 || len(auth) > 0)

	return sb.String()
}

func renderPluginRow(th *theme.Theme, p apimodel.Plugin) string {
	doneStyle := lipgloss.NewStyle().Foreground(th.Palette.Done)
	secondaryStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
	subtleStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)

	name := components.Pad(p.Name, 14)
	managedBy := ""
	if p.ManagedBy != "" {
		managedBy = "   " + subtleStyle.Render("(part of "+p.ManagedBy+")")
	}
	return fmt.Sprintf("    %s %s  %s%s",
		doneStyle.Render("✓"), name, secondaryStyle.Render(p.InstalledVersion), managedBy)
}

func renderPluginSearch(th *theme.Theme, plugins []apimodel.Plugin, opts SearchRenderOpts) string {
	if len(plugins) == 0 {
		query := opts.Query
		if query == "" {
			query = "your query"
		}
		return "No plugins found for '" + query + "'.\n"
	}

	accentStyle := lipgloss.NewStyle().Foreground(th.Palette.PrimaryAccent)
	doneStyle := lipgloss.NewStyle().Foreground(th.Palette.Done)
	secondaryStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)

	// Determine max name width for padding.
	maxName := 0
	for _, p := range plugins {
		if len(p.Name) > maxName {
			maxName = len(p.Name)
		}
	}
	if maxName < 14 {
		maxName = 14
	}

	var sb strings.Builder
	for _, p := range plugins {
		name := components.Pad(p.Name, maxName)
		styledName := accentStyle.Render(name)
		line := fmt.Sprintf("  %s  %s", styledName, p.Summary)
		if p.InstalledVersion != "" {
			// right-align the installed marker after the summary
			line += "  " + doneStyle.Render("✓ installed")
		}
		sb.WriteString(line + "\n")
	}

	// Footer: count + install hint + optional filter echo.
	footerParts := []string{fmt.Sprintf("%d plugin", len(plugins))}
	if len(plugins) != 1 {
		footerParts[0] += "s"
	}
	footerParts[0] += " · install with: formae plugin install <name>"
	if opts.Category != "" {
		footerParts = append(footerParts, "category: "+opts.Category)
	}
	if opts.Type != "" {
		footerParts = append(footerParts, "type: "+opts.Type)
	}
	footer := strings.Join(footerParts, " · ")
	sb.WriteString("  " + secondaryStyle.Render(footer) + "\n")

	return sb.String()
}

func renderPluginInfo(th *theme.Theme, p *apimodel.Plugin) string {
	accentStyle := lipgloss.NewStyle().Foreground(th.Palette.PrimaryAccent).Bold(true)
	doneStyle := lipgloss.NewStyle().Foreground(th.Palette.Done)

	var sb strings.Builder

	// Header: name (accent bold) + version + optional installed marker.
	header := accentStyle.Render(p.Name)
	if p.InstalledVersion != "" {
		header += " " + p.InstalledVersion + "  " + doneStyle.Render("✓ installed")
	}
	fmt.Fprintf(&sb, "%s\n\n", header)

	// Build field pairs — same conditional set as original renderPluginInfo.
	var pairs [][2]string

	// For bundles display "bundle" rather than the empty plugin type and
	// surface the description so the user understands what the bundle
	// pulls in. For plugins, fall back to the plugin runtime type
	// (resource | auth) and namespace as before. ("metapackage" is the
	// internal orbital term; "bundle" is what we show users.)
	if p.Kind == "metapackage" {
		pairs = append(pairs, [2]string{"Type", "bundle"})
	} else {
		pairs = append(pairs, [2]string{"Type", p.Type})
		if p.Namespace != "" {
			pairs = append(pairs, [2]string{"Namespace", p.Namespace})
		}
	}
	if p.Category != "" {
		pairs = append(pairs, [2]string{"Category", p.Category})
	}
	if p.Summary != "" {
		pairs = append(pairs, [2]string{"Summary", p.Summary})
	}
	if p.Kind == "metapackage" && p.Description != "" && p.Description != p.Summary {
		pairs = append(pairs, [2]string{"Description", p.Description})
	}
	if p.Publisher != "" {
		pairs = append(pairs, [2]string{"Publisher", p.Publisher})
	}
	if p.License != "" {
		pairs = append(pairs, [2]string{"License", p.License})
	}
	if p.Channel != "" {
		pairs = append(pairs, [2]string{"Channel", p.Channel})
	}
	if len(p.AvailableVersions) > 0 {
		// Highlight the installed version inside the available list.
		versions := make([]string, len(p.AvailableVersions))
		for i, v := range p.AvailableVersions {
			if v == p.InstalledVersion {
				versions[i] = doneStyle.Render(v)
			} else {
				versions[i] = v
			}
		}
		pairs = append(pairs, [2]string{"Available", strings.Join(versions, ", ")})
	}
	if p.ManagedBy != "" {
		pairs = append(pairs, [2]string{"Part of", p.ManagedBy})
	}

	if len(pairs) > 0 {
		fmt.Fprintf(&sb, "  %s\n", strings.ReplaceAll(components.FieldList(th, pairs), "\n", "\n  "))
	}

	return sb.String()
}
