// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// mergedPlugin tracks where a plugin is installed and the version on
// each side. When agent and CLI report the same plugin at the same
// version, the renderer collapses both into a single `(agent + cli)`
// row; when versions differ, it surfaces the mismatch inline so
// dual-install drift on auth plugins is visible at a glance.
type mergedPlugin struct {
	plugin       apimodel.Plugin
	agentVersion string
	cliVersion   string
}

func (m mergedPlugin) onAgent() bool { return m.agentVersion != "" }
func (m mergedPlugin) onCLI() bool   { return m.cliVersion != "" }

func renderPluginList(agentPlugins, localPlugins []apimodel.Plugin) string {
	if len(agentPlugins) == 0 && len(localPlugins) == 0 {
		return "No plugins installed.\n"
	}

	// Merge by name. Agent-reported metadata wins for the descriptor
	// fields (Type, Kind, Category, ManagedBy) since the agent
	// canonicalizes those; CLI-only plugins fall back to whatever the
	// local orbital records carry.
	merged := map[string]*mergedPlugin{}
	for _, p := range agentPlugins {
		m := merged[p.Name]
		if m == nil {
			m = &mergedPlugin{plugin: p}
			merged[p.Name] = m
		}
		m.plugin = p
		m.agentVersion = p.InstalledVersion
	}
	for _, p := range localPlugins {
		m := merged[p.Name]
		if m == nil {
			m = &mergedPlugin{plugin: p}
			merged[p.Name] = m
		}
		m.cliVersion = p.InstalledVersion
	}

	// Group by kind/type. Bundles get their own section so users can see
	// what curated collections they have installed alongside the
	// individual plugins those bundles pulled in. Internally orbital
	// calls these "metapackages"; we render them as "bundles" in the CLI
	// for friendlier vocabulary.
	var resource, auth, bundles []*mergedPlugin
	for _, m := range merged {
		switch {
		case m.plugin.Kind == "metapackage":
			bundles = append(bundles, m)
		case m.plugin.Type == "auth":
			auth = append(auth, m)
		default:
			resource = append(resource, m)
		}
	}
	sortByName := func(s []*mergedPlugin) {
		sort.Slice(s, func(i, j int) bool { return s[i].plugin.Name < s[j].plugin.Name })
	}
	sortByName(resource)
	sortByName(auth)
	sortByName(bundles)

	var sb strings.Builder
	emit := func(header string, plugins []*mergedPlugin, addLeadingNewline bool) {
		if len(plugins) == 0 {
			return
		}
		if addLeadingNewline {
			sb.WriteString("\n")
		}
		sb.WriteString(display.LightBlue(header) + "\n")
		for _, m := range plugins {
			sb.WriteString(renderMergedRow(m) + "\n")
		}
	}

	emit("Resource plugins:", resource, false)
	emit("Auth plugins:", auth, len(resource) > 0)
	emit("Bundles:", bundles, len(resource) > 0 || len(auth) > 0)

	return sb.String()
}

func renderMergedRow(m *mergedPlugin) string {
	name := padRight(m.plugin.Name, 14)
	managedBy := ""
	if m.plugin.ManagedBy != "" {
		managedBy = display.Grey("  (" + m.plugin.ManagedBy + ")")
	}
	switch {
	case m.onAgent() && m.onCLI() && m.agentVersion == m.cliVersion:
		return fmt.Sprintf("  %s %s  %s  %s%s",
			display.Green("✓"), name, display.Grey(m.agentVersion),
			display.Grey("(agent + cli)"), managedBy)
	case m.onAgent() && m.onCLI() && m.agentVersion != m.cliVersion:
		return fmt.Sprintf("  %s %s  %s  %s%s",
			display.Gold("⚠"), name,
			display.Grey(fmt.Sprintf("agent %s / cli %s", m.agentVersion, m.cliVersion)),
			display.Gold("version mismatch"), managedBy)
	case m.onAgent():
		return fmt.Sprintf("  %s %s  %s  %s%s",
			display.Green("✓"), name, display.Grey(m.agentVersion),
			display.Grey("(agent)"), managedBy)
	default:
		return fmt.Sprintf("  %s %s  %s  %s%s",
			display.Green("✓"), name, display.Grey(m.cliVersion),
			display.Grey("(cli)"), managedBy)
	}
}

func renderPluginSearch(plugins []apimodel.Plugin) string {
	if len(plugins) == 0 {
		return "No plugins found.\n"
	}

	var sb strings.Builder
	for _, p := range plugins {
		line := fmt.Sprintf("  %s  %s",
			padRight(p.Name, 14),
			p.Summary)
		if p.InstalledVersion != "" {
			line += "  " + display.Green("✓ installed")
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func renderPluginInfo(p *apimodel.Plugin) string {
	var sb strings.Builder
	installed := ""
	if p.InstalledVersion != "" {
		installed = " (installed)"
	}
	fmt.Fprintf(&sb, "%s %s%s\n", display.LightBlue(p.Name), p.InstalledVersion, display.Green(installed))

	// For bundles display "bundle" rather than the empty plugin type and
	// surface the description so the user understands what the bundle
	// pulls in. For plugins, fall back to the plugin runtime type
	// (resource | auth) and namespace as before. ("metapackage" is the
	// internal orbital term; "bundle" is what we show users.)
	if p.Kind == "metapackage" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Type:"), "bundle")
	} else {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Type:"), p.Type)
		if p.Namespace != "" {
			fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Namespace:"), p.Namespace)
		}
	}
	if p.Category != "" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Category:"), p.Category)
	}
	if p.Summary != "" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Summary:"), p.Summary)
	}
	if p.Kind == "metapackage" && p.Description != "" && p.Description != p.Summary {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Description:"), p.Description)
	}
	if p.Publisher != "" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Publisher:"), p.Publisher)
	}
	if p.License != "" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("License:"), p.License)
	}
	if p.Channel != "" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Channel:"), p.Channel)
	}
	if len(p.AvailableVersions) > 0 {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Available:"), strings.Join(p.AvailableVersions, ", "))
	}
	if p.ManagedBy != "" {
		fmt.Fprintf(&sb, "  %-14s %s\n", display.Grey("Part of:"), p.ManagedBy)
	}
	return sb.String()
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
