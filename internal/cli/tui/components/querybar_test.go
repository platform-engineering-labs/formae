// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

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
