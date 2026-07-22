// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// ErrorPanel is the single error renderer all commands use for API errors:
// a red rounded border with the error type in the top border, a
// human-readable message, indented details and actionable suggestions.
// Detail and suggestion lines are treated as opaque — callers style
// specific values (resource names, commands) with theme styles.
type ErrorPanel struct {
	Title       string
	Message     string
	Details     []string
	Suggestions []string
}

// Render draws the panel at the given total width.
func (e ErrorPanel) Render(th *theme.Theme, width int) string {
	const minWidth = 24
	width = max(width, minWidth)
	p := th.Palette
	inner := width - 4 // "│ " + " │"

	borderSt := lipgloss.NewStyle().Foreground(p.Error)
	titleSt := lipgloss.NewStyle().Foreground(p.Error).Bold(true)
	messageSt := lipgloss.NewStyle().Foreground(p.TextPrimary).Width(inner)
	detailSt := lipgloss.NewStyle().Width(inner)
	suggestionSt := lipgloss.NewStyle().Foreground(p.TextSecondary).Width(inner)

	var body []string
	appendBlock := func(rendered string) {
		body = append(body, strings.Split(rendered, "\n")...)
	}

	body = append(body, "")
	appendBlock(messageSt.Render(e.Message))
	if len(e.Details) > 0 {
		body = append(body, "")
		for _, d := range e.Details {
			appendBlock(detailSt.Render("  " + d))
		}
	}
	if len(e.Suggestions) > 0 {
		body = append(body, "")
		for _, s := range e.Suggestions {
			appendBlock(suggestionSt.Render(s))
		}
	}
	body = append(body, "")

	title := " " + e.Title + " "
	trailing := max(width-3-lipgloss.Width(title), 0) // "╭─" + title + dashes + "╮"

	var b strings.Builder
	b.WriteString(borderSt.Render("╭─") + titleSt.Render(title) +
		borderSt.Render(strings.Repeat("─", trailing)+"╮") + "\n")
	for _, line := range body {
		pad := max(inner-lipgloss.Width(line), 0)
		b.WriteString(borderSt.Render("│") + " " + line + strings.Repeat(" ", pad) + " " + borderSt.Render("│") + "\n")
	}
	b.WriteString(borderSt.Render("╰" + strings.Repeat("─", width-2) + "╯"))
	return b.String()
}
