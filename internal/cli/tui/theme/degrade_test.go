// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
)

// paletteRoles returns a slice of all AdaptiveColor values in the palette so
// that every color role participates in the degradation check.
func paletteRoles(p Palette) []lipgloss.AdaptiveColor {
	return []lipgloss.AdaptiveColor{
		p.Base,
		p.Surface,
		p.TextPrimary,
		p.TextSecondary,
		p.TextSubtle,
		p.Border,
		p.Selection,
		p.PrimaryAccent,
		p.SecondaryAccent,
		p.Error,
		p.ErrorSubtle,
		p.ErrorBright,
		p.Warning,
		p.Done,
		p.InProgress,
		p.Pending,
	}
}

// renderRoles renders "sample" with each palette role as foreground color and
// concatenates the results. This exercises every color through lipgloss so
// termenv can degrade them to the active profile.
func renderRoles(p Palette) string {
	roles := paletteRoles(p)
	var sb strings.Builder
	for _, role := range roles {
		sb.WriteString(lipgloss.NewStyle().Foreground(role).Render("sample"))
	}
	return sb.String()
}

// TestThemeDegrades verifies that both themes degrade cleanly through the
// termenv color-profile hierarchy (TrueColor → ANSI256 → ANSI/16).
//
// Key assertion at the ANSI (16-color) level: the rendered output must contain
// NO truecolor escape sequences (\x1b[38;2;…). If a palette role bypassed
// lipgloss/termenv and hardcoded a truecolor escape directly, this test would
// catch it.
//
// Note: the classic theme's Done role is literal green — that is an intentional
// palette choice (out of scope here). The test confirms it degrades to a 16-
// color green code, not that it avoids green.
func TestThemeDegrades(t *testing.T) {
	// Save and restore the global lipgloss color profile so this test does not
	// bleed into other tests running in the same process.
	originalProfile := termenv.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(originalProfile)
	})

	themeNames := []string{"formae", "classic"}
	profiles := []struct {
		name    string
		profile termenv.Profile
	}{
		{"TrueColor", termenv.TrueColor},
		{"ANSI256", termenv.ANSI256},
		{"ANSI", termenv.ANSI},
	}

	for _, themeName := range themeNames {
		th := New(themeName)
		for _, prof := range profiles {
			t.Run(themeName+"/"+prof.name, func(t *testing.T) {
				lipgloss.SetColorProfile(prof.profile)

				out := renderRoles(th.Palette)

				// Must produce non-empty output at every profile.
				assert.NotEmpty(t, out, "rendered output must not be empty")

				// At ANSI (16-color), NO truecolor escape sequences must appear.
				if prof.profile == termenv.ANSI {
					assert.NotContains(t, out, "\x1b[38;2;",
						"truecolor fg escape found in 16-color profile — a palette role is not degrading through lipgloss/termenv")
					assert.NotContains(t, out, "\x1b[48;2;",
						"truecolor bg escape found in 16-color profile — a palette role is not degrading through lipgloss/termenv")
				}
			})
		}
	}
}
