// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

// TestBuiltinRowStyles locks the per-theme row-coloring behavior: quiet keeps
// every column uniform (label not special, no whole-row delete tint), while
// rich and colorblind make the label the accent and tint delete rows whole.
func TestBuiltinRowStyles(t *testing.T) {
	q := New("quiet").Rows
	assert.False(t, q.LabelAccent, "quiet label must not be accented")
	assert.False(t, q.DeleteWholeRow, "quiet delete must not tint the whole row")

	for _, name := range []string{"rich", "colorblind"} {
		r := New(name).Rows
		assert.True(t, r.LabelAccent, "%s label must be accented", name)
		assert.True(t, r.DeleteWholeRow, "%s delete must tint the whole row", name)
	}
}

// TestRichErrorIsSoftened documents that rich's Error was softened off the
// pure #FF0000 (too bright on the eye, esp. as the severity confirmation-bar
// background) to pb's easier red — #DC2626 light / #FF6666 dark.
func TestRichErrorIsSoftened(t *testing.T) {
	th := New("rich")
	assert.Equal(t, "#DC2626", string(th.Palette.Error.Light))
	assert.Equal(t, "#FF6666", string(th.Palette.Error.Dark))
}

// TestQuietOpColorsAreUniformNeutral documents that quiet renders every
// operation in the same neutral text color (text_primary): the glyph and the
// word carry the meaning, not the color, so nothing but the operation glyph
// distinguishes a row. quiet's genuine-failure Error color is unchanged.
func TestQuietOpColorsAreUniformNeutral(t *testing.T) {
	th := New("quiet")
	p := th.Palette
	ops := map[string]lipgloss.AdaptiveColor{
		"op_create": p.OpCreate, "op_update": p.OpUpdate, "op_delete": p.OpDelete,
		"op_replace": p.OpReplace, "op_detach": p.OpDetach, "op_keep": p.OpKeep,
	}
	for name, c := range ops {
		assert.Equal(t, "#1A1A1A", c.Light, "%s light", name)
		assert.Equal(t, "#E8E8E8", c.Dark, "%s dark", name)
	}
	// genuine-failure Error color is untouched by the op-color change.
	assert.Equal(t, "#D64545", p.Error.Light)
	assert.Equal(t, "#F87171", p.Error.Dark)
}

// TestQuietUsesBrailleSpinner documents that quiet restores the original
// braille spinner (bubbles spinner.Dot) at its native ~100ms so the glyph
// animates smoothly during long-running operations.
func TestQuietUsesBrailleSpinner(t *testing.T) {
	th := New("quiet")
	assert.Equal(t, []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, th.Spinner.Frames)
	assert.Equal(t, 100, th.Spinner.IntervalMs)
}

// TestRichAndColorblindUseFadingDotPulse locks the fading-dot spinner for rich
// and colorblind: the ●◉○◉ frames at a 333ms interval, giving a calm fade pulse
// rather than a fast flicker. quiet's braille spinner keeps its own interval.
func TestRichAndColorblindUseFadingDotPulse(t *testing.T) {
	want := []string{"●", "◉", "○", "◉"}
	for _, name := range []string{"rich", "colorblind"} {
		t.Run(name, func(t *testing.T) {
			th := New(name)
			assert.Equal(t, want, th.Spinner.Frames)
			assert.Equal(t, "●", th.Spinner.StaticFrame)
			assert.Equal(t, 333, th.Spinner.IntervalMs)
		})
	}
}

// TestRichLogoWordmarkIsBlue documents that rich colors the logo wordmark blue
// (letters) — the propeller stays orange — while other themes leave it unset
// (default white/black wordmark).
func TestRichLogoWordmarkIsBlue(t *testing.T) {
	rich := New("rich").Palette.LogoWordmark
	assert.Equal(t, "#2563EB", rich.Light)
	assert.Equal(t, "#60A5FA", rich.Dark)

	for _, name := range []string{"quiet", "colorblind"} {
		lw := New(name).Palette.LogoWordmark
		assert.Empty(t, lw.Light, "%s must not set a logo wordmark color", name)
		assert.Empty(t, lw.Dark, "%s must not set a logo wordmark color", name)
	}
}
