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

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// Fixture: 7 resources — 3 are S3 buckets (match "S3"), 4 are not.
// ---------------------------------------------------------------------------

func buildFilterFixtureClient() *fakeClient {
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{NativeID: "arn:aws:s3:::bucket-1", Stack: "production", Type: "AWS::S3::Bucket", Label: "bucket-1"},
			{NativeID: "arn:aws:s3:::bucket-2", Stack: "staging", Type: "AWS::S3::Bucket", Label: "bucket-2"},
			{NativeID: "arn:aws:s3:::bucket-3", Stack: "network", Type: "AWS::S3::Bucket", Label: "bucket-3"},
			{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"},
			{NativeID: "sg-0123456789ab", Stack: "production", Type: "AWS::EC2::SecurityGroup", Label: "web-sg"},
			{NativeID: "subnet-0abc123456789a", Stack: "network", Type: "AWS::EC2::Subnet", Label: "subnet-1"},
			{NativeID: "rtb-0abc123456789c", Stack: "network", Type: "AWS::EC2::RouteTable", Label: "rt-main"},
		},
	}
	return &fakeClient{forma: forma}
}

// loadFilterFixture builds a Model with all 7 resources loaded.
func loadFilterFixture(t *testing.T) (Model, tea.Model) {
	t.Helper()
	fc := buildFilterFixtureClient()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = runInit(mm)
	return mm.(Model), mm
}

// typeIntoFilter sends individual rune key messages to simulate typing.
func typeIntoFilter(mm tea.Model, s string) tea.Model {
	for _, r := range s {
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return mm
}

// ---------------------------------------------------------------------------
// Contract: "/" focuses filter bar on loaded tab
// ---------------------------------------------------------------------------

func TestFilter_SlashFocusesBar(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	m := mm.(Model)
	assert.True(t, m.filterFocused, "pressing '/' must focus the filter bar")
}

// ---------------------------------------------------------------------------
// Contract: while focused, typing updates tabs[active].filter live
// ---------------------------------------------------------------------------

func TestFilter_TypingNarrowsLive(t *testing.T) {
	_, mm := loadFilterFixture(t)

	// Focus the bar.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	// Type "S3" — should narrow to S3 resources.
	mm = typeIntoFilter(mm, "S3")

	m := mm.(Model)
	assert.Equal(t, "S3", m.tabs[TabResources].filter,
		"filter must be updated live as user types")

	// Visible rows should be 3 (only S3 buckets).
	vis, _ := m.tabs[TabResources].visible(0)
	assert.Len(t, vis, 3, "typing 'S3' must narrow from 7 to 3 rows")
}

// ---------------------------------------------------------------------------
// Contract: single char narrows, more chars narrow further
// ---------------------------------------------------------------------------

func TestFilter_PartialVsFullTermNarrows(t *testing.T) {
	_, mm := loadFilterFixture(t)
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	// Type "S" — matches S3 and SecurityGroup (AWS::EC2::Security**G**roup has no S match...
	// actually "subnet" also has S. Let's check: "S" matches: bucket (s3), web-sg (sg has S),
	// subnet-1 (subnet has S), rt-main (no)... and "production" (no P... wait "s" in "staging").
	// Use a more targeted partial filter: "bucket" (partial = "buck").
	mm = typeIntoFilter(mm, "buck")
	m := mm.(Model)
	vis1, _ := m.tabs[TabResources].visible(0)
	count1 := len(vis1)

	// Type more: "bucket-1" (exact label) — narrows further.
	mm = typeIntoFilter(mm, "et-1")
	m = mm.(Model)
	vis2, _ := m.tabs[TabResources].visible(0)
	count2 := len(vis2)

	assert.Greater(t, count1, count2, "more typed chars must narrow the result set further")
}

// ---------------------------------------------------------------------------
// Contract: enter keeps filter applied and unfocuses
// ---------------------------------------------------------------------------

func TestFilter_EnterKeepsFilterAndUnfocuses(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoFilter(mm, "S3")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	m := mm.(Model)
	assert.False(t, m.filterFocused, "enter must unfocus the filter bar")
	assert.Equal(t, "S3", m.tabs[TabResources].filter, "filter must be preserved after enter")

	// After unfocus, 'q' should quit (not go into the filter).
	_, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd, "after unfocus, 'q' must produce a quit command")
	assert.Equal(t, tea.Quit(), cmd())
}

// ---------------------------------------------------------------------------
// Contract: esc clears filter and unfocuses
// ---------------------------------------------------------------------------

func TestFilter_EscClearsAndUnfocuses(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoFilter(mm, "S3")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})

	m := mm.(Model)
	assert.False(t, m.filterFocused, "esc must unfocus the filter bar")
	assert.Equal(t, "", m.tabs[TabResources].filter, "esc must clear the filter")

	// Full row set restored.
	vis, _ := m.tabs[TabResources].visible(0)
	assert.Len(t, vis, 7, "esc must restore the full row set (7 rows)")
}

// ---------------------------------------------------------------------------
// Contract: while focused, typed 'q', '2', 's', 'r' go INTO input (not handled as global keys)
// ---------------------------------------------------------------------------

func TestFilter_FocusedTypingGoesIntoInput(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	// Type 'q' — must NOT quit.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		// If cmd is quit, that's a bug.
		result := cmd()
		assert.NotEqual(t, tea.Quit(), result, "'q' while filter focused must NOT quit")
	}
	assert.Equal(t, TabResources, mm.(Model).active, "'q' while focused must NOT switch tabs")

	// Type '2' — must NOT switch to Targets tab.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	assert.Equal(t, TabResources, mm.(Model).active, "'2' while focused must NOT switch tabs")

	// Type 's' — must NOT open sort selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.False(t, mm.(Model).sortOpen, "'s' while focused must NOT open sort selector")

	// Type 'r' — must NOT trigger a refresh.
	initialFilter := mm.(Model).tabs[TabResources].filter
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	assert.Equal(t, initialFilter+"r", mm.(Model).tabs[TabResources].filter,
		"'r' while focused must go into the filter, not trigger a refresh")
}

// ---------------------------------------------------------------------------
// Contract: filter state is per-tab (D5)
// ---------------------------------------------------------------------------

func TestFilter_PerTabIsolation(t *testing.T) {
	_, mm := loadFilterFixture(t)

	// Focus filter bar on Resources and type "S3".
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoFilter(mm, "S3")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // unfocus but keep filter

	// Switch to Targets tab.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		msg := cmd()
		mm, _ = mm.Update(msg)
	}

	// Targets tab should have empty filter.
	assert.Equal(t, "", mm.(Model).tabs[TabTargets].filter,
		"Targets tab must have its own (empty) filter")

	// Switch back to Resources — filter should still be "S3".
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	assert.Equal(t, "S3", mm.(Model).tabs[TabResources].filter,
		"Resources filter must be preserved after switching away and back")
}

// ---------------------------------------------------------------------------
// Contract: filter + cap interaction (D8 pin)
// ---------------------------------------------------------------------------

func TestFilter_FilterPlusCap(t *testing.T) {
	fc := buildFilterFixtureClient()
	opts := Options{
		FocusTab: TabResources,
		MaxRows:  3,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = runInit(mm)

	// Focus and type a filter that matches 5 rows (e.g., "aws" matches all S3 arns and EC2 arns).
	// "arn" matches: bucket-1, bucket-2, bucket-3, web-1 (i-0..., no), web-sg (sg-..., no),
	// subnet-1 (subnet-..., no), rt-main (rtb-..., no).
	// Actually "arn:aws" matches exactly: bucket-1, bucket-2, bucket-3 = 3 rows.
	// Let's use "production" which matches: bucket-1, web-1, web-sg = 3 rows in stack col.
	// Use "aws" which is in NativeID for all S3 (arn:aws:s3) and EC2 (i-..no, sg-..no,
	// subnet-..no, rtb-..no). Actually let's count: bucket-1 (arn:aws:s3→yes), bucket-2 (yes),
	// bucket-3 (yes), web-1 (i-0abc, type AWS::EC2::Instance→yes "aws" in type!),
	// web-sg (AWS::EC2→yes), subnet-1 (AWS::EC2→yes), rt-main (AWS::EC2→yes).
	// That's 7 rows all matching "aws". Let's use "S3" to get 3 rows, cap=3 → status shows "3 of 3".
	// We need filter matches 5, cap 3 → use "bucket" which matches 3, or use "EC2" which matches 4.
	// "EC2": web-1 (AWS::EC2::Instance), web-sg (AWS::EC2::SecurityGroup), subnet-1 (AWS::EC2::Subnet),
	// rt-main (AWS::EC2::RouteTable) = 4 rows. Still not 5.
	// Use "network" (stack): bucket-3 (network), subnet-1 (network), rt-main (network) = 3.
	// Let's use "arn" which matches: bucket-1, bucket-2, bucket-3 = 3 rows.
	// For "filter matches 5, cap 3" test, use "aws" which matches all 7, cap 3 → "3 of 7".
	// OR use a filter matching exactly 5. "a" matches all (all have 'a' somewhere). Let's just
	// use "" filter with 7 rows, cap 3 → status "3 of 7 resources — refine query to see more".
	// For this test we want FILTERED status, so we need a non-empty filter that matches >cap.
	// Use "aws" (case-insensitive): all 7 rows have "aws" in type or NativeID. Cap 3 → "3 of 7 (filtered)".

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoFilter(mm, "aws")

	m = mm.(Model)
	sl := m.tabs[TabResources].statusLine(opts.MaxRows)

	// Should show "Showing 3 of 7 resources (filtered)" since all 7 match "aws" but cap is 3.
	assert.Contains(t, sl, "3 of 7", "status must show shown=cap=3, total=7")
	assert.Contains(t, sl, "(filtered)", "status must show (filtered) suffix")
}

// ---------------------------------------------------------------------------
// Contract: exact-fill in all 4 bar states
// ---------------------------------------------------------------------------

func TestFilter_ExactFillAllBarStates(t *testing.T) {
	const height = 24

	cases := []struct {
		name          string
		filterFocused bool
		filter        string
	}{
		{name: "unfocused-empty", filterFocused: false, filter: ""},
		{name: "unfocused-applied", filterFocused: false, filter: "S3"},
		{name: "focused-empty", filterFocused: true, filter: ""},
		{name: "focused-applied", filterFocused: true, filter: "S3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mm := loadFilterFixture(t)

			if tc.filter != "" || tc.filterFocused {
				mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
				if tc.filter != "" {
					mm = typeIntoFilter(mm, tc.filter)
				}
				if !tc.filterFocused {
					mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
				}
			}

			view := mm.(Model).View()
			lines := strings.Split(view, "\n")
			assert.Equal(t, height, len(lines),
				"View must fill exactly %d lines in state %s", height, tc.name)
		})
	}
}

// ---------------------------------------------------------------------------
// Contract: "/" while sort selector open is swallowed by the selector
// ---------------------------------------------------------------------------

func TestFilter_SlashSwallowedWhenSortOpen(t *testing.T) {
	_, mm := loadFilterFixture(t)

	// Open sort selector.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	require.True(t, mm.(Model).sortOpen, "sort selector must be open")

	// Press '/' — must be swallowed by sort selector, not open filter bar.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	assert.False(t, mm.(Model).filterFocused,
		"'/' while sort selector open must not focus filter bar")
}

// ---------------------------------------------------------------------------
// Golden: filter focused with value narrowing rows
// ---------------------------------------------------------------------------

func TestGolden_FilterFocused(t *testing.T) {
	fc := buildFilterFixtureClient()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = runInit(mm)

	// Focus filter and type "S3".
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoFilter(mm, "S3")

	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// ---------------------------------------------------------------------------
// Golden: unfocused-but-applied filter
// ---------------------------------------------------------------------------

func TestGolden_FilterUnfocusedApplied(t *testing.T) {
	fc := buildFilterFixtureClient()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = runInit(mm)

	// Focus, type "S3", then unfocus with enter.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoFilter(mm, "S3")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}
