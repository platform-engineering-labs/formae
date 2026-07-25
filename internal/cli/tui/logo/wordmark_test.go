// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	"github.com/stretchr/testify/assert"
)

// TestRenderFullLogoBraille_WordmarkColorOverride verifies a theme can recolor
// the braille wordmark letters (rich → blue) while the default keeps the brand
// wordmark. #123456 → truecolor SGR "38;2;18;52;86".
func TestRenderFullLogoBraille_WordmarkColorOverride(t *testing.T) {
	tuitest.PinRendering()

	custom := renderFullLogoBraille(true, brailleFullLogoWidth, lipgloss.Color("#123456"))
	if custom == "" {
		t.Skip("logo asset unavailable in this environment")
	}
	assert.Contains(t, custom, "38;2;18;52;86", "the wordmark override should recolor the letters")

	def := renderFullLogoBraille(true, brailleFullLogoWidth, nil)
	assert.NotContains(t, def, "38;2;18;52;86", "the default wordmark must not use the override color")
}
