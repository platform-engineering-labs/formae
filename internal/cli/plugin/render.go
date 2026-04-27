// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func renderPluginList(agentPlugins []apimodel.Plugin) string {
	if len(agentPlugins) == 0 {
		return "No plugins installed.\n"
	}

	var sb strings.Builder

	// Group by kind/type. Bundles get their own section so users can see
	// what curated collections they have installed alongside the
	// individual plugins those bundles pulled in. Internally orbital
	// calls these "metapackages"; we render them as "bundles" in the CLI
	// for friendlier vocabulary.
	var resource, auth, bundles []apimodel.Plugin
	for _, p := range agentPlugins {
		switch {
		case p.Kind == "metapackage":
			bundles = append(bundles, p)
		case p.Type == "auth":
			auth = append(auth, p)
		default:
			resource = append(resource, p)
		}
	}

	emit := func(header string, plugins []apimodel.Plugin, addLeadingNewline bool) {
		if len(plugins) == 0 {
			return
		}
		if addLeadingNewline {
			sb.WriteString("\n")
		}
		sb.WriteString(display.LightBlue(header) + "\n")
		for _, p := range plugins {
			line := fmt.Sprintf("  %s %s  %s",
				display.Green("✓"),
				padRight(p.Name, 14),
				display.Grey(p.InstalledVersion))
			if p.ManagedBy != "" {
				line += display.Grey("  (" + p.ManagedBy + ")")
			}
			sb.WriteString(line + "\n")
		}
	}

	emit("Resource plugins:", resource, false)
	emit("Auth plugins:", auth, len(resource) > 0)
	emit("Bundles:", bundles, len(resource) > 0 || len(auth) > 0)

	return sb.String()
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
