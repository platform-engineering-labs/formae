// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// HelpGroup is a labeled group of keybinding hints rendered inside the
// HelpOverlay panel.
type HelpGroup struct {
	Title string    // e.g. "Navigate" | "Actions" | "General"
	Hints []KeyHint // reuses KeyHint{Key, Desc} from chrome.go
}

// HelpOverlay renders a centered rounded-border panel titled "Keybindings".
//
// Layout:
//   - Per group: a bold TextPrimary header line, then "key — desc" rows where
//     the key is styled in Accent and the description in TextSecondary.
//   - The key column is left-padded to the widest key across ALL groups so that
//     columns align across group boundaries.
//   - Groups are separated by a blank line.
//
// Centering: lipgloss.Place(width, height, Center, Center, panel).
// Edge case: if the rendered panel is wider or taller than width/height, the
// panel is placed top-left (no centering) to avoid clipped/offset borders.
func HelpOverlay(th *theme.Theme, width, height int, groups []HelpGroup) string {
	p := th.Palette
	s := th.Styles

	// 1. Find the widest key across all groups (plain text width).
	maxKeyWidth := 0
	for _, g := range groups {
		for _, h := range g.Hints {
			w := lipgloss.Width(h.Key)
			if w > maxKeyWidth {
				maxKeyWidth = w
			}
		}
	}

	// 2. Build the panel content.
	headerStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)

	var rows []string
	for i, g := range groups {
		if i > 0 {
			rows = append(rows, "") // blank separator between groups
		}
		rows = append(rows, headerStyle.Render(g.Title))
		for _, h := range g.Hints {
			// Pad the key to maxKeyWidth (plain text measurement).
			keyPlain := h.Key
			padding := strings.Repeat(" ", maxKeyWidth-lipgloss.Width(keyPlain))
			keyStr := s.Accent.Render(keyPlain) + padding
			descStr := descStyle.Render(h.Desc)
			rows = append(rows, "  "+keyStr+" — "+descStr)
		}
	}

	content := strings.Join(rows, "\n")

	// 3. Build the bordered panel: title at top of border, padding inside.
	titleStyle := lipgloss.NewStyle().
		Foreground(p.TextPrimary).
		Bold(true).
		Padding(0, 1)

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		BorderTop(true).
		Padding(1, 2).
		Render(titleStyle.Render("Keybindings") + "\n" + content)

	// 4. Center if there's room; otherwise place top-left.
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	if panelW > width || panelH > height {
		return panel
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
