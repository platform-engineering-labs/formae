// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func segCounts(s string) map[rune]int {
	out := map[rune]int{}
	for _, r := range s {
		out[r]++
	}
	return out
}

func TestProgressBar_Segments(t *testing.T) {
	th := theme.New("formae")
	done := []rune(th.Progress.FillDone)[0]
	inProgress := []rune(th.Progress.FillInProgress)[0]
	pending := []rune(th.Progress.FillPending)[0]
	tests := []struct {
		name   string
		width  int
		counts map[State]int
		want   map[rune]int
	}{
		{
			"completed fill (done+failed) then in-progress then pending",
			10,
			map[State]int{StateDone: 5, StateFailed: 2, StateInProgress: 2, StatePending: 1},
			map[rune]int{done: 7, inProgress: 2, pending: 1},
		},
		{
			"skipped counts as done segment",
			10,
			map[State]int{StateDone: 3, StateSkipped: 2, StatePending: 5},
			map[rune]int{done: 5, pending: 5},
		},
		{
			"all pending",
			8,
			map[State]int{StatePending: 4},
			map[rune]int{pending: 8},
		},
		{
			"all done",
			8,
			map[State]int{StateDone: 3},
			map[rune]int{done: 8},
		},
		{
			"zero total renders pending track",
			6,
			map[State]int{},
			map[rune]int{pending: 6},
		},
		{
			"rounding still fills exact width",
			10,
			map[State]int{StateDone: 1, StateFailed: 1, StateInProgress: 1},
			nil, // width assertion only
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(ProgressBar(th, tt.width, tt.counts, false))
			assert.Equal(t, tt.width, len([]rune(got)), "bar must span exactly the width")
			if tt.want != nil {
				assert.Equal(t, tt.want, segCounts(got))
			}
		})
	}
}

func TestProgressBar_SegmentOrder(t *testing.T) {
	th := theme.New("formae")
	got := ansi.Strip(ProgressBar(th, 12, map[State]int{
		StateDone: 3, StateFailed: 3, StateInProgress: 3, StatePending: 3,
	}, false))
	// completed fill (done+failed=6) → in-progress → pending, contiguous.
	// Done and failed share the single fill glyph (color, not char, distinguishes).
	want := strings.Repeat(th.Progress.FillDone, 6) +
		strings.Repeat(th.Progress.FillInProgress, 3) +
		strings.Repeat(th.Progress.FillPending, 3)
	assert.Equal(t, want, got)
}

func TestProgressBar_TinySegmentsStillVisible(t *testing.T) {
	// a non-zero state never rounds away to zero cells when width allows
	th := theme.New("formae")
	got := ansi.Strip(ProgressBar(th, 10, map[State]int{StateDone: 98, StateInProgress: 2}, false))
	assert.Contains(t, got, th.Progress.FillInProgress, "in-progress segment must stay visible")
	assert.Equal(t, 10, len([]rune(got)))
}

// The completed fill toggles color by outcome: Done normally, Error (red) when
// the command is failing/failed/canceled — as a single █ fill, not a separate
// ▒ failed section.
func TestProgressBar_CompletedFillColorReflectsOutcome(t *testing.T) {
	th := theme.New("formae")
	counts := map[State]int{StateDone: 3, StateFailed: 2}
	const errRGB = "248;113;113"  // Error (dark #F87171)
	const doneRGB = "232;232;232" // Done  (dark #E8E8E8)

	ok := ProgressBar(th, 10, counts, false)
	assert.Contains(t, ok, doneRGB, "healthy completed fill uses the Done color")
	assert.NotContains(t, ok, errRGB, "healthy fill is not red")

	failed := ProgressBar(th, 10, counts, true)
	assert.Contains(t, failed, errRGB, "failed/canceled fill turns red")
	assert.NotContains(t, failed, doneRGB, "failed fill drops the done color")
	assert.NotContains(t, ansi.Strip(failed), "▒", "single █ fill, no separate failed section")
	assert.Equal(t, 10, len([]rune(ansi.Strip(failed))))
}

func TestProgressBar_NonPositiveWidth(t *testing.T) {
	th := theme.New("formae")
	assert.Equal(t, "", ProgressBar(th, 0, map[State]int{StateDone: 1}, false))
	assert.Equal(t, "", ProgressBar(th, -3, map[State]int{StateDone: 1}, false))
}

// The bar draws its fill characters from the active theme rather than a
// hardcoded glyph set, so a discriminating theme (colorblind uses ▚ for
// in-progress, distinct from the quiet theme's ▓/░) shows up in the output.
func TestProgressBarUsesThemeFills(t *testing.T) {
	th := theme.New("colorblind")
	out := ProgressBar(th, 10, map[State]int{StateInProgress: 5, StateDone: 5}, false)
	assert.Contains(t, out, th.Progress.FillInProgress) // "▚"
	assert.NotContains(t, out, "░")                     // colorblind in-progress is not the quiet fill
}

func TestProgressBar_Golden(t *testing.T) {
	th := theme.New("formae")
	out := ProgressBar(th, 40, map[State]int{
		StateDone: 12, StateFailed: 1, StateInProgress: 1, StatePending: 1,
	}, false)
	tuitest.RequireGolden(t, []byte(out))
}
