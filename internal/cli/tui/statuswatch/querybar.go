// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

type queryBar struct {
	th      *theme.Theme
	applied string
	edit    string
	focused bool
}

func newQueryBar(th *theme.Theme, initial string) queryBar {
	return queryBar{th: th, applied: initial}
}

func (q queryBar) Focus() queryBar {
	q.focused = true
	q.edit = q.applied
	return q
}

func (q queryBar) Focused() bool { return q.focused }
func (q queryBar) Query() string { return q.applied }

func (q queryBar) Update(msg tea.KeyMsg) (queryBar, bool) {
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

func (q queryBar) View(width int) string {
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
		dot := lipgloss.NewStyle().Foreground(q.th.Palette.SecondaryAccent).Render("●")
		left = "  " + dot + " " + q.applied
		right = subtle.Render("/: edit query") + "  "
	default:
		left = "  " + subtle.Render("/: query")
	}
	content := left + components.PadBetween(width, left, right) + right
	return sep + "\n" + content
}
