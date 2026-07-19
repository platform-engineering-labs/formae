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

// typeIntoQuery sends individual rune key messages to simulate typing into the
// query bar.
func typeIntoQuery(mm tea.Model, s string) tea.Model {
	for _, r := range s {
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return mm
}

// applyQuery focuses the query bar, types s, and confirms with enter.
func applyQuery(mm tea.Model, s string) tea.Model {
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoQuery(mm, s)
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return mm
}

// ---------------------------------------------------------------------------
// Contract: "/" focuses the query bar
// ---------------------------------------------------------------------------

func TestFilter_SlashFocusesQueryBar(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	assert.True(t, mm.(Model).query.Focused(), "pressing '/' must focus the query bar")
}

// ---------------------------------------------------------------------------
// Contract: the query applies on enter (not live), then narrows
// ---------------------------------------------------------------------------

func TestFilter_AppliesOnEnter(t *testing.T) {
	_, mm := loadFilterFixture(t)

	// Focus and type "S3" — must NOT narrow until enter is pressed.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoQuery(mm, "S3")
	before, _ := mm.(Model).tabs[TabResources].visible(0)
	assert.Len(t, before, 7, "typing must not filter live — all 7 rows still shown before enter")

	// Enter applies the query.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := mm.(Model)
	assert.Equal(t, "S3", m.tabs[TabResources].filter, "enter must apply the typed query")
	vis, _ := m.tabs[TabResources].visible(0)
	assert.Len(t, vis, 3, "applying 'S3' must narrow from 7 to 3 rows")
}

// ---------------------------------------------------------------------------
// Contract: a longer query narrows further
// ---------------------------------------------------------------------------

func TestFilter_LongerQueryNarrowsFurther(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm = applyQuery(mm, "buck")
	vis1, _ := mm.(Model).tabs[TabResources].visible(0)

	mm = applyQuery(mm, "bucket-1")
	vis2, _ := mm.(Model).tabs[TabResources].visible(0)

	assert.Greater(t, len(vis1), len(vis2), "a longer query must narrow the result set further")
}

// ---------------------------------------------------------------------------
// Contract: enter keeps the query applied and unfocuses
// ---------------------------------------------------------------------------

func TestFilter_EnterKeepsQueryAndUnfocuses(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm = applyQuery(mm, "S3")

	m := mm.(Model)
	assert.False(t, m.query.Focused(), "enter must unfocus the query bar")
	assert.Equal(t, "S3", m.tabs[TabResources].filter, "query must be preserved after enter")

	// After unfocus, 'q' should quit (not go into the query).
	_, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd, "after unfocus, 'q' must produce a quit command")
	assert.Equal(t, tea.Quit(), cmd())
}

// ---------------------------------------------------------------------------
// Contract: esc cancels the edit and keeps the previously-applied query
// ---------------------------------------------------------------------------

func TestFilter_EscCancelsEditKeepsApplied(t *testing.T) {
	_, mm := loadFilterFixture(t)

	// Apply "S3" first.
	mm = applyQuery(mm, "S3")

	// Re-focus, type more, then esc — the edit is discarded, "S3" stays applied.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoQuery(mm, "-extra")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})

	m := mm.(Model)
	assert.False(t, m.query.Focused(), "esc must unfocus the query bar")
	assert.Equal(t, "S3", m.tabs[TabResources].filter, "esc must keep the previously-applied query")
	vis, _ := m.tabs[TabResources].visible(0)
	assert.Len(t, vis, 3, "the applied 'S3' query must still narrow to 3 rows")
}

// ---------------------------------------------------------------------------
// Contract: clearing the query (empty + enter) restores the full row set
// ---------------------------------------------------------------------------

func TestFilter_EmptyQueryRestoresAll(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm = applyQuery(mm, "S3")
	// Re-focus, clear the edit (ctrl+u), apply empty.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	m := mm.(Model)
	assert.Equal(t, "", m.tabs[TabResources].filter, "an empty query must clear the filter")
	vis, _ := m.tabs[TabResources].visible(0)
	assert.Len(t, vis, 7, "clearing the query must restore the full row set (7 rows)")
}

// ---------------------------------------------------------------------------
// Contract: while focused, typed q/2/s/r go INTO the query (not global keys)
// ---------------------------------------------------------------------------

func TestFilter_FocusedTypingGoesIntoQuery(t *testing.T) {
	_, mm := loadFilterFixture(t)

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	// Type 'q' — must NOT quit and must NOT switch tabs.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		assert.NotEqual(t, tea.Quit(), cmd(), "'q' while query focused must NOT quit")
	}
	assert.Equal(t, TabResources, mm.(Model).active, "'q' while focused must NOT switch tabs")

	// Type '2' — must NOT switch to Targets tab.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	assert.Equal(t, TabResources, mm.(Model).active, "'2' while focused must NOT switch tabs")

	// Type 's' and 'r' — must go into the query, not sort/refresh. Apply and check.
	mm = typeIntoQuery(mm, "sr")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "q2sr", mm.(Model).tabs[TabResources].filter,
		"keys typed while focused must accumulate in the query")
}

// ---------------------------------------------------------------------------
// Contract: query state is per-tab (D5) — reseeded into the bar on tab switch
// ---------------------------------------------------------------------------

func TestFilter_PerTabIsolation(t *testing.T) {
	_, mm := loadFilterFixture(t)

	// Apply "S3" on Resources.
	mm = applyQuery(mm, "S3")

	// Switch to Targets tab.
	var cmd tea.Cmd
	mm, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		mm, _ = mm.Update(cmd())
	}
	assert.Equal(t, "", mm.(Model).tabs[TabTargets].filter, "Targets tab must have its own (empty) filter")
	assert.Equal(t, "", mm.(Model).query.Query(), "query bar must reflect the (empty) Targets filter")

	// Switch back to Resources — filter still "S3", and the bar reflects it.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	assert.Equal(t, "S3", mm.(Model).tabs[TabResources].filter,
		"Resources filter must be preserved after switching away and back")
	assert.Equal(t, "S3", mm.(Model).query.Query(), "query bar must be reseeded with the Resources filter")
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

	// All 7 rows match "aws" (in NativeID or Type); cap is 3 → "3 of 7 (filtered)".
	mm = applyQuery(mm, "aws")

	sl := mm.(Model).tabs[TabResources].statusLine(opts.MaxRows)
	assert.Contains(t, sl, "3 of 7", "status must show shown=cap=3, total=7")
	assert.Contains(t, sl, "(filtered)", "status must show (filtered) suffix")
}

// ---------------------------------------------------------------------------
// Contract: exact-fill in each query-bar state
// ---------------------------------------------------------------------------

func TestFilter_ExactFillAllBarStates(t *testing.T) {
	const height = 24

	cases := []struct {
		name    string
		setup   func(mm tea.Model) tea.Model
		wantLen int
	}{
		{name: "empty", setup: func(mm tea.Model) tea.Model { return mm }},
		{name: "editing", setup: func(mm tea.Model) tea.Model {
			mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			return typeIntoQuery(mm, "S3")
		}},
		{name: "applied", setup: func(mm tea.Model) tea.Model { return applyQuery(mm, "S3") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mm := loadFilterFixture(t)
			mm = tc.setup(mm)
			lines := strings.Split(mm.(Model).View(), "\n")
			assert.Equal(t, height, len(lines), "View must fill exactly %d lines in state %s", height, tc.name)
		})
	}
}

// ---------------------------------------------------------------------------
// Golden: query bar focused while editing
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

	// Focus the query bar and type "S3" (not yet applied).
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = typeIntoQuery(mm, "S3")

	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// ---------------------------------------------------------------------------
// Golden: applied (unfocused) query
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

	mm = applyQuery(mm, "S3")

	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}
