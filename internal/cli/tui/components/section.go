// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// SectionHeader renders the "▌ Title" group header using the SecondaryAccent
// color, bold. Matches the section-header rendering style used in detailmodel.
func SectionHeader(th *theme.Theme, title string) string {
	p := th.Palette
	return lipgloss.NewStyle().
		Foreground(p.SecondaryAccent).
		Bold(true).
		Render("▌ " + title)
}
