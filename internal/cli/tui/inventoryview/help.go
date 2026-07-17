// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// renderHelpOverlay returns a centered help panel listing all inventory keybindings.
// It renders a bordered panel with title "Keybindings" and two-column layout:
// (key in Accent style, description in TextSecondary). Mirrors statuswatch/help.go.
func renderHelpOverlay(th *theme.Theme, width, bodyHeight int) string {
	bindings := []struct {
		key  string
		desc string
	}{
		{"↑↓ / j/k", "navigate"},
		{"ctrl-d / ctrl-u", "half page"},
		{"enter", "detail"},
		{"esc", "back"},
		{"/", "search"},
		{"s", "sort"},
		{"r", "refresh"},
		{"1-4 / tab", "switch tab"},
		{"q", "quit"},
		{"?", "help"},
	}

	// Build two-column rows: "key — desc"
	var lines []string
	for _, b := range bindings {
		keyStr := th.Styles.Accent.Render(b.key)
		descStr := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary).Render(b.desc)
		lines = append(lines, "  "+keyStr+" — "+descStr)
	}

	// Build content: title + bindings
	titleStyle := lipgloss.NewStyle().
		Foreground(th.Palette.TextPrimary).
		Bold(true)
	title := titleStyle.Render("Keybindings")
	content := title + "\n" + strings.Join(lines, "\n")

	// Apply panel styling: border, padding
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.Palette.Border).
		Padding(1, 2).
		Render(content)

	// Center the panel using lipgloss.Place
	return lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center, panel)
}
