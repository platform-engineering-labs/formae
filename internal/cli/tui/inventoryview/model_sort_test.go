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
// Test 1: Golden — selector open on Resources tab
// ---------------------------------------------------------------------------

func TestGolden_SortSelectorOpen(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m

	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Deliver resource rows via tabLoadedMsg.
	rows := buildFixtureResources(5)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	// Press 's' to open selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// ---------------------------------------------------------------------------
// Test 2: Apply sort via enter → check engine ordering
// ---------------------------------------------------------------------------

func TestSortSelector_ApplySortAsc(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	// Open selector.
	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model := mm.(Model)
	require.True(t, model.sortOpen, "sortOpen must be true after pressing 's'")
	assert.Equal(t, 0, model.sortCursor, "sortCursor must start at 0 when sortCol==-1")

	// Press enter → applies SortAsc on col 0.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := mm.(Model)

	assert.Equal(t, 0, result.tabs[TabResources].sortCol, "sortCol must be 0")
	assert.Equal(t, components.SortAsc, result.tabs[TabResources].sortDir, "sortDir must be SortAsc")
	assert.False(t, result.sortOpen, "sortOpen must be false after enter")
}

// ---------------------------------------------------------------------------
// Test 3: Re-enter on same column toggles to desc; header shows ▼
// ---------------------------------------------------------------------------

func TestSortSelector_ToggleDesc(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	// Pre-apply sort col 0 asc directly.
	m.tabs[TabResources].sortCol = 0
	m.tabs[TabResources].sortDir = components.SortAsc
	m.tabs[TabResources] = m.tabs[TabResources].sync(m.opts.MaxRows)

	var mm tea.Model = m
	// Open selector → sortCursor should start at applied col (0).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model := mm.(Model)
	require.True(t, model.sortOpen)
	assert.Equal(t, 0, model.sortCursor, "sortCursor must start at applied sortCol=0")

	// Press enter → toggles to SortDesc.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := mm.(Model)

	assert.Equal(t, components.SortDesc, result.tabs[TabResources].sortDir, "sortDir must toggle to SortDesc")
	assert.False(t, result.sortOpen, "sortOpen must be false after enter")

	// View must contain ▼.
	assert.Contains(t, result.View(), "▼", "View must contain ▼ after sort desc applied")
}

// ---------------------------------------------------------------------------
// Test 4: esc leaves prior ordering untouched
// ---------------------------------------------------------------------------

func TestSortSelector_EscLeavesSort(t *testing.T) {
	rows := buildFixtureResources(5)
	m := buildSortTestModel(t, rows, 0)

	// Pre-apply sort col 1 asc directly.
	m.tabs[TabResources].sortCol = 1
	m.tabs[TabResources].sortDir = components.SortAsc
	m.tabs[TabResources] = m.tabs[TabResources].sync(m.opts.MaxRows)

	var mm tea.Model = m
	// Open selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	// Move cursor left (toward col 0).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyLeft})
	// Press esc.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})

	result := mm.(Model)
	assert.Equal(t, 1, result.tabs[TabResources].sortCol, "sortCol must remain 1 after esc")
	assert.False(t, result.sortOpen, "sortOpen must be false after esc")
}

// ---------------------------------------------------------------------------
// Test 5: R1 re-cap pin — sort desc → visible rows are global top-N
// ---------------------------------------------------------------------------

func TestSortSelector_R1RecapPin(t *testing.T) {
	// Build 10 distinct rows with unique NativeIDs.
	rows := buildFixtureResources(10)
	m := buildSortTestModel(t, rows, 3)

	var mm tea.Model = m
	// Open selector — cursor at 0 (NativeID col).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	// Press enter → SortAsc col 0.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	result := mm.(Model)
	assert.Equal(t, 0, result.tabs[TabResources].sortCol)
	assert.Equal(t, components.SortAsc, result.tabs[TabResources].sortDir)

	// Get visible rows.
	vis, total := result.tabs[TabResources].visible(3)
	assert.Equal(t, 10, total, "total must be 10 (all rows)")
	require.Len(t, vis, 3, "visible must be capped at 3")

	// Verify the GLOBAL top-3 in asc order by exact NativeID values. If the cap
	// were (incorrectly) applied before the sort, the window would hold the
	// first 3 server-order rows re-sorted locally — a different set entirely.
	assert.Equal(t, "arn:aws:iam:::role/app", vis[0].cells[0],
		"first visible row must be the global asc minimum")
	assert.Equal(t, "arn:aws:rds:::db/primary", vis[1].cells[0])
	assert.Equal(t, "arn:aws:s3:::my-bucket", vis[2].cells[0])

	// Now toggle to desc: open again, enter on same col.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	result = mm.(Model)
	assert.Equal(t, components.SortDesc, result.tabs[TabResources].sortDir)

	visDesc, _ := result.tabs[TabResources].visible(3)
	require.Len(t, visDesc, 3)
	// Global top-3 in desc order by exact NativeID values.
	assert.Equal(t, "subnet-0abc123456789b", visDesc[0].cells[0],
		"first visible row must be the global desc maximum")
	assert.Equal(t, "subnet-0abc123456789a", visDesc[1].cells[0])
	assert.Equal(t, "sg-0123456789ab", visDesc[2].cells[0])
}

// ---------------------------------------------------------------------------
// Test 6: While open, pressing "2" does NOT switch tabs
// ---------------------------------------------------------------------------

func TestSortSelector_KeysBlockedWhenOpen(t *testing.T) {
	rows := buildFixtureResources(3)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	// Open selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	// Press '2' — must NOT switch tabs.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})

	result := mm.(Model)
	assert.Equal(t, TabResources, result.active, "active tab must remain Resources while sort selector is open")
	assert.True(t, result.sortOpen, "sortOpen must still be true (key was swallowed)")
}

// ---------------------------------------------------------------------------
// Test 7: Exact-fill while selector open
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Test: ? while sort selector open does NOT open help overlay
// ---------------------------------------------------------------------------

// TestSortSelector_QuestionMarkBlockedWhenOpen: pressing ? while the sort
// selector is open must be swallowed — helpOpen stays false and sortOpen
// stays true.
func TestSortSelector_QuestionMarkBlockedWhenOpen(t *testing.T) {
	rows := buildFixtureResources(3)
	m := buildSortTestModel(t, rows, 0)

	var mm tea.Model = m
	// Open selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	require.True(t, mm.(Model).sortOpen, "sortOpen must be true after pressing 's'")

	// Press ? — must NOT open help overlay.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})

	result := mm.(Model)
	assert.False(t, result.helpOpen, "? must not open help while sort selector is open")
	assert.True(t, result.sortOpen, "sortOpen must remain true after pressing ?")
}

func TestSortSelector_ExactFillWhileOpen(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m

	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	rows := buildFixtureResources(5)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	// Open selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	view := mm.(Model).View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, 24, len(lines), "View must fill exactly 24 lines when sort selector is open")
}
