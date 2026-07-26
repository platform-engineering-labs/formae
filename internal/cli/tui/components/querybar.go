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
	// cursor is the caret position within edit, measured in RUNES (0 == before
	// the first rune, len([]rune(edit)) == after the last). Left/Right move it;
	// inserts and deletes act relative to it.
	cursor int
}

// NewQueryBar creates a QueryBar with an initial applied query.
func NewQueryBar(th *theme.Theme, initial string) QueryBar {
	return QueryBar{th: th, applied: initial}
}

// Focus enters edit mode, seeding the editable buffer with the applied query
// and placing the caret at the end of it.
func (q QueryBar) Focus() QueryBar {
	q.focused = true
	q.edit = q.applied
	q.cursor = len([]rune(q.edit))
	return q
}

// WithTheme returns a copy of q with only the theme swapped, preserving all
// other state (applied query, in-progress edit buffer, focus). Use this for
// a live theme change (e.g. the Omarchy watcher firing an async
// ApplyThemeMsg) instead of rebuilding via NewQueryBar, which would silently
// drop a mid-edit buffer and steal focus from the user.
func (q QueryBar) WithTheme(th *theme.Theme) QueryBar {
	q.th = th
	return q
}

// Focused reports whether the bar is in edit mode.
func (q QueryBar) Focused() bool { return q.focused }

// Query returns the currently applied query.
func (q QueryBar) Query() string { return q.applied }

// Update handles a key message while the bar is focused. The second return
// value is true when the user pressed enter (the query was applied).
func (q QueryBar) Update(msg tea.KeyMsg) (QueryBar, bool) {
	runes := []rune(q.edit)
	switch msg.Type {
	case tea.KeyEnter:
		q.applied = strings.TrimSpace(q.edit)
		q.focused = false
		return q, true
	case tea.KeyEsc:
		q.focused = false
		return q, false
	case tea.KeyLeft:
		if q.cursor > 0 {
			q.cursor--
		}
	case tea.KeyRight:
		if q.cursor < len(runes) {
			q.cursor++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		q.cursor = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		q.cursor = len(runes)
	case tea.KeyBackspace:
		if q.cursor > 0 {
			q.edit = string(runes[:q.cursor-1]) + string(runes[q.cursor:])
			q.cursor--
		}
	case tea.KeyDelete:
		if q.cursor < len(runes) {
			q.edit = string(runes[:q.cursor]) + string(runes[q.cursor+1:])
		}
	case tea.KeyCtrlU:
		q.edit = ""
		q.cursor = 0
	case tea.KeyRunes, tea.KeySpace:
		ins := msg.Runes
		if msg.Type == tea.KeySpace {
			ins = []rune{' '}
		}
		q.edit = string(runes[:q.cursor]) + string(ins) + string(runes[q.cursor:])
		q.cursor += len(ins)
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
		left = "  " + slash + " " + q.renderEditWithCursor()
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

// renderEditWithCursor renders the edit buffer with the caret drawn at the
// cursor position: a solid block when the caret sits past the last rune, or the
// rune under the caret drawn in reverse video when it sits mid-buffer.
func (q QueryBar) renderEditWithCursor() string {
	runes := []rune(q.edit)
	if q.cursor >= len(runes) {
		block := lipgloss.NewStyle().Foreground(q.th.Palette.TextPrimary).Render("█")
		return q.edit + block
	}
	caret := lipgloss.NewStyle().
		Foreground(q.th.Palette.Base).
		Background(q.th.Palette.TextPrimary)
	before := string(runes[:q.cursor])
	under := string(runes[q.cursor])
	after := string(runes[q.cursor+1:])
	return before + caret.Render(under) + after
}
