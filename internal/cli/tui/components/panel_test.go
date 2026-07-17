// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestPanel(t *testing.T) {
	th := theme.New("formae")
	role := lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"}

	lines := []string{
		"First line of content",
		"Second line of content",
	}

	t.Run("contains_title", func(t *testing.T) {
		result := Panel(th, role, "My Panel", lines, 80)
		plain := stripANSI(result)
		assert.Contains(t, plain, "My Panel")
	})

	t.Run("every_line_fits_width", func(t *testing.T) {
		const width = 80
		result := Panel(th, role, "My Panel", lines, width)
		for i, line := range strings.Split(result, "\n") {
			lw := lipgloss.Width(line)
			assert.LessOrEqual(t, lw, width,
				"Panel line %d exceeds width %d: %q", i+1, width, line)
		}
	})

	t.Run("contains_content_lines", func(t *testing.T) {
		result := Panel(th, role, "My Panel", lines, 80)
		plain := stripANSI(result)
		assert.Contains(t, plain, "First line of content")
		assert.Contains(t, plain, "Second line of content")
	})

	t.Run("narrow_width_still_renders", func(t *testing.T) {
		result := Panel(th, role, "Narrow", lines, 30)
		plain := stripANSI(result)
		assert.Contains(t, plain, "Narrow")
	})
}
