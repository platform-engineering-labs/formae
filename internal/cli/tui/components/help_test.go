// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

// fixedGroups returns a deterministic 3-group fixture with keys of varying
// widths (exercising the widest-key-across-groups alignment).
func fixedGroups() []HelpGroup {
	return []HelpGroup{
		{
			Title: "Navigate",
			Hints: []KeyHint{
				{Key: "↑↓ / j k", Desc: "select"},
				{Key: "PgUp/PgDn", Desc: "page"},
				{Key: "→ ← / h l", Desc: "column"},
			},
		},
		{
			Title: "Actions",
			Hints: []KeyHint{
				{Key: "enter", Desc: "details"},
				{Key: "space", Desc: "expand"},
				{Key: "/", Desc: "search"},
			},
		},
		{
			Title: "General",
			Hints: []KeyHint{
				{Key: "esc", Desc: "back"},
				{Key: "q", Desc: "quit"},
				{Key: "?", Desc: "close help"},
			},
		},
	}
}

func TestHelpOverlay_Golden(t *testing.T) {
	th := theme.New("formae")
	out := HelpOverlay(th, 120, 40, fixedGroups())
	tuitest.RequireGolden(t, []byte(out))
}

func TestHelpOverlay_SmallTerminal(t *testing.T) {
	th := theme.New("formae")

	// Should not panic; renders even when terminal is smaller than the panel.
	out := HelpOverlay(th, 10, 5, fixedGroups())
	assert.NotEmpty(t, out)

	// Verify that when panel > dimensions, it does NOT apply centering offset
	// (i.e. is placed top-left — output starts immediately without leading blank lines).
	plain := ansi.Strip(out)
	lines := strings.Split(plain, "\n")
	// The first line should be non-empty (starts with border character, not blank).
	assert.NotEmpty(t, strings.TrimSpace(lines[0]), "small terminal: first line should not be blank (top-left placement)")

	// The panel width should fit the groups (not be artificially truncated).
	assert.Greater(t, lipgloss.Width(out), 0)
}

func TestHelpOverlay_KeyColumnAlignment(t *testing.T) {
	th := theme.New("formae")
	groups := []HelpGroup{
		{
			Title: "Navigate",
			Hints: []KeyHint{
				{Key: "↑↓", Desc: "short key"},
				{Key: "PgUp/PgDn", Desc: "long key"},
			},
		},
		{
			Title: "Actions",
			Hints: []KeyHint{
				{Key: "enter", Desc: "medium key"},
			},
		},
	}
	out := HelpOverlay(th, 120, 40, groups)
	plain := ansi.Strip(out)

	// The widest key is "PgUp/PgDn" (width 9). All rows should have the key
	// portion padded to the same visual width. We measure by finding " — " and
	// taking the visual width (lipgloss.Width) of the key prefix.
	var keyWidths []int
	for _, line := range strings.Split(plain, "\n") {
		// Binding rows start with two spaces then key content.
		// Look for the separator in the trimmed version.
		trimmed := strings.TrimSpace(line)
		if idx := strings.Index(trimmed, " — "); idx > 0 {
			prefix := trimmed[:idx]
			keyWidths = append(keyWidths, lipgloss.Width(prefix))
		}
	}
	assert.NotEmpty(t, keyWidths, "should have binding rows")
	// All key-column widths must be equal (aligned).
	first := keyWidths[0]
	for _, w := range keyWidths[1:] {
		assert.Equal(t, first, w, "key column not aligned across groups")
	}
}

func TestHelpOverlay_ContainsGroupTitles(t *testing.T) {
	th := theme.New("formae")
	out := HelpOverlay(th, 120, 40, fixedGroups())
	plain := ansi.Strip(out)

	assert.Contains(t, plain, "Navigate")
	assert.Contains(t, plain, "Actions")
	assert.Contains(t, plain, "General")
	assert.Contains(t, plain, "Keybindings")
}

func TestHelpOverlay_ContainsHints(t *testing.T) {
	th := theme.New("formae")
	out := HelpOverlay(th, 120, 40, fixedGroups())
	plain := ansi.Strip(out)

	assert.Contains(t, plain, "select")
	assert.Contains(t, plain, "page")
	assert.Contains(t, plain, "close help")
}
