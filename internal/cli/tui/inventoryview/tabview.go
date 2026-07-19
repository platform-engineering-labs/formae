// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
			title:     t.spec.title,
			entity:    t.spec.entity,
			columns:   cols,
			fetch:     t.spec.fetch,
			styleCell: t.spec.styleCell,
		}
		t.table = components.NewTable(t.th, cols)
	}

	// Grow columns to fill a wide terminal. When every column fits with room to
	// spare, distribute the surplus proportionally to the declared widths so
	// values consume the available space instead of truncating against unused
	// slack. Growing to exactly the width budget (width − 2·numCols of padding
	// accounting) keeps the responsive-hiding pass from dropping a column, and
	// mirrors the shrink path's conservative margin. The two paths are mutually
	// exclusive: shrink triggers only when the priority-0 columns overflow.
	allSum := 0
	for _, c := range cols {
		allSum += c.Width + 2
	}
	if allSum < width && len(cols) > 0 {
		available := width - 2*len(cols)
		totalDeclared := 0
		for _, c := range cols {
			totalDeclared += c.Width
		}
		if totalDeclared > 0 && available > totalDeclared {
			surplus := available - totalDeclared
			distributed := 0
			for i, c := range cols {
				add := surplus * c.Width / totalDeclared
				cols[i].Width += add
				distributed += add
			}
			// Award the rounding remainder (< numCols) to the first column so
			// the budget is filled exactly and deterministically.
			cols[0].Width += surplus - distributed
			t.spec = tabSpec{
				title:     t.spec.title,
				entity:    t.spec.entity,
				columns:   cols,
				fetch:     t.spec.fetch,
				styleCell: t.spec.styleCell,
			}
			t.table = components.NewTable(t.th, cols)
		}
	}

	// Record the final (possibly-shrunk-or-grown) column widths so sync can use
	// them for truncation before styling.
	t.effectiveCols = make([]components.Column, len(t.spec.columns))
	copy(t.effectiveCols, t.spec.columns)

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

// applyCellStyles replaces plain cell text with styled equivalents in a slice
// of ANSI-encoded table lines. bubbles/table uses runewidth.Truncate (not
// ANSI-aware) so styled strings cannot be passed directly to SetRows — instead
// sync records the intended plain→styled replacements (t.styledCells) and this
// function applies them after tbl.View() produces the rendered output.
//
// The replacement is bounded to each column's visual-character slice in the
// rendered line to prevent a cell value substring from matching an earlier
// column. For example, a Label "yes-prod" must not swallow the styled
// replacement meant for the Discoverable "yes" two columns to the right.
//
// Column layout (bubbles/table): each visible column occupies Width+2 visual
// chars (1-space padding on each side). We compute visual start offsets from
// visibleCols/effCols widths, strip ANSI from the line to find the plain text
// within the column's visual slice, then replace in the original ANSI line by
// walking it to map visual positions back to byte positions.
//
// headerLines = 2 is a coupling to bubbles/table's header rendering (one header
// row + one separator row). If bubbles changes its layout this constant needs updating.
func applyCellStyles(lines []string, cells [][]styledCell, tbl components.Table, effCols []components.Column) []string {
	if len(cells) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)

	visIdx := tbl.VisibleColumnIndexes() // original col indexes that appear in the rendered line

	// Build a map from original col index → position within visible cols (0-based).
	colPos := make(map[int]int, len(visIdx))
	for pos, origIdx := range visIdx {
		colPos[origIdx] = pos
	}

	// Compute each visible column's visual start offset and content width.
	// bubbles renders each cell padded to exactly Width visual chars with no
	// inter-column spacing; the "+2" in visibleColumns is for the internal
	// fit-check accounting only (not rendered padding).
	colVisStart := make([]int, len(visIdx)) // visual start of each column's content area
	colVisWidth := make([]int, len(visIdx)) // visual width of each column's content area
	vOffset := 0
	for pos, origIdx := range visIdx {
		w := 0
		if origIdx < len(effCols) {
			w = effCols[origIdx].Width
		}
		colVisStart[pos] = vOffset
		colVisWidth[pos] = w
		vOffset += w // columns are packed adjacently without inter-column spacing
	}

	const headerLines = 2 // bubbles header row + separator row
	dataStart := headerLines
	for row, styledRow := range cells {
		lineIdx := dataStart + row
		if lineIdx >= len(out) {
			break
		}
		line := out[lineIdx]
		strippedRunes := []rune(ansi.Strip(line))

		for _, sc := range styledRow {
			if sc.styled == sc.plain || sc.plain == "" {
				continue
			}
			pos, ok := colPos[sc.col]
			if !ok {
				continue // column not visible
			}
			vStart := colVisStart[pos]
			vEnd := vStart + colVisWidth[pos]
			if vStart >= len(strippedRunes) {
				continue
			}
			if vEnd > len(strippedRunes) {
				vEnd = len(strippedRunes)
			}

			// Find the plain text within the column's visual slice of the stripped line.
			// Use rune-based slicing so multi-byte chars (e.g. "…") are not split.
			sliceRunes := strippedRunes[vStart:vEnd]
			plainRunes := []rune(sc.plain)
			idx := runeIndex(sliceRunes, plainRunes)
			if idx < 0 {
				continue // plain text not found within this column's slice
			}

			// The plain text starts at visual position vStart+idx in the stripped line.
			// Walk the ANSI line to find the byte offset of that visual position,
			// then replace the plain text runes with the styled string.
			plainVisStart := vStart + idx
			plainVisEnd := plainVisStart + len(plainRunes)
			line = replaceInAnsiLine(line, plainVisStart, plainVisEnd, sc.styled)
			// Re-strip after replacement for any subsequent cells in this row.
			strippedRunes = []rune(ansi.Strip(line))
		}
		out[lineIdx] = line
	}
	return out
}

// replaceInAnsiLine replaces the runes at visual positions [visStart, visEnd)
// in an ANSI-encoded line with the styled string. It walks the line rune by
// rune, skipping ANSI escape sequences (which consume no visual columns), to
// find the byte offsets corresponding to the visual range.
// visStart and visEnd are rune (visual character) counts, not byte offsets.
func replaceInAnsiLine(line string, visStart, visEnd int, styled string) string {
	byteStart := -1
	byteEnd := -1
	vis := 0
	i := 0
	for i < len(line) {
		if vis == visStart {
			byteStart = i
		}
		if vis == visEnd {
			byteEnd = i
			break
		}
		if line[i] == '\x1b' {
			// Skip the entire CSI escape sequence (ESC [ ... m).
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++
				for j < len(line) && line[j] != 'm' {
					j++
				}
				if j < len(line) {
					j++ // consume 'm'
				}
			}
			i = j
			continue
		}
		// Decode one rune (handles multi-byte UTF-8 chars like "…").
		_, runeSize := utf8.DecodeRuneInString(line[i:])
		vis++
		i += runeSize
	}
	if byteEnd == -1 {
		byteEnd = i
	}
	if byteStart == -1 || byteStart > len(line) || byteEnd > len(line) || byteStart > byteEnd {
		return line
	}
	return line[:byteStart] + styled + line[byteEnd:]
}

// highlightHeaderColumn re-renders the header row so the sortHi column's cell
// carries a Selection background — the →← sort-column highlight. bubbles/table
// renders the whole header with one uniform TableHeader style, so we can strip
// it back to plain text and re-render the three segments (before / highlighted /
// after) from the same style, applying the background only to the middle slice.
// Columns are packed adjacently at exactly effCols[i].Width visual chars.
func highlightHeaderColumn(header string, tbl components.Table, effCols []components.Column, sortHi int, th *theme.Theme) string {
	visIdx := tbl.VisibleColumnIndexes()
	pos := -1
	for p, orig := range visIdx {
		if orig == sortHi {
			pos = p
			break
		}
	}
	if pos < 0 {
		return header // sortHi column is currently hidden (responsive drop)
	}
	start := 0
	for p := 0; p < pos; p++ {
		if orig := visIdx[p]; orig < len(effCols) {
			start += effCols[orig].Width
		}
	}
	width := 0
	if sortHi < len(effCols) {
		width = effCols[sortHi].Width
	}
	runes := []rune(ansi.Strip(header))
	if width <= 0 || start >= len(runes) {
		return header
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
	}
	// Re-render with a border-less style: TableHeader carries a bottom border
	// (the separator line, rendered separately as the next line), so reusing it
	// here would emit three stray border fragments. Match its foreground/bold.
	base := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary).Bold(true)
	hiStyle := base.Background(th.Palette.Selection)
	return base.Render(string(runes[:start])) +
		hiStyle.Render(string(runes[start:end])) +
		base.Render(string(runes[end:]))
}

// runeIndex finds the first occurrence of needle in haystack (both []rune).
// Returns the rune index (visual position), or -1 if not found.
func runeIndex(haystack, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j, r := range needle {
			if haystack[i+j] != r {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
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

	// Apply per-cell styling replacements (post-render, since bubbles/table
	// uses runewidth.Truncate which corrupts ANSI-escaped cell values).
	// Pass tbl (post-resize) and effectiveCols so applyCellStyles can bound
	// each replacement to its column's byte slice.
	effCols := t.effectiveCols
	if len(effCols) == 0 {
		effCols = t.spec.columns
	}
	tableLines = applyCellStyles(tableLines, t.styledCells, tbl, effCols)

	// Highlight the sort-target column header (sortHi) with a background, so the
	// →← highlight reads like the status-command TUI. The header row is line 0.
	if th != nil && len(tableLines) > 0 {
		tableLines[0] = highlightHeaderColumn(tableLines[0], tbl, effCols, t.sortHi, th)
	}

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

// statusLineNarrow returns an abbreviated status line for narrow terminals
// (width < narrowFooterThreshold). It drops the entity noun and appends compact
// key glyphs: "Showing N of M · ↑↓ enter / s q".
func (t tabModel) statusLineNarrow(maxRows int) string {
	_, total := t.visible(maxRows)
	shown := total
	if maxRows > 0 && shown > maxRows {
		shown = maxRows
	}
	return fmt.Sprintf("Showing %d of %d · ↑↓ enter / s q", shown, total)
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
