// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// QueryBar is the shared query/search editor used by both the status-command
// TUI and the inventory TUI so the two views offer an identical "edit the
// query" interaction. It is a pure value type — callers copy it freely.
//
// Behaviour:
//   - Focus() enters edit mode seeded with the applied query.
//   - Update() edits the buffer; enter applies (returns applied=true), esc
//     cancels the edit and keeps the previously applied query.
//   - Query() returns the applied query (empty string means "no query").
type QueryBar struct {
	th      *theme.Theme
	applied string
	edit    string
	focused bool
}

// NewQueryBar creates a QueryBar with an initial applied query.
func NewQueryBar(th *theme.Theme, initial string) QueryBar {
	return QueryBar{th: th, applied: initial}
}

// Focus enters edit mode, seeding the editable buffer with the applied query.
func (q QueryBar) Focus() QueryBar {
	q.focused = true
	q.edit = q.applied
	return q
}

// Focused reports whether the bar is in edit mode.
func (q QueryBar) Focused() bool { return q.focused }

// Query returns the currently applied query.
func (q QueryBar) Query() string { return q.applied }

// Update handles a key message while the bar is focused. The second return
// value is true when the user pressed enter (the query was applied).
func (q QueryBar) Update(msg tea.KeyMsg) (QueryBar, bool) {
	switch msg.Type {
	case tea.KeyEnter:
		q.applied = strings.TrimSpace(q.edit)
		q.focused = false
		return q, true
	case tea.KeyEsc:
		q.focused = false
		return q, false
	case tea.KeyBackspace:
		if len(q.edit) > 0 {
			q.edit = q.edit[:len(q.edit)-1]
		}
	case tea.KeyCtrlU:
		q.edit = ""
	case tea.KeyRunes:
		q.edit += string(msg.Runes)
	}
	return q, false
}

// View renders the two-line query bar (separator + content) for the given width.
// An active query is marked with an accent "/" prefix — the same affordance
// shown while editing — so the applied and editing states read consistently.
func (q QueryBar) View(width int) string {
	sep := lipgloss.NewStyle().Foreground(q.th.Palette.Border).Render(strings.Repeat("─", width))
	subtle := lipgloss.NewStyle().Foreground(q.th.Palette.TextSubtle)
	var left, right string
	switch {
	case q.focused:
		slash := lipgloss.NewStyle().Foreground(q.th.Palette.PrimaryAccent).Render("/")
		cursor := lipgloss.NewStyle().Foreground(q.th.Palette.TextPrimary).Render("█")
		left = "  " + slash + " " + q.edit + cursor
		right = subtle.Render("enter: apply  esc: cancel") + "  "
	case q.applied != "":
		slash := lipgloss.NewStyle().Foreground(q.th.Palette.PrimaryAccent).Render("/")
		query := lipgloss.NewStyle().Foreground(q.th.Palette.TextSecondary).Render(q.applied)
		left = "  " + slash + " " + query
		right = subtle.Render("/: edit query") + "  "
	default:
		left = "  " + subtle.Render("/: query")
	}
	content := left + PadBetween(width, left, right) + right
	return sep + "\n" + content
}
