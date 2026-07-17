// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// buildFixtureClientFull returns a fakeClient seeded with resources (incl. ⚠
// unmanaged row) and targets — enough for golden tests.
func buildFixtureClientFull() *fakeClient {
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"},
			{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"},
			{NativeID: "arn:aws:s3:::old-logs", Stack: "unmanaged", Type: "AWS::S3::Bucket", Label: "old-logs"},
		},
	}
	targets := []*pkgmodel.Target{
		{Label: "aws-prod", Namespace: "AWS", Discoverable: true, Config: json.RawMessage(`{"Account":"123456"}`)},
		{Label: "dev", Namespace: "Azure", Discoverable: false, Config: json.RawMessage(`{}`)},
	}
	return &fakeClient{
		forma:   forma,
		targets: targets,
	}
}

func newTestInventoryModel(t *testing.T, fc *fakeClient, opts Options) Model {
	t.Helper()
	th := theme.New("formae")
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }
	}
	return New(th, fc, opts)
}

// runInit simulates Init() by calling the fetch cmd directly (skipping spinner
// tick which is not relevant for unit tests). Returns the model after the
// tabLoadedMsg has been delivered.
func runInit(mm tea.Model) (tea.Model, tea.Cmd) {
	m := mm.(Model)
	// Build the fetch cmd for the focus tab directly to avoid batch handling.
	query := m.opts.Query
	cmd := fetchCmd(m.client, m.specs, m.active, query, false)
	msg := cmd()
	var resultCmd tea.Cmd
	mm, resultCmd = mm.Update(msg)
	return mm, resultCmd
}

// ---------------------------------------------------------------------------
// TestModel_ViewLineCountMatchesHeight
// ---------------------------------------------------------------------------

// TestModel_ViewLineCountMatchesHeight verifies that View() returns exactly
// m.height lines in every state: loading, loaded, failed.
func TestModel_ViewLineCountMatchesHeight(t *testing.T) {
	height := 24
	width := 100

	cases := []struct {
		name    string
		prepare func(m Model) Model
	}{
		{
			name: "loading",
			prepare: func(m Model) Model {
				m.tabs[TabResources].state = tabLoading
				return m
			},
		},
		{
			name: "loaded",
			prepare: func(m Model) Model {
				m.tabs[TabResources].state = tabLoaded
				m.tabs[TabResources].allRows = buildFixtureResources(3)
				m.tabs[TabResources] = m.tabs[TabResources].sync(m.opts.MaxRows)
				return m
			},
		},
		{
			name: "failed",
			prepare: func(m Model) Model {
				m.tabs[TabResources].state = tabFailed
				return m
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := buildFixtureClientFull()
			opts := Options{
				FocusTab: TabResources,
				Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
			}
			th := theme.New("formae")
			m := New(th, fc, opts)
			var mm tea.Model = m
			mm, _ = mm.Update(tea.WindowSizeMsg{Width: width, Height: height})
			// Prepare the state directly after window sizing.
			mm = tc.prepare(mm.(Model))

			view := mm.(Model).View()
			lines := strings.Split(view, "\n")
			assert.Equal(t, height, len(lines),
				"View must fill exactly %d lines in %s state", height, tc.name)
		})
	}
}

// ---------------------------------------------------------------------------
// TestModel_QueryBindsToFocusTabOnly
// ---------------------------------------------------------------------------

func TestModel_QueryBindsToFocusTabOnly(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabTargets,
		Query:    "x",
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m

	// Apply window size first.
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Execute Init() commands to trigger the first fetch (targets with query "x").
	mm, _ = runInit(mm)

	// Targets fetch should have used query "x".
	assert.Equal(t, "x", fc.targetsQuery, "focus tab (Targets) should receive the query")

	// Now press "1" to switch to Resources tab and trigger its fetch.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd != nil {
		msg := cmd()
		mm, _ = mm.Update(msg)
	}
	// Resources fetch should use empty query.
	assert.Equal(t, "", fc.resourcesQuery, "non-focus tab (Resources) should receive empty query")
	_ = mm
}

// ---------------------------------------------------------------------------
// TestModel_FirstFetchTransmitsStatsOnce
// ---------------------------------------------------------------------------

func TestModel_FirstFetchTransmitsStatsOnce(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m

	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Execute the init cmd — should fetch resources with fromTUI=false.
	mm, _ = runInit(mm)
	assert.False(t, fc.resourcesFromTUI, "first fetch must pass fromTUI=false")

	// Press "2" to switch to Targets — its first fetch should use fromTUI=true.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		msg := cmd()
		mm, _ = mm.Update(msg)
	}
	assert.True(t, fc.targetsFromTUI, "subsequent fetches must pass fromTUI=true")

	// Press "r" to refetch active tab — should also use fromTUI=true.
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		msg := cmd()
		mm, _ = mm.Update(msg)
	}
	assert.True(t, fc.targetsFromTUI, "refresh must pass fromTUI=true")
	_ = mm
}

// TestModel_EarlyTabSwitchStillFromTUI pins the race window: a tab switch
// BEFORE the init fetch response arrives must still fetch with fromTUI=true —
// Init's fetch is always the session's first, so stats are never sent twice.
func TestModel_EarlyTabSwitchStillFromTUI(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Do NOT deliver the init response — switch tabs immediately.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	require.NotNil(t, cmd)
	msg := cmd()
	mm, _ = mm.Update(msg)
	assert.True(t, fc.targetsFromTUI,
		"tab switch before init response must still fetch with fromTUI=true")
	_ = mm
}

// ---------------------------------------------------------------------------
// TestModel_RefreshPreservesSortAndFilter
// ---------------------------------------------------------------------------

func TestModel_RefreshPreservesSortAndFilter(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m

	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Load the first tab via init.
	mm, _ = runInit(mm)

	// Set filter and sort directly on the active tab (package-internal).
	model := mm.(Model)
	model.tabs[TabResources].filter = "production"
	model.tabs[TabResources].sortCol = 1
	model.tabs[TabResources].sortDir = components.SortDesc
	mm = model

	// Press "r" to refresh.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	require.Equal(t, tabLoading, mm.(Model).tabs[TabResources].state,
		"after 'r', active tab state must be tabLoading")
	// Filter and sort must still be present during loading.
	assert.Equal(t, "production", mm.(Model).tabs[TabResources].filter)
	assert.Equal(t, 1, mm.(Model).tabs[TabResources].sortCol)
	assert.Equal(t, components.SortDesc, mm.(Model).tabs[TabResources].sortDir)

	// Deliver the loaded message.
	if cmd != nil {
		msg := cmd()
		mm, _ = mm.Update(msg)
	}

	// Filter and sort must be preserved after reload.
	loaded := mm.(Model)
	assert.Equal(t, tabLoaded, loaded.tabs[TabResources].state)
	assert.Equal(t, "production", loaded.tabs[TabResources].filter,
		"filter must be preserved after refresh")
	assert.Equal(t, 1, loaded.tabs[TabResources].sortCol,
		"sortCol must be preserved after refresh")
	assert.Equal(t, components.SortDesc, loaded.tabs[TabResources].sortDir,
		"sortDir must be preserved after refresh")
}

// ---------------------------------------------------------------------------
// TestModel_LoadedMsgForInactiveTab
// ---------------------------------------------------------------------------

func TestModel_LoadedMsgForInactiveTab(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Deliver a tabLoadedMsg for Targets while Resources is active.
	rows := []row{targetRow(&pkgmodel.Target{Label: "aws", Namespace: "AWS", Discoverable: true})}
	mm, _ = mm.Update(tabLoadedMsg{tab: TabTargets, rows: rows})

	model := mm.(Model)
	assert.Equal(t, TabResources, model.active, "active tab must not change")
	assert.Equal(t, tabLoaded, model.tabs[TabTargets].state,
		"Targets tab must transition to tabLoaded even when not active")
	assert.Len(t, model.tabs[TabTargets].allRows, 1,
		"Targets tab must have the loaded rows")
}

// ---------------------------------------------------------------------------
// TestModel_NagsDedupe
// ---------------------------------------------------------------------------

func TestModel_NagsDedupe(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Deliver loaded messages with duplicate nags.
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, nags: []string{"nag-1", "nag-2", "nag-1"}})
	mm, _ = mm.Update(tabLoadedMsg{tab: TabTargets, nags: []string{"nag-2", "nag-3"}})

	nags := mm.(Model).Nags()
	// Should be deduped, insertion-ordered.
	assert.Equal(t, []string{"nag-1", "nag-2", "nag-3"}, nags)
}

// ---------------------------------------------------------------------------
// TestModel_TabCycleWrap
// ---------------------------------------------------------------------------

func TestModel_TabCycleWrap(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Jump to tab 4 (Policies, index 3).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	assert.Equal(t, TabPolicies, mm.(Model).active, "pressing 4 must activate Policies tab")

	// Press tab to cycle forward → should wrap to Resources (index 0).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.Equal(t, TabResources, mm.(Model).active, "tab from last tab must wrap to Resources")

	// Press shift+tab from Resources → should wrap to Policies.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	assert.Equal(t, TabPolicies, mm.(Model).active, "shift+tab from first tab must wrap to Policies")
}

// ---------------------------------------------------------------------------
// TestModel_CtrlCQuitsInventory
// ---------------------------------------------------------------------------

func TestModel_CtrlCQuitsInventory(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	assert.Equal(t, tea.Quit(), cmd())
}

// ---------------------------------------------------------------------------
// Golden tests — drive Update directly then capture View()
// ---------------------------------------------------------------------------

// TestGolden_InventoryResourcesLoaded: initial view with Resources loaded.
func TestGolden_InventoryResourcesLoaded(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Deliver the resource rows directly.
	rows := resourceRowsFromForma(fc.forma)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// TestGolden_InventoryTargetsTab: after pressing "2", Targets tab active and loaded.
func TestGolden_InventoryTargetsTab(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Load resources first.
	rows := resourceRowsFromForma(fc.forma)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	// Switch to targets tab — triggers fetch.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	// Deliver the targets loaded msg.
	if cmd != nil {
		msg := cmd()
		mm, _ = mm.Update(msg)
	}
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// TestGolden_InventoryTabCycleWrap: from Policies (press "4" then "tab") → Resources active.
func TestGolden_InventoryTabCycleWrap(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Load resources.
	rows := resourceRowsFromForma(fc.forma)
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	// Jump to Policies tab.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	// Tab-cycle back to Resources.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyTab})
	// Resources should be active and still loaded.
	assert.Equal(t, TabResources, mm.(Model).active)
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// ---------------------------------------------------------------------------
// Helper: build resource rows from a pkgmodel.Forma
// ---------------------------------------------------------------------------

func resourceRowsFromForma(f *pkgmodel.Forma) []row {
	if f == nil {
		return nil
	}
	rows := make([]row, 0, len(f.Resources))
	for _, r := range f.Resources {
		rows = append(rows, resourceRow(r))
	}
	return rows
}
