// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// tabState represents the loading state of a tab.
type tabState int

const (
	tabNotLoaded tabState = iota
	tabLoading
	tabLoaded
	tabFailed
)

// tabModel is the engine for a single inventory tab. It owns the
// filter→sort→cap pipeline over []row objects and hands only the resulting
// cells to the table for display. components.Table is a display/navigation
// shell only — the engine never calls SortBy on it (R1).
type tabModel struct {
	spec          tabSpec
	th            *theme.Theme
	state         tabState
	allRows       []row
	err           error
	query         string // applied query-bar text: server query (serverQuery tabs) or client substring
	sortCol       int    // -1 = server order
	sortDir       components.SortDirection
	sortHi        int // column highlighted by →← (the s-key sort target)
	table         components.Table
	width         int
	height        int
	effectiveCols []components.Column // post-shrink column widths from setSize
	styledCells   [][]styledCell      // per-row per-col plain→styled replacements from sync
}

// newTabModel creates a tabModel with sane defaults: state tabNotLoaded, empty
// rows, and a default ascending sort on the Label column (column 0) so the sort
// arrow is always visible and the list has a stable, readable default order.
func newTabModel(th *theme.Theme, spec tabSpec) tabModel {
	return tabModel{
		spec:    spec,
		th:      th,
		state:   tabNotLoaded,
		sortCol: 0, // Label column (leftmost) across all tabs
		sortDir: components.SortAsc,
		sortHi:  0,
		table:   components.NewTable(th, spec.columns),
	}
}

// visible applies the filter→sort→cap pipeline over allRows and returns
// the visible rows plus the post-filter total (before cap).
//
// Pipeline rules (D8/R1):
//  1. filter: case-insensitive substring match — a row matches if ANY cell
//     (full cell set, including columns hidden by responsive dropping) contains
//     the filter substring. Empty filter matches all.
//  2. sort: applied AFTER filter to the ENTIRE filtered set — stable
//     (sort.SliceStable), case-insensitive on cells[sortCol]. sortCol -1 or
//     sortDir SortNone → keep server order.
//  3. cap: applied LAST — maxRows > 0 && len(filtered-sorted) > maxRows →
//     truncate to first maxRows. maxRows 0 = unlimited.
//
// visible is PURE (no mutation, no table interaction).
func (t tabModel) visible(maxRows int) (vis []row, total int) {
	// Step 1: filter. serverQuery tabs are already filtered by the fetch, so the
	// client-side substring filter only applies to the non-serverQuery tabs.
	filtered := t.allRows
	if !t.spec.serverQuery && t.query != "" {
		needle := strings.ToLower(t.query)
		filtered = make([]row, 0, len(t.allRows))
		for _, r := range t.allRows {
			if rowMatchesFilter(r, needle) {
				filtered = append(filtered, r)
			}
		}
	}

	total = len(filtered)

	// Step 2: sort (on the full filtered set)
	if t.sortCol >= 0 && t.sortDir != components.SortNone {
		col := t.sortCol
		asc := t.sortDir == components.SortAsc
		sorted := make([]row, len(filtered))
		copy(sorted, filtered)
		sort.SliceStable(sorted, func(a, b int) bool {
			av := strings.ToLower(sorted[a].cells[col])
			bv := strings.ToLower(sorted[b].cells[col])
			if asc {
				return av < bv
			}
			return av > bv
		})
		filtered = sorted
	}

	// Step 3: cap
	vis = filtered
	if maxRows > 0 && len(vis) > maxRows {
		vis = vis[:maxRows]
	}

	return vis, total
}

// rowMatchesFilter reports whether any cell in r contains needle (already lower-cased).
func rowMatchesFilter(r row, needle string) bool {
	for _, cell := range r.cells {
		if strings.Contains(strings.ToLower(cell), needle) {
			return true
		}
	}
	return false
}

// styledCell records a plain-to-styled replacement for post-render processing.
// bubbles/table uses runewidth.Truncate (not ANSI-aware) on every cell value,
// so pre-styled strings passed to SetRows are always mangled. Instead, sync
// pushes PLAIN truncated text into the table and records the intended styled
// replacements. loadedView applies them after tbl.View() produces ANSI output.
//
// col is the original column index (0-based in the full column set, before
// responsive dropping). applyCellStyles uses it to bound the replacement to
// the correct column's byte slice in the rendered line.
type styledCell struct {
	col    int    // original column index in the full column set
	plain  string // the plain text we pushed into the table
	styled string // the styled replacement (styled != plain means replace)
}

// sync pushes the visible() cells into the table and sets the sort indicator.
// It never calls table.SortBy — the engine owns row ordering (R1).
// sync does not touch state or err — pipeline only.
//
// For each cell: plain text is first truncated to the column's effective width
// (post-shrink, as computed by setSize), then a styleCell application is
// RECORDED for post-render replacement in loadedView. The table always receives
// plain truncated text so that bubbles/table's runewidth-based internal
// truncation does not corrupt ANSI escape sequences.
func (t tabModel) sync(maxRows int) tabModel {
	vis, _ := t.visible(maxRows)

	// Resolve the effective column widths. effectiveCols is set by setSize;
	// fall back to spec.columns if setSize has not been called yet.
	effCols := t.effectiveCols
	if len(effCols) == 0 {
		effCols = t.spec.columns
	}

	cells := make([][]string, len(vis))
	styledCells := make([][]styledCell, len(vis))
	for i, r := range vis {
		row := make([]string, len(r.cells))
		styledRow := make([]styledCell, len(r.cells))
		for col, cell := range r.cells {
			// Step 1: truncate plain text to the column's final width.
			colWidth := 0
			if col < len(effCols) {
				colWidth = effCols[col].Width
			}
			plain := cell
			if colWidth > 0 {
				plain = components.Truncate(cell, colWidth)
			}
			row[col] = plain

			// Step 2: record the intended styled replacement when styleCell applies.
			styledRow[col] = styledCell{col: col, plain: plain, styled: plain}
			if t.spec.styleCell != nil {
				styled := t.spec.styleCell(t.th, col, plain)
				styledRow[col] = styledCell{col: col, plain: plain, styled: styled}
			}
		}
		cells[i] = row
		styledCells[i] = styledRow
	}

	// SetSortState MUST precede SetRows: SetRows runs applySort using the table's
	// current sortCol, so setting rows first would re-sort by the PREVIOUS column
	// while styledCells (from visible()) are ordered by the new one — a one-frame
	// mismatch that renders per-cell styles (e.g. "⚠ unmanaged") against the wrong
	// rows until the next event re-syncs. Setting the sort state first makes
	// SetRows sort by the new column, keeping both orders aligned.
	t.table = t.table.SetSortState(t.sortCol, t.sortDir).SetRows(cells)
	t.styledCells = styledCells
	return t
}
