// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// filterBarLines is the number of body-region lines consumed by the filter bar
// when it is visible (input line + dim rule line).
const filterBarLines = 2

// filterBar is a lightweight wrapper around bubbles/textinput for the
// inventory client-side live filter. It is owned by the root Model as UI
// state (not per-tab state — the focus state is global, but the value is
// read from/written to tabs[active].filter on every keystroke).
//
// Behaviour:
//   - While focused: every keystroke updates the active tab's filter live.
//   - enter: unfocuses, filter stays applied.
//   - esc: clears the active tab's filter AND unfocuses.
//   - Only textinput keys + enter/esc + ctrl+c are routed while focused.
type filterBar struct {
	ti textinput.Model
}

// newFilterBar creates a filterBar with the correct prompt.
func newFilterBar() filterBar {
	ti := textinput.New()
	ti.Prompt = "/: "
	return filterBar{ti: ti}
}

// focus focuses the textinput and sets its value to the current filter.
func (f filterBar) focus(currentFilter string) filterBar {
	f.ti.SetValue(currentFilter)
	f.ti.Focus()
	f.ti.CursorEnd()
	return f
}

// blur unfocuses the textinput.
func (f filterBar) blur() filterBar {
	f.ti.Blur()
	return f
}

// value returns the current textinput value.
func (f filterBar) value() string {
	return f.ti.Value()
}

// setValue sets the textinput value without changing focus.
func (f filterBar) setValue(v string) filterBar {
	f.ti.SetValue(v)
	return f
}

// renderLines renders the 2-line filter bar for the body region.
// Returns exactly filterBarLines (2) strings:
//   - Line 0: the textinput line ("  /: <value>█" when focused, "  /: <value>" when blurred)
//   - Line 1: a dim rule line spanning width
//
// When th is nil (unit tests without theme), renders plain text.
func (f filterBar) renderLines(th *theme.Theme, width int) []string {
	// Line 0: input line.
	inputRendered := f.ti.View()
	// Pad to width.
	inputW := lipgloss.Width(inputRendered)
	if inputW < width {
		inputRendered += strings.Repeat(" ", width-inputW)
	}

	// Line 1: dim rule.
	ruleStr := strings.Repeat("─", width)
	if th != nil {
		dimStyle := lipgloss.NewStyle().Foreground(th.Palette.Border)
		ruleStr = dimStyle.Render(ruleStr)
	}

	return []string{inputRendered, ruleStr}
}
