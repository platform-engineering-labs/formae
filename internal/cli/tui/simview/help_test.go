// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package simview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

// TestSimView_HelpOverlay_Golden asserts the help overlay renders correctly
// at a fixed size. The golden captures the full view with showHelp=true.
func TestSimView_HelpOverlay_Golden(t *testing.T) {
	m := makeModel(100, 32)
	// Open the overlay.
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)
	assert.True(t, m.showHelp, "showHelp must be true after pressing ?")
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestSimView_HelpOverlay_ContainsGroups checks the overlay contains the
// expected group titles and key hints.
func TestSimView_HelpOverlay_ContainsGroups(t *testing.T) {
	m := makeModel(100, 32)
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)

	out := ansi.Strip(m.View())

	assert.Contains(t, out, "Navigate", "overlay must contain Navigate group")
	assert.Contains(t, out, "Actions", "overlay must contain Actions group")
	assert.Contains(t, out, "General", "overlay must contain General group")
	assert.Contains(t, out, "Keybindings", "overlay must contain Keybindings title")
	assert.Contains(t, out, "select", "overlay must contain select hint")
	assert.Contains(t, out, "expand", "overlay must contain expand hint")
	assert.Contains(t, out, "confirm", "overlay must contain confirm hint")
	assert.Contains(t, out, "abort", "overlay must contain abort hint")
	assert.Contains(t, out, "close help", "overlay must contain close help hint")
}

// TestSimView_HelpToggle_QuestionMark verifies ? opens then closes the overlay.
func TestSimView_HelpToggle_QuestionMark(t *testing.T) {
	m := makeModel(100, 32)
	assert.False(t, m.showHelp, "showHelp must be false initially")

	// Press ? → open
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)
	assert.True(t, m.showHelp, "? must open overlay")

	// Press ? again → close
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)
	assert.False(t, m.showHelp, "second ? must close overlay")
}

// TestSimView_HelpModal_EscCloses verifies esc closes the overlay.
func TestSimView_HelpModal_EscCloses(t *testing.T) {
	m := makeModel(100, 32)
	// Open overlay
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)
	assert.True(t, m.showHelp, "overlay must be open")

	// Press esc → closes overlay (does NOT quit)
	var cmd tea.Cmd
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	assert.False(t, m.showHelp, "esc must close overlay")
	assert.Nil(t, cmd, "esc while overlay open must NOT quit")
}

// TestSimView_HelpModal_SwallowsKeys verifies that normal keys are swallowed
// while the overlay is open: the underlying state must not change.
func TestSimView_HelpModal_SwallowsKeys(t *testing.T) {
	m := makeModel(100, 32)
	// Open overlay
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)

	// Record state before key presses.
	sortColBefore := m.sortCol[kindResource]
	cursorBefore := m.cursor
	decisionBefore := m.decision
	showHelpBefore := m.showHelp

	// Press 's' (sort) — must be swallowed.
	var cmd tea.Cmd
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	mAfterS := mm.(Model)
	assert.Equal(t, sortColBefore, mAfterS.sortCol[kindResource], "s must be swallowed: sort column unchanged")
	assert.Equal(t, showHelpBefore, mAfterS.showHelp, "s must not close overlay")
	assert.Nil(t, cmd, "s must return no cmd")

	// Press 'y' (confirm) — must be swallowed, no quit, decision unchanged.
	m = mAfterS
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	mAfterY := mm.(Model)
	assert.Equal(t, decisionBefore, mAfterY.decision, "y must be swallowed: decision unchanged")
	assert.Equal(t, showHelpBefore, mAfterY.showHelp, "y must not close overlay")
	assert.Nil(t, cmd, "y must return no cmd")

	// Press Down — must be swallowed, cursor unchanged.
	m = mAfterY
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mAfterDown := mm.(Model)
	assert.Equal(t, cursorBefore, mAfterDown.cursor, "Down must be swallowed: cursor unchanged")
	assert.Equal(t, showHelpBefore, mAfterDown.showHelp, "Down must not close overlay")
	assert.Nil(t, cmd, "Down must return no cmd")
}

// TestSimView_HelpModal_CtrlCStillQuits verifies ctrl+c quits even with overlay open.
func TestSimView_HelpModal_CtrlCStillQuits(t *testing.T) {
	m := makeModel(100, 32)
	// Open overlay
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = mm.(Model)
	assert.True(t, m.showHelp, "overlay must be open")

	// ctrl+c must still quit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.NotNil(t, cmd, "ctrl+c must return a quit cmd even when overlay is open")
}

// TestSimView_HelpGroups_GeneralInvariant verifies that the General group
// in simHelpGroups has "?" key with "close help" desc — the cross-view invariant.
func TestSimView_HelpGroups_GeneralInvariant(t *testing.T) {
	groups := simHelpGroups()

	var generalGroup *components.HelpGroup
	for i := range groups {
		if groups[i].Title == "General" {
			generalGroup = &groups[i]
			break
		}
	}
	assert.NotNil(t, generalGroup, "simHelpGroups must have a General group")

	var found bool
	for _, h := range generalGroup.Hints {
		if h.Key == "?" && h.Desc == "close help" {
			found = true
			break
		}
	}
	assert.True(t, found, `General group must contain {Key: "?", Desc: "close help"}`)
}
