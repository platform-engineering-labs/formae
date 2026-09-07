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

// runeKey is a tiny helper to send a single rune to the bar.
func runeKey(q QueryBar, r rune) QueryBar {
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return q
}

// TestQueryBar_CursorInsertMovesAndInserts verifies Left/Right move the text
// cursor and that typing inserts at the cursor, not always at the end.
func TestQueryBar_CursorInsertMidBuffer(t *testing.T) {
	q := NewQueryBar(theme.New("formae"), "abc")
	q = q.Focus() // cursor seeded at end (after "abc")

	// move left twice: cursor between 'a' and 'b'
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyLeft})
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyLeft})
	q = runeKey(q, 'X')

	q, applied := q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.True(t, applied)
	assert.Equal(t, "aXbc", q.Query())
}

// TestQueryBar_HomeEnd verifies Home jumps to the start and End to the end.
func TestQueryBar_HomeEnd(t *testing.T) {
	q := NewQueryBar(theme.New("formae"), "abc").Focus()
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyHome})
	q = runeKey(q, 'Y') // "Yabc"
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnd})
	q = runeKey(q, 'Z') // "YabcZ"
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "YabcZ", q.Query())
}

// TestQueryBar_BackspaceAndDeleteAtCursor verifies Backspace removes the rune
// before the cursor and Delete removes the rune at the cursor.
func TestQueryBar_BackspaceAndDeleteAtCursor(t *testing.T) {
	// Backspace mid-buffer: "abc", cursor after 'b', backspace removes 'b'.
	q := NewQueryBar(theme.New("formae"), "abc").Focus()
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyLeft}) // cursor between 'b' and 'c'
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "ac", q.Query())

	// Delete at start removes the first rune.
	q = NewQueryBar(theme.New("formae"), "abc").Focus()
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyHome})
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyDelete})
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "bc", q.Query())
}

// TestQueryBar_CursorClamps verifies Left past the start and Right past the end
// are clamped (no panic, insertion still lands at the boundary).
func TestQueryBar_CursorClamps(t *testing.T) {
	q := NewQueryBar(theme.New("formae"), "ab").Focus()
	for i := 0; i < 5; i++ {
		q, _ = q.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	q = runeKey(q, 'Z') // inserts at start -> "Zab"
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "Zab", q.Query())

	q = NewQueryBar(theme.New("formae"), "ab").Focus()
	for i := 0; i < 5; i++ {
		q, _ = q.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	q = runeKey(q, 'Z') // inserts at end -> "abZ"
	q, _ = q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "abZ", q.Query())
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

// TestQueryBar_WithThemePreservesEditingState covers a live theme swap
// (e.g. the Omarchy watcher's async ApplyThemeMsg) arriving while the user
// is mid-edit. Rebuilding the bar via NewQueryBar(t, q.Query()) would carry
// over only the applied query and zero-value focused/edit — silently
// un-focusing the bar and discarding the in-progress edit buffer. WithTheme
// must swap only the theme field and leave focus/edit/applied untouched.
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
