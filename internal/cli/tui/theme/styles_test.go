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

func TestNewStyles_BothPalettesProduceStyles(t *testing.T) {
	formae := NewStyles(FormaePalette())
	classic := NewStyles(ClassicPalette())

	// Both render without panicking
	assert.NotEmpty(t, formae.Title.Render("test"))
	assert.NotEmpty(t, classic.Title.Render("test"))
	assert.NotEmpty(t, formae.Panel.Render("test"))
	assert.NotEmpty(t, classic.Panel.Render("test"))
}
