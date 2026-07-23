// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package simview

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestOpGlyphAndColorFromTheme(t *testing.T) {
	th := theme.New("rich")
	assert.Equal(t, th.Glyphs.OpCreate, opGlyph(th.Glyphs, opCreate))
	assert.Equal(t, th.Glyphs.OpDelete, opGlyph(th.Glyphs, opDelete))
	assert.Equal(t, th.Palette.OpCreate, opColor(th.Palette, opCreate))
	assert.Equal(t, th.Palette.OpReplace, opColor(th.Palette, opReplace))
}
