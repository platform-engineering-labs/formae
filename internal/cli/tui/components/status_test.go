// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

func TestGlyph(t *testing.T) {
	th := theme.New("quiet")
	tests := []struct {
		state State
		want  string
	}{
		{StateFailed, th.Glyphs.StatusFailed},
		{StateSkipped, th.Glyphs.StatusSkipped},
		{StateDone, ""},
		{StateInProgress, ""},
		{StatePending, ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, Glyph(th.Glyphs, tt.state), string(tt.state))
	}
}

func TestIndicator_Content(t *testing.T) {
	th := theme.New("formae")
	tests := []struct {
		name        string
		state       State
		spinnerView string
		elapsed     string
		want        string
	}{
		{"done", StateDone, "", "", "Done"},
		{"in-progress", StateInProgress, "◐", "00:05", "◐ 00:05"},
		{"pending", StatePending, "", "", "·"},
		{"failed", StateFailed, "", "", "FAILED"},
		{"skipped", StateSkipped, "", "", "Skipped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(Indicator(th, tt.state, tt.spinnerView, tt.elapsed))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStateCounts_Content(t *testing.T) {
	th := theme.New("formae")

	t.Run("zero failed still shown, zero skipped hidden", func(t *testing.T) {
		got := ansi.Strip(StateCounts(th, map[State]int{
			StateDone: 12, StateInProgress: 2, StatePending: 1,
		}))
		assert.Equal(t, "12 done · 2 in-progress · 1 pending · 0 failed", got)
	})

	t.Run("skipped shown when non-zero", func(t *testing.T) {
		got := ansi.Strip(StateCounts(th, map[State]int{
			StateDone: 142, StatePending: 50, StateFailed: 3, StateSkipped: 5,
		}))
		assert.Equal(t, "142 done · 0 in-progress · 50 pending · 3 failed · 5 skipped", got)
	})
}

func TestNewSpinner_UsesPrimaryAccent(t *testing.T) {
	th := theme.New("formae")
	s := NewSpinner(th)
	assert.Equal(t, th.Palette.PrimaryAccent, s.Style.GetForeground())
}

func TestNewSpinnerFromTheme(t *testing.T) {
	th := theme.New("quiet")
	s := NewSpinner(th)
	assert.Equal(t, th.Spinner.Frames, s.Spinner.Frames)
	assert.Equal(t, time.Duration(th.Spinner.IntervalMs)*time.Millisecond, s.Spinner.FPS)
}

func TestIndicator_Golden(t *testing.T) {
	th := theme.New("formae")
	out := Indicator(th, StateDone, "", "") + "\n" +
		Indicator(th, StateInProgress, "◐", "00:05") + "\n" +
		Indicator(th, StatePending, "", "") + "\n" +
		Indicator(th, StateFailed, "", "") + "\n" +
		Indicator(th, StateSkipped, "", "")
	tuitest.RequireGolden(t, []byte(out))
}
