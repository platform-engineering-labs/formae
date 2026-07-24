// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestQueryBar_EditingLifecycle(t *testing.T) {
	q := NewQueryBar(theme.New("formae"), "state:InProgress")
	assert.Equal(t, "state:InProgress", q.Query())
	assert.False(t, q.Focused())

	q = q.Focus()
	assert.True(t, q.Focused())

	// type " x", then apply
	q, applied := q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	assert.False(t, applied)
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	q, applied = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, applied)
	assert.False(t, q.Focused())
	assert.Equal(t, "state:InProgress x", q.Query())
}

func TestQueryBar_EscReverts(t *testing.T) {
	q := NewQueryBar(theme.New("formae"), "a")
	q = q.Focus()
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	q, applied := q.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, applied)
	assert.False(t, q.Focused())
	assert.Equal(t, "a", q.Query())
}

func TestQueryBar_BackspaceAndClear(t *testing.T) {
	q := NewQueryBar(theme.New("formae"), "ab")
	q = q.Focus()
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	q, applied := q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, applied)
	assert.Equal(t, "a", q.Query())

	q = q.Focus()
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "", q.Query())
}

func TestQueryBar_ViewStates(t *testing.T) {
	th := theme.New("formae")
	empty := NewQueryBar(th, "")
	assert.Contains(t, empty.View(80), "/: query")

	active := NewQueryBar(th, "state:InProgress")
	v := active.View(80)
	// The applied state is marked with the accent "/" prefix (not a dot) and
	// shows the query text plus the edit hint.
	assert.Contains(t, v, "/")
	assert.NotContains(t, v, "●")
	assert.Contains(t, v, "state:InProgress")
	assert.Contains(t, v, "/: edit query")

	editing := active.Focus()
	v = editing.View(80)
	assert.Contains(t, v, "█")
	assert.Contains(t, v, "enter: apply")
	assert.Contains(t, v, "esc: cancel")
}

// TestQueryBar_WithThemePreservesEditingState gates the PLA-348 fix for a
// live theme swap (e.g. the Omarchy watcher's async ApplyThemeMsg) arriving
// while the user is mid-edit. Before the fix, Model.ApplyTheme rebuilt the
// bar via NewQueryBar(t, q.Query()), which only carries over the applied
// query and zero-values focused/edit — silently un-focusing the bar and
// discarding the in-progress edit buffer. WithTheme must swap only the theme
// field and leave focus/edit/applied untouched.
func TestQueryBar_WithThemePreservesEditingState(t *testing.T) {
	q := NewQueryBar(theme.New("quiet"), "state:InProgress")
	q = q.Focus()
	q, applied := q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	require.False(t, applied)
	require.True(t, q.Focused(), "precondition: bar is focused mid-edit")

	themed := q.WithTheme(theme.New("rich"))

	assert.True(t, themed.Focused(), "WithTheme must not steal focus mid-edit")
	assert.Equal(t, "state:InProgress", themed.Query(), "WithTheme must not touch the applied query")

	v := themed.View(80)
	assert.Contains(t, v, "x", "in-progress edit buffer must survive WithTheme")
	assert.Contains(t, v, "█", "still rendering in edit mode after WithTheme")

	// Finish the edit to prove the buffer (not just focus) really survived.
	themed, applied = themed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, applied)
	assert.Equal(t, "state:InProgressx", themed.Query(), "edit buffer content survived WithTheme and applied correctly")
}

// TestQueryBar_WithThemePreservesUnfocusedState checks the simpler case: an
// applied-but-not-editing bar keeps its applied query across WithTheme too.
func TestQueryBar_WithThemePreservesUnfocusedState(t *testing.T) {
	q := NewQueryBar(theme.New("quiet"), "state:Failed")
	themed := q.WithTheme(theme.New("rich"))
	assert.False(t, themed.Focused())
	assert.Equal(t, "state:Failed", themed.Query())
}
