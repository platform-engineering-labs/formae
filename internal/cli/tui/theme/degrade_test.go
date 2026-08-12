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
// Note: each built-in's specific color choices (e.g. hue picked for Done) are
// out of scope here. The test only confirms every role degrades cleanly
// through the termenv profile hierarchy, not what any particular role's
// degraded value is.
func TestThemeDegrades(t *testing.T) {
	// Save and restore the global lipgloss color profile so this test does not
	// bleed into other tests running in the same process.
	originalProfile := termenv.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(originalProfile)
	})

	themeNames := []string{"quiet", "rich", "colorblind"}
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

// selectionRowProbe renders role as a Background the same way the inventory
// view's own cursor-row probe does (highlightCursorRow), so this test's
// escape-sequence comparison matches what that probe actually inspects.
func selectionRowProbe(role lipgloss.AdaptiveColor) string {
	return lipgloss.NewStyle().Background(role).Render("x")
}

// TestSelectionDiffersFromBaseWhenDegraded is a byte-identity guard for the
// inventory view's cursor-row probe: highlightCursorRow locates the selected
// row by searching each rendered line for the raw SGR escape sequence
// Background(Selection) produces. If Selection and Base ever degraded to the
// identical escape sequence at a given color profile, that search could match
// the wrong row — or every row sharing the panel background — instead of
// uniquely identifying the cursor row.
//
// This is a byte-identity check, not a claim about perceptual separation on a
// real 16-color terminal: the terminal's actual color mapping is unknowable
// to us. It only pins that termenv's own degradation keeps the two escape
// sequences distinct at each profile the probe relies on.
func TestSelectionDiffersFromBaseWhenDegraded(t *testing.T) {
	// Save and restore the global lipgloss color profile so this test does not
	// bleed into other tests running in the same process.
	originalProfile := termenv.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(originalProfile)
	})

	profiles := []struct {
		name    string
		profile termenv.Profile
	}{
		{"ANSI256", termenv.ANSI256},
		{"ANSI", termenv.ANSI},
	}

	for _, themeName := range builtinNames() {
		th := New(themeName)
		for _, prof := range profiles {
			t.Run(themeName+"/"+prof.name, func(t *testing.T) {
				lipgloss.SetColorProfile(prof.profile)

				selection := selectionRowProbe(th.Palette.Selection)
				base := selectionRowProbe(th.Palette.Base)

				assert.NotEqual(t, base, selection,
					"Selection and Base degrade to the identical escape sequence at %s — the inventory view's cursor-row probe could no longer tell them apart", prof.name)
			})
		}
	}
}
