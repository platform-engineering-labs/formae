// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// HelpGroup is a labeled group rendered inside the HelpOverlay panel. Keybinding
// groups (the default) render in the left column; groups with Legend=true (icon
// meanings, e.g. status glyphs) render in a separate right column, since those
// are not keybindings.
type HelpGroup struct {
	Title  string    // e.g. "Navigate" | "Actions" | "General" | "Status"
	Hints  []KeyHint // reuses KeyHint{Key, Desc} from chrome.go
	Legend bool      // render in the right (legend) column instead of keybindings
}

// HelpOverlay renders a centered rounded-border panel titled "Help".
//
// Layout:
//   - Two columns: keybinding groups on the left, legend groups (Legend=true) on
//     the right. With no legend groups it collapses to a single column.
//   - Per group: a bold TextPrimary header, then "key — desc" rows (key in
//     Accent, description in TextSecondary). Keys align within their column.
//   - Groups within a column are separated by a blank line; a blank line
//     separates the title from the content.
//
// Centering: lipgloss.Place(width, height, Center, Center, panel). If the panel
// is larger than width/height it is placed top-left to avoid clipped borders.
func HelpOverlay(th *theme.Theme, width, height int, groups []HelpGroup) string {
	p := th.Palette
	s := th.Styles
	headerStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)

	// renderColumn stacks a set of groups, aligning keys within the column.
	renderColumn := func(gs []HelpGroup) string {
		maxKeyWidth := 0
		for _, g := range gs {
			for _, h := range g.Hints {
				if w := lipgloss.Width(h.Key); w > maxKeyWidth {
					maxKeyWidth = w
				}
			}
		}
		var rows []string
		for i, g := range gs {
			if i > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, headerStyle.Render(g.Title))
			for _, h := range g.Hints {
				pad := strings.Repeat(" ", maxKeyWidth-lipgloss.Width(h.Key))
				rows = append(rows, "  "+s.Accent.Render(h.Key)+pad+" — "+descStyle.Render(h.Desc))
			}
		}
		return strings.Join(rows, "\n")
	}

	var keybinds, legends []HelpGroup
	for _, g := range groups {
		if g.Legend {
			legends = append(legends, g)
		} else {
			keybinds = append(keybinds, g)
		}
	}

	content := renderColumn(keybinds)
	if len(legends) > 0 {
		content = lipgloss.JoinHorizontal(lipgloss.Top, content, "      ", renderColumn(legends))
	}

	// Title aligned with the content (no extra indent), then a blank line.
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		Padding(1, 2).
		Render(headerStyle.Render("Help") + "\n\n" + content)

	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	if panelW > width || panelH > height {
		return panel
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
