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
	// Selection highlight for cursor rows
	assert.NotEmpty(t, string(p.Selection.Dark))
}

func TestPalettes_HaveErrorTiers(t *testing.T) {
	for _, p := range []Palette{FormaePalette()} {
		assert.NotEmpty(t, p.ErrorSubtle.Dark)
		assert.NotEmpty(t, p.ErrorBright.Dark)
	}
}
