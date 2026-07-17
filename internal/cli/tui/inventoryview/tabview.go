// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// setSize stores the terminal dimensions on the model, budgets the table
// region, and calls t.table.SetSize. The table region height is exactly
// t.height (the caller passes the body budget, not the total terminal height).
//
// Column-width overflow guard (design R8): when even the priority-0 columns'
// declared widths don't fit width, their widths are shrunk proportionally
// (min 4 per column) so the table never overflows.
func (t tabModel) setSize(width, height int) tabModel {
	t.width = width
	t.height = height

	// Recompute column widths. Check whether priority-0 columns fit, and if
	// not shrink them proportionally.
	cols := make([]components.Column, len(t.spec.columns))
	copy(cols, t.spec.columns)

	// Sum of priority-0 columns (including bubbles' 2-cell padding per col).
	p0Sum := 0
	for _, c := range cols {
		if c.Priority == 0 {
			p0Sum += c.Width + 2
		}
	}

	if p0Sum > width {
		// Need to shrink priority-0 cols proportionally. Available budget is
		// width - (2 * number_of_p0_cols) to leave room for padding.
		p0Count := 0
		for _, c := range cols {
			if c.Priority == 0 {
				p0Count++
			}
		}
		available := width - 2*p0Count
		if available < 4*p0Count {
			available = 4 * p0Count
		}
		totalDeclared := 0
		for _, c := range cols {
			if c.Priority == 0 {
				totalDeclared += c.Width
			}
		}
		for i, c := range cols {
			if c.Priority != 0 {
				continue
			}
			newW := c.Width * available / totalDeclared
			if newW < 4 {
				newW = 4
			}
			cols[i].Width = newW
		}
		t.spec = tabSpec{
			title:   t.spec.title,
			entity:  t.spec.entity,
			columns: cols,
			fetch:   t.spec.fetch,
		}
		t.table = components.NewTable(t.th, cols)
	}

	t.table = t.table.SetSize(width, height)
	return t
}

// view returns the body-region lines for the tab, exactly filling t.height
// (padding with blank lines as needed).
//
//   - tabLoading: centered "<spinView> Loading <entity>…"
//   - tabFailed:  components.ErrorPanel rendering of t.err + dim hint "r: retry"
//   - tabLoaded + 0 allRows: centered "No <entity> found."
//   - tabLoaded otherwise: table.View() lines; when post-filter total exceeds
//     the cap, appends a dashed marker line and a dim count line.
func (t tabModel) view(th *theme.Theme, maxRows int, spinView string) []string {
	h := t.height
	if h <= 0 {
		h = 1
	}

	var lines []string

	switch {
	case t.state == tabLoading:
		lines = t.loadingView(th, spinView)

	case t.state == tabFailed:
		lines = t.failedView(th)

	case t.state == tabLoaded && len(t.allRows) == 0:
		lines = t.emptyView(th)

	default:
		// tabLoaded with rows (or tabNotLoaded — render table which is empty)
		lines = t.loadedView(th, maxRows)
	}

	// Pad or trim to exactly h lines.
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// loadingView renders the loading state: centered "<spinView> Loading <entity>…".
func (t tabModel) loadingView(th *theme.Theme, spinView string) []string {
	h := t.height
	if h <= 0 {
		h = 1
	}

	msg := spinView + " Loading " + t.spec.entity + "…"
	centered := centerText(msg, t.width)
	if th != nil {
		centered = th.Styles.StatusPending.Render(centered)
	}

	lines := make([]string, h)
	midRow := h / 2
	lines[midRow] = centered
	return lines
}

// failedView renders the error state: ErrorPanel + "r: retry" hint.
func (t tabModel) failedView(th *theme.Theme) []string {
	msg := "unknown error"
	if t.err != nil {
		msg = t.err.Error()
	}

	panel := components.ErrorPanel{
		Title:   "Error",
		Message: msg,
	}

	var panelLines []string
	if th != nil {
		rendered := panel.Render(th, t.width)
		panelLines = strings.Split(rendered, "\n")
	} else {
		panelLines = []string{msg}
	}

	// Hint line "r: retry" in dim style.
	hintPlain := "r: retry"
	var hintLine string
	if th != nil {
		hintLine = th.Styles.KeybindingDesc.Render(hintPlain)
	} else {
		hintLine = hintPlain
	}

	lines := append(panelLines, hintLine)
	return lines
}

// emptyView renders the empty state: centered "No <entity> found."
func (t tabModel) emptyView(th *theme.Theme) []string {
	h := t.height
	if h <= 0 {
		h = 1
	}

	msg := "No " + t.spec.entity + " found."
	centered := centerText(msg, t.width)
	if th != nil {
		centered = th.Styles.StatusPending.Render(centered)
	}

	lines := make([]string, h)
	midRow := h / 2
	lines[midRow] = centered
	return lines
}

// loadedView renders the table and optional truncation marker.
func (t tabModel) loadedView(th *theme.Theme, maxRows int) []string {
	_, total := t.visible(maxRows)
	shown := total
	if maxRows > 0 && shown > maxRows {
		shown = maxRows
	}

	// Determine whether we will show truncation markers (2 lines).
	needsMarker := maxRows > 0 && total > maxRows
	markerLines := 0
	if needsMarker {
		markerLines = 2
	}

	// Re-size the table to leave room for the marker lines.
	tbl := t.table
	tableH := t.height - markerLines
	if tableH < 1 {
		tableH = 1
	}
	tbl = tbl.SetSize(t.width, tableH)

	tableOut := tbl.View()
	tableLines := strings.Split(tableOut, "\n")

	// Remove trailing empty lines that bubbles/table may append.
	for len(tableLines) > 0 && tableLines[len(tableLines)-1] == "" {
		tableLines = tableLines[:len(tableLines)-1]
	}

	var lines []string
	lines = append(lines, tableLines...)

	// Truncation marker when cap applies.
	if needsMarker {
		remaining := total - shown
		markerLine := dashedMarker(t.width)
		countMsg := fmt.Sprintf("%d more results not shown. Use /: search to filter.", remaining)

		if th != nil {
			dimStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
			lines = append(lines, dimStyle.Render(markerLine))
			lines = append(lines, dimStyle.Render(countMsg))
		} else {
			lines = append(lines, markerLine)
			lines = append(lines, countMsg)
		}
	}

	return lines
}

// statusLine returns "Showing <shown> of <total> <entity>", with appropriate
// suffix for filtered/truncated cases.
func (t tabModel) statusLine(maxRows int) string {
	_, total := t.visible(maxRows)
	shown := total
	if maxRows > 0 && shown > maxRows {
		shown = maxRows
	}

	base := fmt.Sprintf("Showing %d of %d %s", shown, total, t.spec.entity)

	if t.filter != "" {
		return base + " (filtered)"
	}
	if maxRows > 0 && total > maxRows {
		return base + " — refine your query to see more"
	}
	return base
}

// centerText centers plain text within the given width by prepending spaces.
func centerText(s string, width int) string {
	msgWidth := lipgloss.Width(s)
	if msgWidth >= width || width <= 0 {
		return s
	}
	pad := (width - msgWidth) / 2
	return strings.Repeat(" ", pad) + s
}

// dashedMarker returns a "─ ─ ─ …" pattern repeated to fill width.
func dashedMarker(width int) string {
	if width <= 0 {
		return ""
	}
	unit := "─ "
	unitLen := 2
	count := width / unitLen
	if count == 0 {
		count = 1
	}
	s := strings.Repeat(unit, count)
	// Trim or pad to exact width.
	runes := []rune(s)
	if len(runes) > width {
		runes = runes[:width]
	}
	return string(runes)
}
