// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

// ---------------------------------------------------------------------------
// helpers shared by sort tests
// ---------------------------------------------------------------------------

func buildSortTestModel(t *testing.T, rows []row, maxRows int) Model {
	t.Helper()
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		MaxRows:  maxRows,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Deliver the rows directly.
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	return mm.(Model)
}

// ---------------------------------------------------------------------------
// s sorts by the highlighted column (sortHi), which defaults to column 0.
// Mirrors the status-command TUI: no modal selector.
// ---------------------------------------------------------------------------

func TestSort_SortsByHighlightedColumn(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := mm.(Model)

	assert.Equal(t, 0, result.tabs[TabResources].sortCol, "s must sort by the highlighted column (0)")
	assert.Equal(t, components.SortAsc, result.tabs[TabResources].sortDir, "a fresh column sorts ascending")
}

// ---------------------------------------------------------------------------
// Pressing s again on the active column toggles the direction; header shows ▼.
// ---------------------------------------------------------------------------

func TestSort_ToggleDesc(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // asc
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // toggle desc
	result := mm.(Model)

	assert.Equal(t, components.SortDesc, result.tabs[TabResources].sortDir, "second s must toggle to SortDesc")
	assert.Contains(t, result.View(), "▼", "View must contain ▼ after sort desc applied")
}

// ---------------------------------------------------------------------------
// →← move the sort-column highlight without sorting; s then sorts the new one.
// ---------------------------------------------------------------------------

func TestSort_ArrowsMoveHighlightThenSort(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	// → moves the highlight to column 1 but does not sort.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mid := mm.(Model)
	assert.Equal(t, 1, mid.tabs[TabResources].sortHi, "→ must move the highlight to column 1")
	assert.Equal(t, -1, mid.tabs[TabResources].sortCol, "→ alone must not apply a sort")

	// s then sorts by the highlighted column (1).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.Equal(t, 1, mm.(Model).tabs[TabResources].sortCol, "s must sort the highlighted column (1)")
}

// ---------------------------------------------------------------------------
// ← wraps around the column set.
// ---------------------------------------------------------------------------

func TestSort_LeftWrapsAround(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyLeft})
	n := len(m.specs[TabResources].columns)
	assert.Equal(t, n-1, mm.(Model).tabs[TabResources].sortHi, "← from column 0 must wrap to the last column")
}

// ---------------------------------------------------------------------------
// R1 re-cap pin — sort desc → visible rows are the global top-N, not a
// locally-re-sorted first page.
// ---------------------------------------------------------------------------

func TestSort_R1RecapPin(t *testing.T) {
	rows := buildFixtureResources(10)
	m := buildSortTestModel(t, rows, 3)

	var mm tea.Model = m
	// s sorts by column 0 (NativeID) ascending.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := mm.(Model)
	assert.Equal(t, 0, result.tabs[TabResources].sortCol)
	assert.Equal(t, components.SortAsc, result.tabs[TabResources].sortDir)

	vis, total := result.tabs[TabResources].visible(3)
	assert.Equal(t, 10, total, "total must be 10 (all rows)")
	require.Len(t, vis, 3, "visible must be capped at 3")
	assert.Equal(t, "arn:aws:iam:::role/app", vis[0].cells[0], "first visible row must be the global asc minimum")
	assert.Equal(t, "arn:aws:rds:::db/primary", vis[1].cells[0])
	assert.Equal(t, "arn:aws:s3:::my-bucket", vis[2].cells[0])

	// Toggle to desc: another s on the same column.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result = mm.(Model)
	assert.Equal(t, components.SortDesc, result.tabs[TabResources].sortDir)

	visDesc, _ := result.tabs[TabResources].visible(3)
	require.Len(t, visDesc, 3)
	assert.Equal(t, "subnet-0abc123456789b", visDesc[0].cells[0], "first visible row must be the global desc maximum")
	assert.Equal(t, "subnet-0abc123456789a", visDesc[1].cells[0])
	assert.Equal(t, "sg-0123456789ab", visDesc[2].cells[0])
}

// ---------------------------------------------------------------------------
// Exact-fill after a sort is applied.
// ---------------------------------------------------------------------------

func TestSort_ExactFillAfterSort(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	lines := strings.Split(mm.(Model).View(), "\n")
	assert.Equal(t, 24, len(lines), "View must fill exactly 24 lines after a sort is applied")
}

// ---------------------------------------------------------------------------
// Golden: a column highlighted (moved) and sorted descending.
// ---------------------------------------------------------------------------

func TestGolden_SortApplied(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: buildFixtureResources(5)})

	// Move the highlight to Type (col 2) and sort descending.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // asc
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // desc

	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}
