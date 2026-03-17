// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormaePalette(t *testing.T) {
	p := FormaePalette()

	// Primary accent is blue
	assert.NotEmpty(t, string(p.PrimaryAccent.Dark))
	// Secondary accent is orange
	assert.NotEmpty(t, string(p.SecondaryAccent.Dark))
	// Error is red
	assert.NotEmpty(t, string(p.Error.Dark))
	// Warning is yellow/gold
	assert.NotEmpty(t, string(p.Warning.Dark))
	// All base colors defined
	assert.NotEmpty(t, string(p.Base.Dark))
	assert.NotEmpty(t, string(p.TextPrimary.Dark))
	assert.NotEmpty(t, string(p.TextSecondary.Dark))
	assert.NotEmpty(t, string(p.TextSubtle.Dark))
}

func TestClassicPalette(t *testing.T) {
	p := ClassicPalette()

	// Classic palette has same structure
	assert.NotEmpty(t, string(p.PrimaryAccent.Dark))
	assert.NotEmpty(t, string(p.SecondaryAccent.Dark))
	assert.NotEmpty(t, string(p.Error.Dark))
	assert.NotEmpty(t, string(p.Warning.Dark))
}

func TestClassicPalette_PreservesExistingColors(t *testing.T) {
	p := ClassicPalette()

	// Gold matches gookit/color RGB(181, 181, 91) = #B5B55B
	assert.Equal(t, "#B5B55B", string(p.SecondaryAccent.Dark))
}

func TestPaletteByName(t *testing.T) {
	tests := []struct {
		name     string
		expected Palette
	}{
		{"formae", FormaePalette()},
		{"classic", ClassicPalette()},
		{"unknown", FormaePalette()}, // default fallback
		{"", FormaePalette()},        // empty fallback
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PaletteByName(tt.name)
			assert.Equal(t, tt.expected, p)
		})
	}
}
