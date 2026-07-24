// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRichErrorIsHardRed documents that rich's Error color was changed to a
// pure, hard red (#FF0000) in both light and dark, replacing the softer
// #DC2626/#F87171 it inherited from quiet. This makes rich's failed status,
// error text, and severity confirmation-bar background all pure red. rich's
// error_subtle/error_bright stay untouched.
func TestRichErrorIsHardRed(t *testing.T) {
	th := New("rich")
	assert.Equal(t, "#FF0000", string(th.Palette.Error.Light))
	assert.Equal(t, "#FF0000", string(th.Palette.Error.Dark))
}

// TestQuietOpDeleteIsRestrainedGray documents that quiet's op_delete was
// changed from red to the same restrained gray used by op_create/op_update,
// so quiet carries no red for deletes — the "-" glyph and the word "delete"
// carry that meaning instead. quiet's genuine-failure Error color is
// unchanged.
func TestQuietOpDeleteIsRestrainedGray(t *testing.T) {
	th := New("quiet")
	assert.Equal(t, "#444444", string(th.Palette.OpDelete.Light))
	assert.Equal(t, "#AAAAAA", string(th.Palette.OpDelete.Dark))
	assert.Equal(t, "#DC2626", string(th.Palette.Error.Light))
	assert.Equal(t, "#F87171", string(th.Palette.Error.Dark))
}

// TestAllThemesUsePbFadingDotSpinner locks in pb's fading-dot spinner
// (frames, interval, static frame) across all three built-in themes, each
// of which is a self-contained root with its own [spinner] section.
func TestAllThemesUsePbFadingDotSpinner(t *testing.T) {
	want := []string{"●", "◉", "○", "◉"}
	for _, name := range []string{"rich", "quiet", "colorblind"} {
		t.Run(name, func(t *testing.T) {
			th := New(name)
			assert.Equal(t, want, th.Spinner.Frames)
			assert.Equal(t, 333, th.Spinner.IntervalMs)
			assert.Equal(t, "●", th.Spinner.StaticFrame)
		})
	}
}
