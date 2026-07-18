// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// AckMarker selects the glyph and theme role for an acknowledgment line.
type AckMarker int

const (
	AckDone AckMarker = iota // ✓ — completed action
	AckSkip                  // · — no-op / already satisfied
	AckWarn                  // ! — warning / follow-up hint
	AckFail                  // ✗ — failed action
)

// AckLine renders a single marker line like "✓ switched to staging".
// Symbol + color together carry the semantics (colorblind-safe, no green).
func AckLine(th *theme.Theme, m AckMarker, text string) string {
	var glyph string
	var role lipgloss.AdaptiveColor
	switch m {
	case AckDone:
		glyph, role = "✓", th.Palette.Done
	case AckSkip:
		glyph, role = "·", th.Palette.TextSubtle
	case AckWarn:
		glyph, role = "!", th.Palette.Warning
	default:
		glyph, role = "✗", th.Palette.Error
	}
	marker := lipgloss.NewStyle().Foreground(role).Render(glyph)
	body := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(text)
	if m == AckSkip {
		body = lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render(text)
	}
	return marker + " " + body
}
