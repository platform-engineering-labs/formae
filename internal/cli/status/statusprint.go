// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package status

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// renderStatusList renders the human-readable command list. When detailed is
// false it prints a compact summary table; when true it prints per-resource
// detail groups beneath each command header.
//
// width is the terminal width (use termWidth(os.Stdout) from call sites).
// now is injected for deterministic tests; callers pass time.Now().
func renderStatusList(th *theme.Theme, resp *apimodel.ListCommandStatusResponse, detailed bool, width int) string {
	return renderStatusListAt(th, resp, detailed, width, time.Now())
}

// renderStatusListAt is the testable variant with an explicit now.
func renderStatusListAt(th *theme.Theme, resp *apimodel.ListCommandStatusResponse, detailed bool, width int, now time.Time) string {
	if resp == nil || len(resp.Commands) == 0 {
		dimSt := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		return dimSt.Render("(no commands)") + "\n"
	}

	if !detailed {
		return statuswatch.RenderSummaryTable(th, resp.Commands, width, now)
	}

	// Detailed: command header row (via summary table, 1 row per command) +
	// per-resource group rows beneath it.
	return renderDetailedList(th, resp.Commands, width, now)
}

// renderDetailedList renders one command header followed by its resource groups
// for each command in cmds.
func renderDetailedList(th *theme.Theme, cmds []apimodel.Command, width int, now time.Time) string {
	p := th.Palette
	sep := lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", width))

	var sb strings.Builder
	for i, c := range cmds {
		if i > 0 {
			sb.WriteString("\n")
			sb.WriteString(sep)
			sb.WriteString("\n")
		}
		// Command header: ID, state, command type, mode, duration
		sb.WriteString(renderCommandHeader(th, c, width, now))
		// Resource groups
		sb.WriteString(statuswatch.RenderDetailTable(th, c, width, now))
	}
	return sb.String()
}

// renderCommandHeader renders a single command's summary line (ID, state, type,
// mode, duration) using the same style roles as the multi-view table.
func renderCommandHeader(th *theme.Theme, c apimodel.Command, width int, now time.Time) string {
	p := th.Palette

	idSt := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	textSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	dimSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	// State glyph + color
	var glyphStr string
	var stateSt lipgloss.Style
	switch c.State {
	case "Success":
		glyphStr = "✓"
		stateSt = lipgloss.NewStyle().Foreground(p.Done)
	case "Failed":
		glyphStr = "✗"
		stateSt = lipgloss.NewStyle().Foreground(p.Error)
	case "Canceled":
		glyphStr = "⊘"
		stateSt = lipgloss.NewStyle().Foreground(p.TextSubtle)
	default:
		glyphStr = "◐"
		stateSt = lipgloss.NewStyle().Foreground(p.InProgress)
	}

	// Duration
	var dur time.Duration
	if !c.StartTs.IsZero() {
		if (c.State == "Success" || c.State == "Failed" || c.State == "Canceled") && !c.EndTs.IsZero() {
			dur = c.EndTs.Sub(c.StartTs)
		} else {
			dur = now.Sub(c.StartTs)
		}
	}

	parts := []string{
		stateSt.Render(glyphStr),
		idSt.Render(c.CommandID),
		textSt.Render(c.Command),
	}
	if c.Mode != "" {
		parts = append(parts, dimSt.Render(c.Mode))
	}
	parts = append(parts, dimSt.Render(components.FormatDuration(dur)))

	return strings.Join(parts, "  ") + "\n"
}
