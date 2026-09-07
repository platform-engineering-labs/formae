// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSortAppliesCellStylesSameFrame pins the fix for the "column coloring is
// wrong until you hit up/down" bug: when the sort column changes, sync() must
// order the bubbles table rows by the NEW column in the SAME frame, so the
// per-cell styles (e.g. the "⚠ unmanaged" warning colour) — which are matched to
// rendered rows by position in applyCellStyles — line up immediately.
//
// Previously sync() ran SetRows (which sorts by the table's current/old column)
// before SetSortState (which updated the column), so the first frame after a
// sort rendered styled cells against the wrong rows and only corrected on the
// next event. We assert the frame right after the sort is identical to a frame
// produced by an extra no-op re-sync (which was the "correct" state).
func TestSortAppliesCellStylesSameFrame(t *testing.T) {
	fc := buildFixtureClientFull()
	m := newTestInventoryModel(t, fc, Options{FocusTab: TabResources})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	mm, _ = runInit(mm)

	// Sort by a different column than the default (Right → Stack, then s).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	sorted := mm.(Model)
	frameAfterSort := sorted.View()

	// A redundant re-sync must not change anything: the frame right after the
	// sort is already fully styled. (Before the fix, this re-sync is what a
	// stray up/down effectively did to "correct" the colouring.)
	resynced := sorted
	resynced.tabs[resynced.active] = resynced.tabs[resynced.active].sync(resynced.opts.MaxRows)
	frameAfterResync := resynced.View()

	if frameAfterSort != frameAfterResync {
		t.Errorf("stale render: the frame immediately after sort differs from a re-synced frame — per-cell styles are one frame behind the sort")
	}
}
