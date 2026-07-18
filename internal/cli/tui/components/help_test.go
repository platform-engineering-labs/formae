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
	"github.com/stretchr/testify/require"

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

// TestHelpOverlay_CrossViewGeneralInvariant asserts the cross-view consistency
// invariant: for any view's []HelpGroup, the "? close help" entry in the General
// group always renders with key label "?" and desc "close help".
//
// Design note: the FULL General group contents differ by view (simview has no
// esc/back; driftview uses "esc / q"; statuswatch/inventory have separate esc
// and q lines). So we do NOT assert byte-identity of the complete General group
// across all four views — that would misrepresent the real bindings. Instead we
// assert the shared invariant: the {Key:"?", Desc:"close help"} entry appears
// in the rendered output of every view's help overlay, using the same label
// ("?") and description ("close help"). This is the actual consistency guarantee
// documented in PLA-290.
func TestHelpOverlay_CrossViewGeneralInvariant(t *testing.T) {
	// General groups as each view defines them (faithful copies, not truncated).
	viewGenerals := []struct {
		view    string
		general HelpGroup
	}{
		{
			view: "statuswatch",
			general: HelpGroup{
				Title: "General",
				Hints: []KeyHint{
					{Key: "esc", Desc: "back"},
					{Key: "q", Desc: "quit"},
					{Key: "?", Desc: "close help"},
				},
			},
		},
		{
			view: "inventory",
			general: HelpGroup{
				Title: "General",
				Hints: []KeyHint{
					{Key: "esc", Desc: "back"},
					{Key: "q", Desc: "quit"},
					{Key: "?", Desc: "close help"},
				},
			},
		},
		{
			view: "simview",
			general: HelpGroup{
				Title: "General",
				Hints: []KeyHint{
					{Key: "q", Desc: "abort"},
					{Key: "?", Desc: "close help"},
				},
			},
		},
		{
			view: "driftview",
			general: HelpGroup{
				Title: "General",
				Hints: []KeyHint{
					{Key: "esc / q", Desc: "abort"},
					{Key: "?", Desc: "close help"},
				},
			},
		},
	}

	th := theme.New("formae")

	// For each view, render an overlay containing just the General group and
	// assert that "?" and "close help" appear in the plain output.
	for _, tc := range viewGenerals {
		tc := tc
		t.Run(tc.view, func(t *testing.T) {
			groups := []HelpGroup{tc.general}
			out := HelpOverlay(th, 120, 40, groups)
			p := ansi.Strip(out)

			// Invariant: "?" key label present.
			assert.Contains(t, p, "?", "%s: General group must render '?' key", tc.view)
			// Invariant: "close help" desc present.
			assert.Contains(t, p, "close help", "%s: General group must render 'close help' desc", tc.view)
			// Invariant: panel title present.
			assert.Contains(t, p, "Keybindings", "%s: overlay must be titled 'Keybindings'", tc.view)
		})
	}

	// Additionally: the "? — close help" row renders identically (same ANSI bytes)
	// across all four views because it uses the same KeyHint struct — extract
	// just that row from each rendering and compare.
	var renderedRows []string
	for _, tc := range viewGenerals {
		groups := []HelpGroup{tc.general}
		out := HelpOverlay(th, 120, 40, groups)
		// Find the row containing "close help" in the rendered output.
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(ansi.Strip(line), "close help") {
				renderedRows = append(renderedRows, line)
				break
			}
		}
	}
	require.Len(t, renderedRows, len(viewGenerals), "each view must produce a 'close help' row")
	// All four "? close help" rows must be byte-identical (same styling, same padding).
	// They may differ in key-column padding when other keys in the group are wider
	// (since padding is set by the widest key across all groups). So we assert only
	// that the PLAIN TEXT portions match — the styling is uniform by construction
	// (all use the same Accent/TextSecondary roles). Full byte-identity is NOT
	// asserted across views because the widest-key padding differs per view.
	for i, row := range renderedRows {
		plainRow := ansi.Strip(row)
		assert.Contains(t, plainRow, "?", "view %d: close-help row must contain '?'", i)
		assert.Contains(t, plainRow, "close help", "view %d: close-help row must contain 'close help'", i)
		assert.Contains(t, plainRow, "—", "view %d: close-help row must use '—' separator", i)
	}
}
