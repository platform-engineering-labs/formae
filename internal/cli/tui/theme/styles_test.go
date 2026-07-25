// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewStyles(t *testing.T) {
	s := NewStyles(FormaePalette())

	// Styles produce non-empty rendered output
	assert.NotEmpty(t, s.Title.Render("test"))
	assert.NotEmpty(t, s.Subtitle.Render("test"))
	assert.NotEmpty(t, s.ErrorPanel.Render("test"))
	assert.NotEmpty(t, s.KeybindingKey.Render("test"))
	assert.NotEmpty(t, s.KeybindingDesc.Render("test"))
	assert.NotEmpty(t, s.ProgressBarFill.Render("test"))
	assert.NotEmpty(t, s.StatusDone.Render("test"))
	assert.NotEmpty(t, s.StatusInProgress.Render("test"))
	assert.NotEmpty(t, s.StatusPending.Render("test"))
	assert.NotEmpty(t, s.StatusFailed.Render("test"))
}

// TestNewStyles_ProducesStylesWithoutPanicking guards NewStyles against
// panicking for a fully-populated Palette. It used to build two Styles from
// two distinct Go palettes (FormaePalette and the now-removed
// ClassicPalette); only FormaePalette remains, so this is a straightforward
// smoke test.
func TestNewStyles_ProducesStylesWithoutPanicking(t *testing.T) {
	s := NewStyles(FormaePalette())

	assert.NotEmpty(t, s.Title.Render("test"))
	assert.NotEmpty(t, s.Panel.Render("test"))
}
