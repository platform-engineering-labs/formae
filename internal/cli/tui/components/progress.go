// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

const (
	progressFillChar  = "━"
	progressEmptyChar = "─"
)

// ProgressBar renders a fixed-width progress bar: filled portion in the
// secondary accent, remainder in the pending shade. A zero total renders an
// empty track.
func ProgressBar(th *theme.Theme, completed, total, width int) string {
	if width <= 0 {
		return ""
	}
	p := th.Palette
	fillStyle := lipgloss.NewStyle().Foreground(p.SecondaryAccent)
	emptyStyle := lipgloss.NewStyle().Foreground(p.Pending)

	if total <= 0 {
		return emptyStyle.Render(strings.Repeat(progressEmptyChar, width))
	}

	filled := min(max(width*completed/total, 0), width)

	return fillStyle.Render(strings.Repeat(progressFillChar, filled)) +
		emptyStyle.Render(strings.Repeat(progressEmptyChar, width-filled))
}
