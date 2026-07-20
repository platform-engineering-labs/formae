// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// ProgressBar renders a fixed-width bar for a command's progress. The completed
// portion (done + skipped + failed) is a single █ fill whose color reflects the
// command's OUTCOME — the theme Done color normally, or Error (red) when the
// command is failing/failed/canceled (failed == true) — so the whole bar
// toggles to its end-state color instead of showing per-resource success/
// failure sections. In-progress (░) and pending (⋅) keep their own colors. A
// zero total renders a full pending track. Non-zero segments never round away
// to zero cells while the width allows.
func ProgressBar(th *theme.Theme, width int, counts map[State]int, failed bool) string {
	if width <= 0 {
		return ""
	}
	p := th.Palette

	fill := p.Done
	if failed {
		fill = p.Error
	}

	// Display order: completed fill, in-progress, pending.
	chars := []string{"█", "░", "⋅"}
	colors := []lipgloss.AdaptiveColor{fill, p.InProgress, p.Pending}
	segCounts := []int{
		counts[StateDone] + counts[StateSkipped] + counts[StateFailed],
		counts[StateInProgress],
		counts[StatePending],
	}

	total := 0
	for _, n := range segCounts {
		total += n
	}
	if total == 0 {
		pending := lipgloss.NewStyle().Foreground(p.Pending)
		return pending.Render(strings.Repeat("⋅", width))
	}

	widths := allocateCells(segCounts, total, width)

	var b strings.Builder
	for i := range segCounts {
		if widths[i] == 0 {
			continue
		}
		st := lipgloss.NewStyle().Foreground(colors[i])
		b.WriteString(st.Render(strings.Repeat(chars[i], widths[i])))
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
