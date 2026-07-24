//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestThemeHasPlanes(t *testing.T) {
	th := Theme{
		Glyphs:          Glyphs{OpCreate: "+"},
		Progress:        Progress{FillDone: "█", Animation: "pulse"},
		Spinner:         Spinner{Frames: []string{"◐", "◓"}, IntervalMs: 120},
		ConfirmationBar: ConfirmationBar{Color: "brand"},
		Header:          HeaderStyle{Highlight: "background"},
	}
	assert.Equal(t, "+", th.Glyphs.OpCreate)
	assert.Equal(t, "pulse", th.Progress.Animation)
	assert.Equal(t, 120, th.Spinner.IntervalMs)
	assert.Equal(t, "brand", th.ConfirmationBar.Color)
	assert.Equal(t, "background", th.Header.Highlight)
}
