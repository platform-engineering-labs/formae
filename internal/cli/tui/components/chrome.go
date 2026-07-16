// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// KeyHint is one key/description pair rendered in a FooterBar.
type KeyHint struct {
	Key  string
	Desc string
}

// PadBetween returns the spacing that separates left and right so the pair
// spans totalWidth. Widths are measured ANSI-aware. At least one space is
// always returned.
func PadBetween(totalWidth int, left, right string) string {
	space := max(totalWidth-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return strings.Repeat(" ", space)
}

// FormatDuration renders a duration as MM:SS. Minutes are not capped.
func FormatDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// HeaderBar renders the top bar: bold left text, right-aligned status
// (e.g. "↻ live"), with a bottom border across the full width.
func HeaderBar(th *theme.Theme, left, right string, width int) string {
	p := th.Palette
	leftPadded := "  " + left
	rightPadded := right + "  "
	content := leftPadded + PadBetween(width, leftPadded, rightPadded) + rightPadded

	return lipgloss.NewStyle().
		Foreground(p.TextPrimary).Bold(true).
		Width(width).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(p.Border).
		Render(content)
}

// FooterBar renders the bottom bar: key hints on the left, optional extra
// info plus the standing "?: help" hint on the right, with a top border.
func FooterBar(th *theme.Theme, width int, hints []KeyHint, right string) string {
	p := th.Palette
	s := th.Styles

	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, s.KeybindingKey.Render(h.Key)+s.KeybindingDesc.Render(": "+h.Desc))
	}
	left := "  " + strings.Join(parts, "  ")

	rightParts := []string{}
	if right != "" {
		rightParts = append(rightParts, right)
	}
	rightParts = append(rightParts, s.KeybindingKey.Render("?")+s.KeybindingDesc.Render(": help"))
	rightStr := strings.Join(rightParts, "  ") + "  "

	content := left + PadBetween(width, left, rightStr) + rightStr

	return lipgloss.NewStyle().
		Width(width).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(p.Border).
		Render(content)
}
