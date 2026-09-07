// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package simview

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// TestOpGlyphAndColorFromTheme asserts opGlyph/opColor route every opKind to
// its matching theme field, so a future swapped or missing case is caught.
func TestOpGlyphAndColorFromTheme(t *testing.T) {
	th := theme.New("rich") // non-degenerate op colors

	tests := []struct {
		name  string
		op    opKind
		glyph string
		color lipgloss.AdaptiveColor
	}{
		{"create", opCreate, th.Glyphs.OpCreate, th.Palette.OpCreate},
		{"update", opUpdate, th.Glyphs.OpUpdate, th.Palette.OpUpdate},
		{"delete", opDelete, th.Glyphs.OpDelete, th.Palette.OpDelete},
		{"replace", opReplace, th.Glyphs.OpReplace, th.Palette.OpReplace},
		{"detach", opDetach, th.Glyphs.OpDetach, th.Palette.OpDetach},
		{"keep", opKeep, th.Glyphs.OpKeep, th.Palette.OpKeep},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.glyph, opGlyph(th.Glyphs, tc.op))
			assert.Equal(t, tc.color, opColor(th.Palette, tc.op))
		})
	}
}
