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

// FormatAge renders how long ago start was as a compact relative time of at
// most four characters: 42s, 5m, 3h, 12d, >3mo, >2y. The display is
// approximate for old timestamps — callers must sort on the real time, not
// on this string. A zero time renders "—".
func FormatAge(start, now time.Time) string {
	if start.IsZero() {
		return "—"
	}
	d := now.Sub(start)
	day := 24 * time.Hour
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*day:
		return fmt.Sprintf("%dd", int(d/day))
	case d < 300*day: // 1–9 months
		return fmt.Sprintf(">%dmo", int(d/(30*day)))
	default: // 10+ months reads as years
		y := int(d / (365 * day))
		if y < 1 {
			y = 1
		}
		return fmt.Sprintf(">%dy", y)
	}
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

// VersionLabel formats a version string for header display: "" stays empty,
// otherwise it is prefixed with "v" (unless already prefixed).
func VersionLabel(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// HeaderBarBranded renders a compact two-line header: the "formae" wordmark
// ("form" in bright text, "ae" in brand orange) followed by the command, with a
// right-aligned status and a bottom border. commandAccent renders the command in
// the accent color instead of bright text.
func HeaderBarBranded(th *theme.Theme, command, right string, width int, commandAccent bool) string {
	p := th.Palette
	form := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true).Render("form")
	ae := lipgloss.NewStyle().Foreground(p.SecondaryAccent).Bold(true).Render("ae")
	left := "  " + form + ae
	if command != "" {
		cmdColor := p.TextPrimary
		if commandAccent {
			cmdColor = p.PrimaryAccent
		}
		left += " " + lipgloss.NewStyle().Foreground(cmdColor).Render(command)
	}
	rt := ""
	if right != "" {
		rt = right + "  "
	}
	line := left + PadBetween(width, left, rt) + rt
	border := lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", width))
	return line + "\n" + border
}

// HeaderBarWithLogo renders a four-line header banner with a brand icon on the
// left. The icon occupies three rows, with the wordmark stacked alongside it:
// "formae" (accent) on row 1 with the right-aligned status, the command (bright)
// on row 2, and the version (dim) on row 3; a full-width border runs beneath.
// logoRows are pre-styled and assumed equal width; missing rows are blank.
func HeaderBarWithLogo(th *theme.Theme, command, right, version string, width int, logoRows []string) string {
	p := th.Palette
	iconW := 0
	for _, r := range logoRows {
		if w := lipgloss.Width(r); w > iconW {
			iconW = w
		}
	}
	row := func(i int) string {
		if i < len(logoRows) {
			return logoRows[i]
		}
		return strings.Repeat(" ", iconW)
	}
	pad := func(s string) string {
		if w := lipgloss.Width(s); w < width {
			return s + strings.Repeat(" ", width-w)
		}
		return s
	}

	brand := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true).Render("formae")
	l0 := row(0) + " " + brand
	rt := ""
	if right != "" {
		rt = right + "  "
	}
	line0 := l0 + PadBetween(width, l0, rt) + rt

	line1 := pad(row(1) + " " + lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true).Render(command))

	l2 := row(2) + " "
	if version != "" {
		l2 += lipgloss.NewStyle().Foreground(p.TextSubtle).Render(version)
	}
	line2 := pad(l2)

	border := lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", width))

	return line0 + "\n" + line1 + "\n" + line2 + "\n" + border
}

// FooterBarNarrow renders the bottom bar for narrow terminals: a single
// dim-styled content string with a top border and no "?: help" suffix.
// The content is rendered using the KeybindingDesc (TextSubtle) role.
func FooterBarNarrow(th *theme.Theme, width int, content string) string {
	p := th.Palette
	s := th.Styles

	dimContent := s.KeybindingDesc.Render(content)
	left := "  " + dimContent
	// Pad to full width.
	leftW := lipgloss.Width(left)
	if leftW < width {
		left += strings.Repeat(" ", width-leftW)
	}

	return lipgloss.NewStyle().
		Width(width).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(p.Border).
		Render(left)
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
