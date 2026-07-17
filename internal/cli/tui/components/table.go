// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"sort"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// Column describes a table column. Priority controls responsive hiding:
// columns with higher Priority are dropped first when the terminal is too
// narrow; Priority 0 columns are always shown.
type Column struct {
	Title    string
	Width    int
	Priority int
}

// SortDirection is the sort order applied to a column.
type SortDirection int

const (
	SortNone SortDirection = iota
	SortAsc
	SortDesc
)

// Table wraps bubbles/table with theming, responsive column hiding and
// column sorting. It has value semantics like the bubbles components.
type Table struct {
	inner   table.Model
	cols    []Column
	rows    [][]string // master data (full column set), in current sort order
	width   int
	sortCol int
	sortDir SortDirection
}

// NewTable creates a themed table with the given column set.
func NewTable(th *theme.Theme, cols []Column) Table {
	p := th.Palette

	styles := table.DefaultStyles()
	styles.Header = th.Styles.TableHeader
	styles.Cell = th.Styles.TableRow
	styles.Selected = lipgloss.NewStyle().
		Foreground(p.TextPrimary).
		Background(p.Selection)

	// The bubbles defaults bind d/f/u/b which collide with the shared
	// KeyMap (detail toggle, filter); restrict paging to ctrl+u/d and
	// pgup/pgdown.
	km := table.DefaultKeyMap()
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"))
	km.PageUp = key.NewBinding(key.WithKeys("pgup"))
	km.PageDown = key.NewBinding(key.WithKeys("pgdown"))

	inner := table.New(
		table.WithFocused(true),
		table.WithStyles(styles),
		table.WithKeyMap(km),
	)

	return Table{inner: inner, cols: cols, width: 80, sortCol: -1}
}

// SetRows replaces the table data. Each row must carry the full column set,
// including values for currently hidden columns.
func (t Table) SetRows(rows [][]string) Table {
	t.rows = rows
	return t.applySort().reproject()
}

// SetSize resizes the table and recomputes which columns fit.
func (t Table) SetSize(width, height int) Table {
	t.width = width
	t.inner.SetWidth(width)
	t.inner.SetHeight(height)
	return t.reproject()
}

// SortBy sorts rows by the given column and marks the header with a ▲/▼
// indicator. SortNone clears the indicator; subsequent SetRows calls then
// keep their given order.
func (t Table) SortBy(col int, dir SortDirection) Table {
	if col < 0 || col >= len(t.cols) || dir == SortNone {
		t.sortCol, t.sortDir = -1, SortNone
		return t.reproject()
	}
	t.sortCol, t.sortDir = col, dir
	return t.applySort().reproject()
}

// SetSortState marks the header sort indicator without reordering rows.
// Use this when the caller owns row ordering; SortBy both sorts and marks.
func (t Table) SetSortState(col int, dir SortDirection) Table {
	if col < 0 || col >= len(t.cols) || dir == SortNone {
		t.sortCol, t.sortDir = -1, SortNone
		return t.reproject()
	}
	t.sortCol, t.sortDir = col, dir
	return t.reproject()
}

// Update handles navigation key messages.
func (t Table) Update(msg tea.Msg) (Table, tea.Cmd) {
	var cmd tea.Cmd
	t.inner, cmd = t.inner.Update(msg)
	return t, cmd
}

// View renders the table.
func (t Table) View() string { return t.inner.View() }

// Cursor returns the selected row index.
func (t Table) Cursor() int { return t.inner.Cursor() }

// SelectedRow returns the full data row under the cursor, including values
// of hidden columns. Returns nil when the table is empty.
func (t Table) SelectedRow() []string {
	i := t.inner.Cursor()
	if i < 0 || i >= len(t.rows) {
		return nil
	}
	return t.rows[i]
}

func (t Table) applySort() Table {
	if t.sortDir == SortNone || t.sortCol < 0 {
		return t
	}
	col, asc := t.sortCol, t.sortDir == SortAsc
	sorted := make([][]string, len(t.rows))
	copy(sorted, t.rows)
	sort.SliceStable(sorted, func(a, b int) bool {
		if asc {
			return sorted[a][col] < sorted[b][col]
		}
		return sorted[a][col] > sorted[b][col]
	})
	t.rows = sorted
	return t
}

// reproject pushes the visible projection of columns and rows into the
// wrapped bubbles table.
func (t Table) reproject() Table {
	visible := visibleColumns(t.cols, t.width)

	bcols := make([]table.Column, 0, len(visible))
	for _, i := range visible {
		title := t.cols[i].Title
		if i == t.sortCol {
			switch t.sortDir {
			case SortAsc:
				title += " ▲"
			case SortDesc:
				title += " ▼"
			}
		}
		bcols = append(bcols, table.Column{Title: title, Width: t.cols[i].Width})
	}

	brows := make([]table.Row, 0, len(t.rows))
	for _, r := range t.rows {
		row := make(table.Row, 0, len(visible))
		for _, i := range visible {
			row = append(row, r[i])
		}
		brows = append(brows, row)
	}

	// Clear rows first: bubbles/table panics when rows are wider than the
	// current column set. That clamps the cursor to -1, so restore it after.
	cursor := max(t.inner.Cursor(), 0)
	t.inner.SetRows(nil)
	t.inner.SetColumns(bcols)
	t.inner.SetRows(brows)
	t.inner.SetCursor(min(cursor, len(brows)-1))
	return t
}

// visibleColumns returns indexes of the columns that fit in width, dropping
// the highest-Priority columns first (rightmost on ties). Priority 0
// columns are never dropped.
func visibleColumns(cols []Column, width int) []int {
	shown := make([]bool, len(cols))
	for i := range cols {
		shown[i] = true
	}
	total := func() int {
		sum := 0
		for i, c := range cols {
			if shown[i] {
				sum += c.Width + 2 // bubbles pads each cell by one on both sides
			}
		}
		return sum
	}
	for total() > width {
		drop := -1
		for i, c := range cols {
			if !shown[i] || c.Priority == 0 {
				continue
			}
			if drop == -1 || c.Priority >= cols[drop].Priority {
				drop = i
			}
		}
		if drop == -1 {
			break
		}
		shown[drop] = false
	}
	vis := make([]int, 0, len(cols))
	for i := range cols {
		if shown[i] {
			vis = append(vis, i)
		}
	}
	return vis
}
