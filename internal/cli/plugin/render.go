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
		return "No plugins installed."
	}

	var sb strings.Builder

	// Group by type
	var resource, auth []apimodel.Plugin
	for _, p := range agentPlugins {
		if p.Type == "auth" {
			auth = append(auth, p)
		} else {
			resource = append(resource, p)
		}
	}

	if len(resource) > 0 {
		sb.WriteString(display.LightBlue("Resource plugins (agent):") + "\n")
		for _, p := range resource {
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

	if len(auth) > 0 {
		if len(resource) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(display.LightBlue("Auth plugins:") + "\n")
		for _, p := range auth {
			line := fmt.Sprintf("  %s %s  %s",
				display.Green("✓"),
				padRight(p.Name, 14),
				display.Grey(p.InstalledVersion))
			sb.WriteString(line + "\n")
		}
	}

	return sb.String()
}

func renderPluginSearch(plugins []apimodel.Plugin) string {
	if len(plugins) == 0 {
		return "No plugins found."
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
	sb.WriteString(fmt.Sprintf("%s %s%s\n", display.LightBlue(p.Name), p.InstalledVersion, display.Green(installed)))
	sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Type:"), p.Type))
	if p.Namespace != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Namespace:"), p.Namespace))
	}
	if p.Category != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Category:"), p.Category))
	}
	if p.Summary != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Summary:"), p.Summary))
	}
	if p.Publisher != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Publisher:"), p.Publisher))
	}
	if p.License != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("License:"), p.License))
	}
	if p.Channel != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Channel:"), p.Channel))
	}
	if len(p.AvailableVersions) > 0 {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Available:"), strings.Join(p.AvailableVersions, ", ")))
	}
	if p.ManagedBy != "" {
		sb.WriteString(fmt.Sprintf("  %-14s %s\n", display.Grey("Part of:"), p.ManagedBy))
	}
	return sb.String()
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
