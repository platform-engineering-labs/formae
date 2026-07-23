//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQuietMatchesFormaePalette locks quiet.toml against the original
// FormaePalette. The TOML additionally sets the six op_* colors (which the old
// Go palette left zero), so we compare only the sixteen pre-existing fields.
func TestQuietMatchesFormaePalette(t *testing.T) {
	th, ok := loadBuiltin("quiet")
	require.True(t, ok)
	q := th.Palette
	f := FormaePalette()

	assert.Equal(t, f.Base, q.Base)
	assert.Equal(t, f.Surface, q.Surface)
	assert.Equal(t, f.TextPrimary, q.TextPrimary)
	assert.Equal(t, f.TextSecondary, q.TextSecondary)
	assert.Equal(t, f.TextSubtle, q.TextSubtle)
	assert.Equal(t, f.Border, q.Border)
	assert.Equal(t, f.Selection, q.Selection)
	assert.Equal(t, f.PrimaryAccent, q.PrimaryAccent)
	assert.Equal(t, f.SecondaryAccent, q.SecondaryAccent)
	assert.Equal(t, f.Error, q.Error)
	assert.Equal(t, f.ErrorSubtle, q.ErrorSubtle)
	assert.Equal(t, f.ErrorBright, q.ErrorBright)
	assert.Equal(t, f.Warning, q.Warning)
	assert.Equal(t, f.Done, q.Done)
	assert.Equal(t, f.InProgress, q.InProgress)
	assert.Equal(t, f.Pending, q.Pending)
}

// TestClassicMatchesClassicPalette locks classic.toml against the pre-recolor
// ClassicPalette (the deprecated backward-compat theme).
func TestClassicMatchesClassicPalette(t *testing.T) {
	th, ok := loadBuiltin("classic")
	require.True(t, ok)
	c := th.Palette
	f := ClassicPalette()

	assert.Equal(t, f.Base, c.Base)
	assert.Equal(t, f.Surface, c.Surface)
	assert.Equal(t, f.TextPrimary, c.TextPrimary)
	assert.Equal(t, f.TextSecondary, c.TextSecondary)
	assert.Equal(t, f.TextSubtle, c.TextSubtle)
	assert.Equal(t, f.Border, c.Border)
	assert.Equal(t, f.Selection, c.Selection)
	assert.Equal(t, f.PrimaryAccent, c.PrimaryAccent)
	assert.Equal(t, f.SecondaryAccent, c.SecondaryAccent)
	assert.Equal(t, f.Error, c.Error)
	assert.Equal(t, f.ErrorSubtle, c.ErrorSubtle)
	assert.Equal(t, f.ErrorBright, c.ErrorBright)
	assert.Equal(t, f.Warning, c.Warning)
	assert.Equal(t, f.Done, c.Done)
	assert.Equal(t, f.InProgress, c.InProgress)
	assert.Equal(t, f.Pending, c.Pending)
}
