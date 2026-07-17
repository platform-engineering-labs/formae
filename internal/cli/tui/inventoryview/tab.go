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
	spec    tabSpec
	state   tabState
	allRows []row
	err     error //nolint:unused // set by later tasks (tabFailed path)
	filter  string
	sortCol int // -1 = server order
	sortDir components.SortDirection
	table   components.Table
	width   int //nolint:unused // set by later tasks (SetSize path)
	height  int //nolint:unused // set by later tasks (SetSize path)
}

// newTabModel creates a tabModel with sane defaults: sortCol -1 (server order),
// state tabNotLoaded, empty rows.
func newTabModel(th *theme.Theme, spec tabSpec) tabModel {
	return tabModel{
		spec:    spec,
		state:   tabNotLoaded,
		sortCol: -1,
		sortDir: components.SortNone,
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
	// Step 1: filter
	filtered := t.allRows
	if t.filter != "" {
		needle := strings.ToLower(t.filter)
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

// sync pushes the visible() cells into the table and sets the sort indicator.
// It never calls table.SortBy — the engine owns row ordering (R1).
// sync does not touch state or err — pipeline only.
func (t tabModel) sync(maxRows int) tabModel {
	vis, _ := t.visible(maxRows)

	cells := make([][]string, len(vis))
	for i, r := range vis {
		cells[i] = r.cells
	}

	t.table = t.table.SetRows(cells).SetSortState(t.sortCol, t.sortDir)
	return t
}
