// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// progressSegment maps a class of states onto a bar character and theme
// color, in display order. Character density (solid → hatched → light →
// dots) carries the meaning without color — colorblind-safe by default.
type progressSegment struct {
	states []State
	char   string
	color  func(theme.Palette) lipgloss.AdaptiveColor
}

var progressSegments = []progressSegment{
	{[]State{StateDone, StateSkipped}, "█", func(p theme.Palette) lipgloss.AdaptiveColor { return p.Done }},
	{[]State{StateFailed}, "▒", func(p theme.Palette) lipgloss.AdaptiveColor { return p.Error }},
	{[]State{StateInProgress}, "░", func(p theme.Palette) lipgloss.AdaptiveColor { return p.InProgress }},
	{[]State{StatePending}, "⋅", func(p theme.Palette) lipgloss.AdaptiveColor { return p.Pending }},
}

// ProgressBar renders a fixed-width bar segmented by state, per the
// status-watch mockup: █ done/skipped, ▒ failed, ░ in-progress, ⋅ pending.
// Segment colors follow the theme roles (Done-bright, Error, InProgress,
// Pending). A zero total renders a full pending track. Non-zero states
// never round away to zero cells while the width allows.
func ProgressBar(th *theme.Theme, width int, counts map[State]int) string {
	if width <= 0 {
		return ""
	}
	p := th.Palette

	segCounts := make([]int, len(progressSegments))
	total := 0
	for i, seg := range progressSegments {
		for _, s := range seg.states {
			segCounts[i] += counts[s]
		}
		total += segCounts[i]
	}
	if total == 0 {
		pending := lipgloss.NewStyle().Foreground(p.Pending)
		return pending.Render(strings.Repeat("⋅", width))
	}

	widths := allocateCells(segCounts, total, width)

	var b strings.Builder
	for i, seg := range progressSegments {
		if widths[i] == 0 {
			continue
		}
		st := lipgloss.NewStyle().Foreground(seg.color(p))
		b.WriteString(st.Render(strings.Repeat(seg.char, widths[i])))
	}
	return b.String()
}

// allocateCells distributes width cells over the segments proportionally to
// their counts, guaranteeing the widths sum to width and that no non-zero
// count collapses to zero cells while zero-guaranteed cells fit.
func allocateCells(counts []int, total, width int) []int {
	widths := make([]int, len(counts))

	// Reserve one cell per non-zero segment so small counts stay visible.
	nonZero := 0
	for _, c := range counts {
		if c > 0 {
			nonZero++
		}
	}
	if nonZero >= width {
		// Not enough room for proportionality: one cell per non-zero
		// segment, in display order, until the width is spent.
		left := width
		for i, c := range counts {
			if c > 0 && left > 0 {
				widths[i] = 1
				left--
			}
		}
		return widths
	}

	// Cumulative rounding: deterministic and sums exactly to width.
	cum, prev := 0, 0
	for i, c := range counts {
		cum += c
		boundary := (width*cum + total/2) / total
		widths[i] = boundary - prev
		prev = boundary
	}

	// Visibility fix-up: a non-zero count that rounded to zero cells
	// steals one cell from the widest segment.
	for i, c := range counts {
		if c == 0 || widths[i] > 0 {
			continue
		}
		widest := -1
		for k := range widths {
			if widths[k] > 1 && (widest == -1 || widths[k] > widths[widest]) {
				widest = k
			}
		}
		if widest >= 0 {
			widths[widest]--
			widths[i] = 1
		}
	}
	return widths
}
