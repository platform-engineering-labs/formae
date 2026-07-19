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
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// openDetailOnFirstRow sends an enter key to open the detail on the first visible row.
func openDetailOnFirstRow(t *testing.T, mm tea.Model) tea.Model {
	t.Helper()
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return mm
}

// buildDetailTestModel builds a loaded model ready for detail tests.
// It uses buildFixtureClientFull() and injects the given rows for the Resources tab.
func buildDetailTestModel(t *testing.T, rows []row) tea.Model {
	t.Helper()
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(tabLoadedMsg{tab: TabResources, rows: rows})
	return mm
}

// ---------------------------------------------------------------------------
// Behavior tests (Update-direct, no goldens)
// ---------------------------------------------------------------------------

// TestDetail_EnterOnLoadedTabOpensDetail verifies enter opens detail when tab loaded.
func TestDetail_EnterOnLoadedTabOpensDetail(t *testing.T) {
	rows := buildFixtureResources(3)
	mm := buildDetailTestModel(t, rows)

	mm = openDetailOnFirstRow(t, mm)

	m := mm.(Model)
	assert.True(t, m.detailOpen, "detail screen must be open after enter")
	assert.NotEmpty(t, m.detailTitle, "detailTitle must be set")
}

// TestDetail_EnterNoOpOnEmptyTab verifies enter is a no-op on an empty tab.
func TestDetail_EnterNoOpOnEmptyTab(t *testing.T) {
	mm := buildDetailTestModel(t, []row{})

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	m := mm.(Model)
	assert.False(t, m.detailOpen, "detail screen must not open when tab has no rows")
}

// TestDetail_EnterNoOpOnLoadingTab verifies enter is a no-op when tab is loading.
func TestDetail_EnterNoOpOnLoadingTab(t *testing.T) {
	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabResources,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Don't deliver a tabLoadedMsg — tab stays in tabLoading/tabNotLoaded state.
	// Switch to loading state manually.
	model := mm.(Model)
	model.tabs[TabResources].state = tabLoading
	mm = model

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.False(t, mm.(Model).detailOpen, "detail screen must not open when tab is loading")
}

// TestDetail_EnterNoOpWhenQueryFocused verifies enter with the query bar focused
// applies the query rather than opening the detail screen.
func TestDetail_EnterNoOpWhenQueryFocused(t *testing.T) {
	rows := buildFixtureResources(3)
	mm := buildDetailTestModel(t, rows)

	// Focus the query bar.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	require.True(t, mm.(Model).query.Focused(), "query must be focused")

	// Enter while the query is focused confirms the query, not opens detail.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	m := mm.(Model)
	assert.False(t, m.detailOpen, "detail screen must not open when query is focused (enter applies query)")
	assert.False(t, m.query.Focused(), "query focus must be cleared after enter")
}

// TestDetail_EscClosesDetail verifies esc from detail closes and returns to list.
func TestDetail_EscClosesDetail(t *testing.T) {
	rows := buildFixtureResources(3)
	mm := buildDetailTestModel(t, rows)

	mm = openDetailOnFirstRow(t, mm)
	require.True(t, mm.(Model).detailOpen, "detail must be open before esc")

	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})

	assert.False(t, mm.(Model).detailOpen, "esc must close the detail screen")
}

// TestDetail_QSwallowedInDetail verifies q does not quit from the detail screen.
func TestDetail_QQuitsFromDetail(t *testing.T) {
	rows := buildFixtureResources(3)
	mm := buildDetailTestModel(t, rows)

	mm = openDetailOnFirstRow(t, mm)
	require.True(t, mm.(Model).detailOpen)

	_, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	require.NotNil(t, cmd, "q must quit from the detail screen")
	assert.Equal(t, tea.Quit(), cmd(), "'q' in detail must produce a quit command")
}

// TestDetail_CtrlCQuitsFromDetail verifies ctrl+c quits from the detail screen.
func TestDetail_CtrlCQuitsFromDetail(t *testing.T) {
	rows := buildFixtureResources(3)
	mm := buildDetailTestModel(t, rows)

	mm = openDetailOnFirstRow(t, mm)
	require.True(t, mm.(Model).detailOpen)

	_, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	assert.Equal(t, tea.Quit(), cmd())
}

// TestDetail_R1DesyncPin verifies that cursor resolution uses vis[cursor] not SelectedRow().
// Sort col 3 (Label) desc → rows appear Z→A. The detail title must match the first visible row.
// This tests at cursor 0 where sorted order (web-sg) diverges from server order (my-bucket).
func TestDetail_R1DesyncPin(t *testing.T) {
	// Use rows where sorted desc order differs from server order:
	// server order: my-bucket, web-1, web-sg
	// sorted by Label desc: web-sg, web-1, my-bucket
	rows := []row{
		resourceRow(pkgmodel.Resource{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"}),
		resourceRow(pkgmodel.Resource{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"}),
		resourceRow(pkgmodel.Resource{NativeID: "sg-0123456789ab", Stack: "production", Type: "AWS::EC2::SecurityGroup", Label: "web-sg"}),
	}
	mm := buildDetailTestModel(t, rows)

	// Apply sort: Label column (index 3) desc.
	model := mm.(Model)
	model.tabs[TabResources].sortCol = 3 // Label
	model.tabs[TabResources].sortDir = components.SortDesc
	model.tabs[TabResources] = model.tabs[TabResources].sync(model.opts.MaxRows)
	mm = model

	// Verify: sorted desc: web-sg(0), web-1(1), my-bucket(2). Cursor at 0 (no movement after sort).
	vis, _ := mm.(Model).tabs[TabResources].visible(0)
	cursor := mm.(Model).tabs[TabResources].table.Cursor()
	require.Less(t, cursor, len(vis), "cursor must be within visible range")
	expectedLabel := vis[cursor].title

	mm = openDetailOnFirstRow(t, mm)

	assert.Equal(t, expectedLabel, mm.(Model).detailTitle,
		"detail title must match the row under the cursor in sorted order")
	assert.True(t, strings.HasPrefix(mm.(Model).detailTitle, "web-sg"),
		"title must start with 'web-sg' (first row in desc-sorted order; diverges from server order where my-bucket is first)")
}

// TestDetail_FilteredDetail verifies filter narrowing → enter opens correct entity.
func TestDetail_FilteredDetail(t *testing.T) {
	rows := buildFixtureResources(5) // has "my-bucket", "web-1", "web-sg", "primary", "old-logs"
	mm := buildDetailTestModel(t, rows)

	// Resources is a serverQuery tab, so narrowing happens server-side. Simulate
	// a server-filtered result set containing only the "old-logs" row.
	var oldLogs row
	for _, r := range rows {
		if r.cells[3] == "old-logs" {
			oldLogs = r
			break
		}
	}
	model := mm.(Model)
	model.tabs[TabResources].query = "label:old-logs"
	model.tabs[TabResources].allRows = []row{oldLogs}
	model.tabs[TabResources] = model.tabs[TabResources].sync(model.opts.MaxRows)
	mm = model

	// Cursor is at 0 — the only visible row should be "old-logs".
	vis, _ := mm.(Model).tabs[TabResources].visible(0)
	require.Len(t, vis, 1, "filter 'old-logs' must narrow to exactly 1 row")
	require.Equal(t, "old-logs", vis[0].cells[3], "visible row must be old-logs (Label col 3)")

	mm = openDetailOnFirstRow(t, mm)

	m := mm.(Model)
	assert.True(t, m.detailOpen)
	assert.True(t, strings.HasPrefix(m.detailTitle, "old-logs"),
		"detail title must start with 'old-logs'")
}

// TestDetail_EscRestoresListByteIdentical verifies View() before open == View() after esc.
func TestDetail_EscRestoresListByteIdentical(t *testing.T) {
	rows := buildFixtureResources(3)
	mm := buildDetailTestModel(t, rows)

	viewBefore := mm.(Model).View()

	mm = openDetailOnFirstRow(t, mm)
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})

	viewAfter := mm.(Model).View()

	assert.Equal(t, viewBefore, viewAfter,
		"View() must be byte-identical before open and after esc")
}

// TestDetail_ScrollChangesViewport verifies j scrolls when detail content is taller than viewport.
func TestDetail_ScrollChangesViewport(t *testing.T) {
	// Build a row with many detail lines so the content overflows the viewport.
	manyLines := make([]string, 50)
	for i := range manyLines {
		manyLines[i] = "line content here"
	}
	r := row{
		cells:  []string{"arn:x", "production", "AWS::S3::Bucket", "my-big-bucket"},
		title:  "my-big-bucket (AWS::S3::Bucket)",
		detail: func(_ int) []string { return manyLines },
	}

	mm := buildDetailTestModel(t, []row{r})
	mm = openDetailOnFirstRow(t, mm)

	offsetBefore := mm.(Model).detailViewport.YOffset

	// Press j to scroll down.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})

	offsetAfter := mm.(Model).detailViewport.YOffset
	assert.Greater(t, offsetAfter, offsetBefore,
		"j must scroll the viewport down (YOffset must increase)")
}

// TestDetail_ExactFillShortContent verifies exact fill for short detail content.
func TestDetail_ExactFillShortContent(t *testing.T) {
	r := row{
		cells:  []string{"arn:x", "production", "AWS::S3::Bucket", "my-bucket"},
		title:  "my-bucket (AWS::S3::Bucket)",
		detail: func(_ int) []string { return []string{"Label: my-bucket"} },
	}
	mm := buildDetailTestModel(t, []row{r})
	mm = openDetailOnFirstRow(t, mm)

	view := mm.(Model).View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, 24, len(lines),
		"detail View() must fill exactly 24 lines (short content)")
}

// TestDetail_ExactFillLongContent verifies exact fill for long detail content (many lines).
func TestDetail_ExactFillLongContent(t *testing.T) {
	manyLines := make([]string, 100)
	for i := range manyLines {
		manyLines[i] = "detail line content"
	}
	r := row{
		cells:  []string{"arn:x", "production", "AWS::S3::Bucket", "my-bucket"},
		title:  "my-bucket (AWS::S3::Bucket)",
		detail: func(_ int) []string { return manyLines },
	}
	mm := buildDetailTestModel(t, []row{r})
	mm = openDetailOnFirstRow(t, mm)

	view := mm.(Model).View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, 24, len(lines),
		"detail View() must fill exactly 24 lines (long content)")
}

// ---------------------------------------------------------------------------
// Golden tests (100x24)
// ---------------------------------------------------------------------------

// TestGolden_DetailResource: resource detail for an "⚠ unmanaged" row.
func TestGolden_DetailResource(t *testing.T) {
	res := pkgmodel.Resource{
		NativeID:   "arn:aws:s3:::old-logs",
		Stack:      "$unmanaged",
		Type:       "AWS::S3::Bucket",
		Label:      "old-logs",
		Managed:    false,
		Target:     "aws-prod",
		Properties: json.RawMessage(`{"BucketName":"old-logs","Tags":{"Env":"dev","Owner":"ops"}}`),
	}
	r := resourceRow(res)
	mm := buildDetailTestModel(t, []row{r})
	mm = openDetailOnFirstRow(t, mm)
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// TestGolden_DetailStack: stack detail containing a policy reference line.
func TestGolden_DetailStack(t *testing.T) {
	s := &pkgmodel.Stack{
		Label:       "my-stack",
		Description: "test stack",
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Policies:    []json.RawMessage{json.RawMessage(`{"$ref":"policy://shared-retention"}`)},
	}

	now := func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }
	r := stackRow(s, now())

	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabStacks,
		Now:      now,
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(tabLoadedMsg{tab: TabStacks, rows: []row{r}})

	// Switch to stacks tab view.
	model := mm.(Model)
	model.active = TabStacks
	mm = model

	mm = openDetailOnFirstRow(t, mm)
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}

// TestGolden_DetailPolicy: policy detail with multiple attached stacks.
func TestGolden_DetailPolicy(t *testing.T) {
	p := apimodel.PolicyInventoryItem{
		Label:          "shared-retention",
		Type:           "ttl",
		Config:         json.RawMessage(`{"TTLSeconds": 86400}`),
		AttachedStacks: []string{"production", "staging", "network"},
	}
	r := policyRow(p)

	fc := buildFixtureClientFull()
	opts := Options{
		FocusTab: TabPolicies,
		Now:      func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) },
	}
	m := newTestInventoryModel(t, fc, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(tabLoadedMsg{tab: TabPolicies, rows: []row{r}})

	// Switch to policies tab.
	model := mm.(Model)
	model.active = TabPolicies
	mm = model

	mm = openDetailOnFirstRow(t, mm)
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}
