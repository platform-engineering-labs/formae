// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// Panel renders a print-and-exit bordered panel using a rounded border in the
// given role color, with the title embedded in the top border. The panel is
// wrapped to the given width. This is the same card technique used by
// detailmodel's renderCard.
func Panel(th *theme.Theme, role lipgloss.AdaptiveColor, title string, lines []string, width int) string {
	const minWidth = 24
	if width < minWidth {
		width = minWidth
	}

	borderSt := lipgloss.NewStyle().Foreground(role)
	content := strings.Join(lines, "\n")

	// Inner width: panel width minus 2 (borders) minus 2 (padding "│ " and " │")
	innerW := width - 4
	if innerW < 1 {
		innerW = 1
	}

	// Render card body with rounded border
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(role).
		Width(innerW).
		Padding(0, 1).
		Render(content)

	// Measure actual rendered width from the bottom border line
	cardLines := strings.Split(card, "\n")
	actualWidth := 0
	if len(cardLines) > 0 {
		actualWidth = lipgloss.Width(cardLines[len(cardLines)-1])
	}
	if actualWidth == 0 {
		actualWidth = innerW + 2
	}

	// Embed title in the top border: ╭─ title ─────╮
	titleContent := " " + title + " "
	titleW := lipgloss.Width(titleContent)
	dashW := actualWidth - titleW - 2 // 2 = ╭ + ╮
	if dashW < 1 {
		dashW = 1
	}

	headerLine := borderSt.Render("╭─") +
		titleContent +
		borderSt.Render(strings.Repeat("─", dashW)+"╮")

	if len(cardLines) > 0 {
		cardLines[0] = headerLine
	}

	return strings.Join(cardLines, "\n")
}
