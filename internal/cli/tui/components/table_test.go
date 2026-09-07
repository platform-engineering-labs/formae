// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func testColumns() []Column {
	return []Column{
		{Title: "Label", Width: 20, Priority: 0},
		{Title: "Type", Width: 24, Priority: 1},
		{Title: "Stack", Width: 14, Priority: 2},
		{Title: "State", Width: 10, Priority: 3},
	}
}

func testRows() [][]string {
	return [][]string{
		{"web-1", "AWS::EC2::Instance", "production", "Done"},
		{"api-db", "AWS::RDS::DBInstance", "production", "Failed"},
		{"my-bucket", "AWS::S3::Bucket", "staging", "Done"},
	}
}

func newTestTable(width int) Table {
	th := theme.New("formae")
	tbl := NewTable(th, testColumns())
	tbl = tbl.SetRows(testRows())
	return tbl.SetSize(width, 10)
}

func TestTable_AllColumnsWhenWide(t *testing.T) {
	view := ansi.Strip(newTestTable(120).View())
	for _, title := range []string{"Label", "Type", "Stack", "State"} {
		assert.Contains(t, view, title)
	}
}

func TestTable_DropsLowPriorityColumnsWhenNarrow(t *testing.T) {
	// 40 cells: only Label (always) and one more column can fit
	view := ansi.Strip(newTestTable(40).View())
	assert.Contains(t, view, "Label")
	assert.NotContains(t, view, "State") // Priority 3 dropped first
	assert.NotContains(t, view, "Stack") // then Priority 2
}

func TestTable_SelectedRowReturnsFullRowEvenWhenColumnsHidden(t *testing.T) {
	tbl := newTestTable(40)
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.Equal(t, []string{"api-db", "AWS::RDS::DBInstance", "production", "Failed"}, tbl.SelectedRow())
}

func TestTable_SortByColumn(t *testing.T) {
	tbl := newTestTable(120).SortBy(0, SortAsc)
	assert.Equal(t, []string{"api-db", "AWS::RDS::DBInstance", "production", "Failed"}, tbl.SelectedRow())

	view := ansi.Strip(tbl.View())
	assert.Contains(t, view, "Label ▲")

	tbl = tbl.SortBy(0, SortDesc)
	assert.Contains(t, ansi.Strip(tbl.View()), "Label ▼")
	assert.Equal(t, []string{"web-1", "AWS::EC2::Instance", "production", "Done"}, tbl.SelectedRow())
}

func TestTable_CursorNavigation(t *testing.T) {
	tbl := newTestTable(120)
	assert.Equal(t, 0, tbl.Cursor())
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.Equal(t, 2, tbl.Cursor())
	tbl, _ = tbl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	assert.Equal(t, 1, tbl.Cursor())
}

func TestTable_Golden(t *testing.T) {
	tuitest.RequireGolden(t, []byte(newTestTable(80).View()))
}

func TestTable_SetSortState_IndicatorWithoutReorder(t *testing.T) {
	th := theme.New("formae")
	tbl := NewTable(th, []Column{{Title: "A", Width: 5}, {Title: "B", Width: 5}})
	tbl = tbl.SetRows([][]string{{"zz", "1"}, {"aa", "2"}}).SetSize(40, 10)

	tbl = tbl.SetSortState(0, SortAsc)

	view := tbl.View()
	assert.Contains(t, view, "A ▲")
	// Row order must be untouched (SortBy would have moved "aa" first).
	assert.Equal(t, []string{"zz", "1"}, tbl.SelectedRow())
}

func TestTable_SetSortState_ClearedBySortNone(t *testing.T) {
	th := theme.New("formae")
	tbl := NewTable(th, []Column{{Title: "A", Width: 5}})
	tbl = tbl.SetRows([][]string{{"x"}}).SetSize(40, 10).SetSortState(0, SortDesc)
	tbl = tbl.SetSortState(-1, SortNone)
	assert.NotContains(t, tbl.View(), "▼")
}

// TestVisibleColumnIndexes_WideAllVisible verifies all columns are visible at wide width.
func TestVisibleColumnIndexes_WideAllVisible(t *testing.T) {
	th := theme.New("formae")
	cols := testColumns() // Label(20,0), Type(24,1), Stack(14,2), State(10,3)
	tbl := NewTable(th, cols).SetSize(200, 10)
	got := tbl.VisibleColumnIndexes()
	assert.Equal(t, []int{0, 1, 2, 3}, got)
}

// TestVisibleColumnIndexes_NarrowDropsLowPriority verifies high-priority columns
// are dropped first when width is narrow.
func TestVisibleColumnIndexes_NarrowDropsLowPriority(t *testing.T) {
	th := theme.New("formae")
	// testColumns: Label(20,p0), Type(24,p1), Stack(14,p2), State(10,p3)
	// At width=40: total = (20+2)+(24+2)+(14+2)+(10+2) = 76 > 40
	// Drop State(p3)=12 → 64 > 40
	// Drop Stack(p2)=16 → 48 > 40
	// Drop Type(p1)=26 → 22 ≤ 40 → only Label visible
	tbl := NewTable(th, testColumns()).SetSize(40, 10)
	got := tbl.VisibleColumnIndexes()
	// Only Priority 0 column (Label at index 0) should remain.
	assert.Equal(t, []int{0}, got)
}

// TestTable_SetThemeRebuildsHeaderAndCellStyles covers a live theme swap
// (the Omarchy watcher firing theme.ApplyThemeMsg): Table
// bakes header/cell/selected-row styles into the wrapped bubbles/table.Model
// once, at NewTable time (see NewTable), and every subsequent View() reuses
// those baked styles verbatim — a bare struct-field swap of a *theme.Theme
// held elsewhere never touches them. SetTheme must rebuild the baked styles
// from the new theme while preserving all other state (rows, sort, cursor).
//
// quiet and rich share identical neutral/border/selection colors by design
// (only accents/status colors differ), so this test builds a synthetic
// re-themed copy with different neutrals — the same shape of change the
// Omarchy live-follow watcher produces when the OS palette changes.
func TestTable_SetThemeRebuildsHeaderAndCellStyles(t *testing.T) {
	base := theme.New("quiet")
	tbl := NewTable(base, testColumns())
	tbl = tbl.SetRows(testRows()).SetSize(80, 10)
	before := tbl.View()

	retheme := *base
	retheme.Palette.TextSecondary = lipgloss.AdaptiveColor{Light: "#FF00FF", Dark: "#FF00FF"}
	retheme.Palette.TextPrimary = lipgloss.AdaptiveColor{Light: "#00FFFF", Dark: "#00FFFF"}
	retheme.Palette.Border = lipgloss.AdaptiveColor{Light: "#00FF00", Dark: "#00FF00"}
	retheme.Styles = theme.NewStyles(retheme.Palette)

	tbl = tbl.SetTheme(&retheme)
	after := tbl.View()

	assert.NotEqual(t, before, after,
		"header/cell styling must be rebuilt from the new theme, not left stale")
	// State survives the swap: same rows, same cursor — this is a re-theme,
	// not a reconstruction (which is why SetTheme exists instead of NewTable).
	assert.Equal(t, testRows()[0], tbl.SelectedRow())
	assert.Equal(t, 0, tbl.Cursor())
}

// manyRows returns n distinct rows, more than any test viewport can show.
func manyRows(n int) [][]string {
	rows := make([][]string, n)
	for i := range rows {
		id := string(rune('a' + i%26))
		rows[i] = []string{"row-" + id, "AWS::EC2::Instance", "production", "Done"}
	}
	return rows
}

// TestTable_SetSizeFitsHeightBudget pins that a sized table renders exactly the
// requested number of lines. bubbles derives the row viewport height by
// subtracting the rendered header height, so measuring a stale header made the
// table one line taller than its budget — and the caller trimmed that line,
// which is the cursor's line once the table is scrolled to the bottom.
func TestTable_SetSizeFitsHeightBudget(t *testing.T) {
	// Size first, then fill: that is the order the inventory view uses, and the
	// order that exposed the stale header measurement.
	tbl := NewTable(theme.New("quiet"), testColumns())
	tbl = tbl.SetSize(80, 12).SetRows(manyRows(40))
	assert.Equal(t, 12, lipgloss.Height(tbl.View()))
}

// TestTable_ScrollSurvivesIdenticalSetRows pins that re-pushing unchanged rows
// (the inventory engine re-syncs on every keypress) does not scroll the cursor
// row off screen: reprojecting resets the wrapped viewport's offset, so an
// unchanged data set must be a no-op.
func TestTable_ScrollSurvivesIdenticalSetRows(t *testing.T) {
	rows := manyRows(40)
	tbl := NewTable(theme.New("quiet"), testColumns())
	tbl = tbl.SetRows(rows).SetSize(80, 12)

	down := tea.KeyMsg{Type: tea.KeyDown}
	for i := 0; i < len(rows)-1; i++ {
		tbl, _ = tbl.Update(down)
		tbl = tbl.SetSortState(-1, SortNone).SetRows(rows).SetSize(80, 12)
	}

	assert.Equal(t, len(rows)-1, tbl.Cursor())
	last := ansi.Strip(tbl.View())
	assert.Contains(t, last, tbl.SelectedRow()[0],
		"the cursor row must still be rendered after scrolling to the bottom")
}
