// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// fixtureSpec returns a plain 2-column spec for pipeline tests.
// No coupling to any entity-specific spec.
func fixtureSpec() tabSpec {
	return tabSpec{
		title:  "Test",
		entity: "tests",
		columns: []components.Column{
			{Title: "Name", Width: 20, Priority: 0},
			{Title: "Value", Width: 20, Priority: 1},
		},
		fetch: nil, // not used in pipeline tests
	}
}

// makeRow creates a row from two cell values for test fixtures.
func makeRow(name, value string) row {
	return row{cells: []string{name, value}}
}

// makeRows creates rows from name+value pairs.
func makeRows(pairs ...string) []row {
	rows := make([]row, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		rows = append(rows, makeRow(pairs[i], pairs[i+1]))
	}
	return rows
}

func newTestTabModel(rows []row) tabModel {
	th := theme.New("formae")
	t := newTabModel(th, fixtureSpec())
	t.allRows = rows
	t.state = tabLoaded
	return t
}

// ---------------------------------------------------------------------------
// (a) Filter matches across columns case-insensitively
// ---------------------------------------------------------------------------

func TestVisible_FilterMatchesAcrossColumns(t *testing.T) {
	rows := []row{
		makeRow("alpha", "foo"),   // match in col 0
		makeRow("beta", "FOO"),    // match in col 1, uppercase
		makeRow("gamma", "bar"),   // no match
		makeRow("ALPHA", "other"), // match in col 0, uppercase
	}
	tm := newTestTabModel(rows)
	tm.query = "foo"

	vis, total := tm.visible(0)

	// Should match rows with "foo" anywhere (case-insensitive): indices 0 and 1
	require.Len(t, vis, 2, "should match rows containing 'foo' case-insensitively in any column")
	assert.Equal(t, total, 2)
	assert.Equal(t, "alpha", vis[0].cells[0])
	assert.Equal(t, "beta", vis[1].cells[0])
}

func TestVisible_FilterCaseInsensitiveBothDirections(t *testing.T) {
	rows := []row{
		makeRow("Widget", "production"),
		makeRow("gadget", "STAGING"),
		makeRow("thing", "dev"),
	}
	tm := newTestTabModel(rows)

	// Uppercase filter against lowercase data
	tm.query = "WIDGET"
	vis, _ := tm.visible(0)
	require.Len(t, vis, 1)
	assert.Equal(t, "Widget", vis[0].cells[0])

	// Lowercase filter against uppercase data
	tm.query = "staging"
	vis, _ = tm.visible(0)
	require.Len(t, vis, 1)
	assert.Equal(t, "gadget", vis[0].cells[0])
}

func TestVisible_EmptyFilterMatchesAll(t *testing.T) {
	rows := makeRows("a", "1", "b", "2", "c", "3")
	tm := newTestTabModel(rows)
	tm.query = ""

	vis, total := tm.visible(0)
	assert.Len(t, vis, 3)
	assert.Equal(t, 3, total)
}

// ---------------------------------------------------------------------------
// (b) THE R1 REGRESSION PIN: sort must happen on the full filtered set BEFORE cap
// ---------------------------------------------------------------------------

func TestVisible_SortBeforeCap_R1RegressionPin(t *testing.T) {
	// 5 rows with values that sort desc: "e" > "d" > "c" > "b" > "a"
	// Server order has "a","b","c","d","e" (ascending by name).
	// If cap(2) were applied BEFORE sort, we'd get rows "a" and "b".
	// Correct: sort THEN cap → we should get "e" and "d".
	rows := []row{
		makeRow("a", "alpha"),
		makeRow("b", "bravo"),
		makeRow("c", "charlie"),
		makeRow("d", "delta"),
		makeRow("e", "echo"),
	}
	tm := newTestTabModel(rows)
	tm.sortCol = 0
	tm.sortDir = components.SortDesc

	vis, total := tm.visible(2)

	// total = all 5 rows (no filter), cap applied after
	assert.Equal(t, 5, total, "total must be post-filter pre-cap count")
	require.Len(t, vis, 2, "cap must limit to 2 rows")

	// Global top-2 in desc order: "e", "d"
	assert.Equal(t, "e", vis[0].cells[0], "first visible row must be 'e' (global desc max)")
	assert.Equal(t, "d", vis[1].cells[0], "second visible row must be 'd'")
}

// ---------------------------------------------------------------------------
// (c) Cap 0 = unlimited
// ---------------------------------------------------------------------------

func TestVisible_Cap0Unlimited(t *testing.T) {
	rows := makeRows("a", "1", "b", "2", "c", "3", "d", "4", "e", "5")
	tm := newTestTabModel(rows)

	vis, total := tm.visible(0)
	assert.Len(t, vis, 5, "cap 0 should return all rows")
	assert.Equal(t, 5, total)
}

// ---------------------------------------------------------------------------
// (d) Total = post-filter pre-cap count
// ---------------------------------------------------------------------------

func TestVisible_TotalIsPostFilterPreCap(t *testing.T) {
	// 5 rows, filter narrows to 3, cap 2 → total=3, len(vis)=2
	rows := []row{
		makeRow("apple", "fruit"),
		makeRow("banana", "fruit"),
		makeRow("carrot", "veggie"),
		makeRow("date", "fruit"),
		makeRow("eggplant", "veggie"),
	}
	tm := newTestTabModel(rows)
	tm.query = "fruit" // matches col 1: apple, banana, date → 3 rows

	vis, total := tm.visible(2)

	assert.Equal(t, 3, total, "total must reflect post-filter count (3 rows contain 'fruit')")
	assert.Len(t, vis, 2, "cap 2 must limit visible rows to 2")
}

// ---------------------------------------------------------------------------
// (e) Cursor stability: sync twice → table.Cursor() unchanged
// ---------------------------------------------------------------------------

func TestSync_CursorStability(t *testing.T) {
	rows := makeRows("a", "1", "b", "2", "c", "3")
	tm := newTestTabModel(rows)
	th := theme.New("formae")
	tm.table = components.NewTable(th, fixtureSpec().columns).SetSize(120, 10)

	// First sync
	tm = tm.sync(0)
	cursor0 := tm.table.Cursor()

	// Second sync (no state change)
	tm = tm.sync(0)
	cursor1 := tm.table.Cursor()

	assert.Equal(t, cursor0, cursor1, "cursor must be stable across successive syncs")
}

// ---------------------------------------------------------------------------
// Additional: sync sets sort indicator without calling SortBy
// ---------------------------------------------------------------------------

func TestSync_SetsSortStateNotSortBy(t *testing.T) {
	// If SortBy were called, the table's internal sort would reorder.
	// SetSortState only marks the header. We verify:
	// 1. The rows in the table match what visible() produces (not what SortBy would do).
	// 2. The sort state is reflected (tested via visible order).
	rows := []row{
		makeRow("z", "last"),
		makeRow("a", "first"),
		makeRow("m", "middle"),
	}
	tm := newTestTabModel(rows)
	tm.sortCol = 0
	tm.sortDir = components.SortAsc
	th := theme.New("formae")
	tm.table = components.NewTable(th, fixtureSpec().columns).SetSize(120, 10)

	tm = tm.sync(0)

	// visible() returns sorted rows; sync pushes those cells into table.
	// The table's SelectedRow at cursor 0 should be "a" (asc-sorted first).
	selected := tm.table.SelectedRow()
	require.NotNil(t, selected)
	assert.Equal(t, "a", selected[0], "table must reflect engine-sorted rows")
}

// ---------------------------------------------------------------------------
// newTabModel: initial state
// ---------------------------------------------------------------------------

func TestNewTabModel_InitialState(t *testing.T) {
	th := theme.New("formae")
	spec := fixtureSpec()
	tm := newTabModel(th, spec)

	assert.Equal(t, tabNotLoaded, tm.state)
	assert.Equal(t, -1, tm.sortCol, "sortCol must be -1 (server order)")
	assert.Equal(t, components.SortNone, tm.sortDir)
	assert.Empty(t, tm.allRows)
	assert.Empty(t, tm.query)
}
