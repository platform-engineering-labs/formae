// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// tabHarness: minimal tea.Model wrapper around one tabModel.
//
// Update handles tea.WindowSizeMsg → setSize + sync.
// View joins view() lines + "\n" + statusLine.
// ---------------------------------------------------------------------------

type tabHarness struct {
	tab     tabModel
	th      *theme.Theme
	maxRows int
	spinStr string
}

func newHarness(tab tabModel, maxRows int) tabHarness {
	return tabHarness{
		tab:     tab,
		th:      theme.New("formae"),
		maxRows: maxRows,
		spinStr: "⠋",
	}
}

func (h tabHarness) Init() tea.Cmd { return nil }

func (h tabHarness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sz, ok := msg.(tea.WindowSizeMsg); ok {
		h.tab = h.tab.setSize(sz.Width, sz.Height)
		h.tab = h.tab.sync(h.maxRows)
	}
	return h, nil
}

func (h tabHarness) View() string {
	lines := h.tab.view(h.th, h.maxRows, h.spinStr)
	sl := h.tab.statusLine(h.maxRows)
	all := append(lines, sl)
	return strings.Join(all, "\n")
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// resourceSpec returns the real resources tabSpec (columns, entity).
func resourceSpec() tabSpec {
	specs := newSpecs(nil)
	return specs[TabResources]
}

// buildFixtureResources builds n resource rows using the real resourceRow builder.
func buildFixtureResources(n int) []row {
	resources := []pkgmodel.Resource{
		{NativeID: "arn:aws:s3:::my-bucket", Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket"},
		{NativeID: "i-0abc123456789", Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1"},
		{NativeID: "sg-0123456789ab", Stack: "production", Type: "AWS::EC2::SecurityGroup", Label: "web-sg"},
		{NativeID: "arn:aws:rds:::db/primary", Stack: "production", Type: "AWS::RDS::DBInstance", Label: "primary"},
		{NativeID: "arn:aws:s3:::old-logs", Stack: "$unmanaged", Type: "AWS::S3::Bucket", Label: "old-logs"},
		{NativeID: "subnet-0abc123456789a", Stack: "network", Type: "AWS::EC2::Subnet", Label: "subnet-1"},
		{NativeID: "subnet-0abc123456789b", Stack: "network", Type: "AWS::EC2::Subnet", Label: "subnet-2"},
		{NativeID: "rtb-0abc123456789c", Stack: "network", Type: "AWS::EC2::RouteTable", Label: "rt-main"},
		{NativeID: "igw-0abc123456789d", Stack: "network", Type: "AWS::EC2::InternetGateway", Label: "igw-main"},
		{NativeID: "arn:aws:iam:::role/app", Stack: "iam", Type: "AWS::IAM::Role", Label: "app-role"},
	}
	rows := make([]row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, resourceRow(resources[i%len(resources)]))
	}
	return rows
}

// makeTab creates a tabModel in the loaded state with the given rows.
func makeTab(th *theme.Theme, rows []row) tabModel {
	spec := resourceSpec()
	tm := newTabModel(th, spec)
	tm.state = tabLoaded
	tm.allRows = rows
	return tm
}

// makeLoadingTab creates a tabModel in the loading state.
func makeLoadingTab(th *theme.Theme) tabModel {
	spec := resourceSpec()
	tm := newTabModel(th, spec)
	tm.state = tabLoading
	return tm
}

// makeFailedTab creates a tabModel in the failed state.
func makeFailedTab(th *theme.Theme) tabModel {
	spec := resourceSpec()
	tm := newTabModel(th, spec)
	tm.state = tabFailed
	tm.err = errors.New("connection refused: agent unreachable")
	return tm
}

// ---------------------------------------------------------------------------
// Exact-fill contract (non-golden): len(view()) must equal height budget
// ---------------------------------------------------------------------------

func TestView_ExactFillLoaded(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(5)
	tm := makeTab(th, rows)
	tm = tm.setSize(100, 20)
	tm = tm.sync(0)

	lines := tm.view(th, 0, "⠋")
	assert.Len(t, lines, 20, "view must return exactly height lines for loaded state")
}

func TestView_ExactFillLoading(t *testing.T) {
	th := theme.New("formae")
	tm := makeLoadingTab(th)
	tm = tm.setSize(100, 20)

	lines := tm.view(th, 0, "⠋")
	assert.Len(t, lines, 20, "view must return exactly height lines for loading state")
}

func TestView_ExactFillEmpty(t *testing.T) {
	th := theme.New("formae")
	tm := makeTab(th, nil)
	tm = tm.setSize(100, 20)
	tm = tm.sync(0)

	lines := tm.view(th, 0, "⠋")
	assert.Len(t, lines, 20, "view must return exactly height lines for empty state")
}

func TestView_ExactFillFailed(t *testing.T) {
	th := theme.New("formae")
	tm := makeFailedTab(th)
	tm = tm.setSize(100, 20)

	lines := tm.view(th, 0, "⠋")
	assert.Len(t, lines, 20, "view must return exactly height lines for failed state")
}

func TestView_ExactFillTruncated(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(10)
	tm := makeTab(th, rows)
	tm = tm.setSize(100, 24)
	tm = tm.sync(3)

	lines := tm.view(th, 3, "⠋")
	assert.Len(t, lines, 24, "view must return exactly height lines for truncated state")
}

// ---------------------------------------------------------------------------
// statusLine unit tests
// ---------------------------------------------------------------------------

func TestStatusLine_UnfilteredNoTrunc(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(3)
	tm := makeTab(th, rows)
	tm = tm.setSize(100, 24)
	tm = tm.sync(0)

	sl := tm.statusLine(0)
	assert.Equal(t, "Showing 3 of 3 resources", sl)
}

func TestStatusLine_Filtered(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(5)
	tm := makeTab(th, rows)
	tm.filter = "production"
	tm = tm.setSize(100, 24)
	tm = tm.sync(0)

	sl := tm.statusLine(0)
	assert.Contains(t, sl, "(filtered)")
}

func TestStatusLine_TruncatedNotFiltered(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(10)
	tm := makeTab(th, rows)
	tm = tm.setSize(100, 24)
	tm = tm.sync(3)

	sl := tm.statusLine(3)
	assert.Equal(t, "Showing 3 of 10 resources — refine your query to see more", sl)
}

func TestStatusLine_Cap0ShowsAll(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(7)
	tm := makeTab(th, rows)
	tm = tm.setSize(100, 24)
	tm = tm.sync(0)

	sl := tm.statusLine(0)
	assert.Equal(t, "Showing 7 of 7 resources", sl)
}

// ---------------------------------------------------------------------------
// Golden tests — 4 scenarios at 100x24
// ---------------------------------------------------------------------------

const goldenWidth = 100
const goldenHeight = 24

// harnessSend sends a WindowSizeMsg to the harness and returns the updated model.
func harnessSend(h tabHarness, width, height int) tabHarness {
	m, _ := h.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m.(tabHarness)
}

// TestGolden_LoadedResources: 5 rows incl. one ⚠ unmanaged.
func TestGolden_LoadedResources(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(5)
	h := newHarness(makeTab(th, rows), 0)
	h = harnessSend(h, goldenWidth, goldenHeight)
	tuitest.RequireGolden(t, []byte(h.View()))
}

// TestGolden_Truncated: 10 rows, cap 3 → dashed marker + "7 more results not shown…" + truncated status.
func TestGolden_Truncated(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(10)
	h := newHarness(makeTab(th, rows), 3)
	h = harnessSend(h, goldenWidth, goldenHeight)
	tuitest.RequireGolden(t, []byte(h.View()))
}

// TestGolden_Empty: empty state — "No resources found."
func TestGolden_Empty(t *testing.T) {
	th := theme.New("formae")
	h := newHarness(makeTab(th, nil), 0)
	h = harnessSend(h, goldenWidth, goldenHeight)
	tuitest.RequireGolden(t, []byte(h.View()))
}

// TestGolden_Failed: error banner + "r: retry".
func TestGolden_Failed(t *testing.T) {
	th := theme.New("formae")
	h := newHarness(makeFailedTab(th), 0)
	h = harnessSend(h, goldenWidth, goldenHeight)
	tuitest.RequireGolden(t, []byte(h.View()))
}

// ---------------------------------------------------------------------------
// Truncation marker content test
// ---------------------------------------------------------------------------

func TestView_TruncationMarkerAndCount(t *testing.T) {
	th := theme.New("formae")
	rows := buildFixtureResources(10)
	tm := makeTab(th, rows)
	tm = tm.setSize(100, 24)
	tm = tm.sync(3)

	lines := tm.view(th, 3, "⠋")

	// Join all lines to search within
	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "7 more results not shown")
	require.Contains(t, joined, "Use /: search to filter")
}

// ---------------------------------------------------------------------------
// setSize wires width/height (smoke test)
// ---------------------------------------------------------------------------

func TestSetSize_StoresDimensions(t *testing.T) {
	th := theme.New("formae")
	tm := makeTab(th, buildFixtureResources(3))
	tm2 := tm.setSize(80, 20)
	assert.Equal(t, 80, tm2.width)
	assert.Equal(t, 20, tm2.height)
}

// ---------------------------------------------------------------------------
// Narrow-terminal regression: setSize at <30 cols must not panic and every
// rendered line must be ≤ width printable columns (R8 overflow guard).
// ---------------------------------------------------------------------------

func TestNarrowTerminal_NoPanicAndFitsWidth(t *testing.T) {
	// narrowWidth must be < 22 (= Label col width 20 + 2 padding) to trigger
	// the overflow guard in setSize, which rebuilds the table via NewTable.
	const narrowWidth = 15
	const narrowHeight = 20

	th := theme.New("formae")
	rows := buildFixtureResources(5)
	tm := makeTab(th, rows)

	// This exercises the overflow guard in setSize which rebuilds the table.
	// Before the fix, components.NewTable(nil, cols) panics here.
	require.NotPanics(t, func() {
		tm = tm.setSize(narrowWidth, narrowHeight)
		tm = tm.sync(0)
	}, "setSize/sync must not panic on very narrow terminals")

	lines := tm.view(th, 0, "⠋")
	assert.Len(t, lines, narrowHeight, "view must return exactly height lines")

	for i, line := range lines {
		w := lipgloss.Width(line)
		assert.LessOrEqualf(t, w, narrowWidth,
			"line %d is %d cols wide (limit %d): %q", i, w, narrowWidth, line)
	}
}

// ---------------------------------------------------------------------------
// styleCell hook: per-entity rendering
// ---------------------------------------------------------------------------

// buildResourcesTabLoaded returns a loaded tabModel for the resources spec with
// the given rows, sized at 100×24.
func buildResourcesTabLoaded(th *theme.Theme, rows []row) tabModel {
	specs := newSpecs(nil)
	tm := newTabModel(th, specs[TabResources])
	tm.state = tabLoaded
	tm.allRows = rows
	tm = tm.setSize(100, 24)
	tm = tm.sync(0)
	return tm
}

// buildTargetsTabLoaded returns a loaded tabModel for the targets spec with
// the given rows, sized at 100×24.
func buildTargetsTabLoaded(th *theme.Theme, rows []row) tabModel {
	specs := newSpecs(nil)
	tm := newTabModel(th, specs[TabTargets])
	tm.state = tabLoaded
	tm.allRows = rows
	tm = tm.setSize(100, 24)
	tm = tm.sync(0)
	return tm
}

// TestStyleCell_Resources_UnmanagedHasErrorAnsi verifies that after sync the
// rendered view contains the StatusFailed ANSI sequence around "⚠ unmanaged".
func TestStyleCell_Resources_UnmanagedHasErrorAnsi(t *testing.T) {
	th := theme.New("formae")
	rows := []row{resourceRow(pkgmodel.Resource{
		NativeID: "arn:aws:s3:::old-logs",
		Stack:    "$unmanaged",
		Type:     "AWS::S3::Bucket",
		Label:    "old-logs",
	})}

	tm := buildResourcesTabLoaded(th, rows)
	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")

	// Render the expected fragment using the same style, then check it appears
	// in the table output.
	expected := th.Styles.StatusFailed.Render("⚠ unmanaged")
	require.Contains(t, joined, expected,
		"rendered table must contain StatusFailed-styled '⚠ unmanaged'")
}

// TestStyleCell_Resources_ManagedStackNoAnsi verifies that a managed (non-⚠)
// Stack cell is NOT wrapped in the error style.
func TestStyleCell_Resources_ManagedStackNoAnsi(t *testing.T) {
	th := theme.New("formae")
	rows := []row{resourceRow(pkgmodel.Resource{
		NativeID: "arn:aws:s3:::bucket",
		Stack:    "production",
		Type:     "AWS::S3::Bucket",
		Label:    "bucket",
	})}

	tm := buildResourcesTabLoaded(th, rows)
	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")

	// The error style must NOT wrap "production".
	errorStyled := th.Styles.StatusFailed.Render("production")
	require.NotContains(t, joined, errorStyled,
		"managed stack cell must not be wrapped in error style")
}

// TestStyleCell_Targets_YesHasAccentAnsi verifies that a discoverable "yes"
// cell is rendered with the Accent style.
func TestStyleCell_Targets_YesHasAccentAnsi(t *testing.T) {
	th := theme.New("formae")
	tgt := &pkgmodel.Target{
		Label:        "aws-prod",
		Namespace:    "AWS",
		Discoverable: true,
	}
	rows := []row{targetRow(tgt)}

	tm := buildTargetsTabLoaded(th, rows)
	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")

	expected := th.Styles.Accent.Render("yes")
	require.Contains(t, joined, expected,
		"discoverable 'yes' cell must be rendered with Accent style")
}

// TestStyleCell_Targets_NoPlain verifies that "no" for Discoverable is NOT
// wrapped in the accent style.
func TestStyleCell_Targets_NoPlain(t *testing.T) {
	th := theme.New("formae")
	tgt := &pkgmodel.Target{
		Label:        "dev",
		Namespace:    "AWS",
		Discoverable: false,
	}
	rows := []row{targetRow(tgt)}

	tm := buildTargetsTabLoaded(th, rows)
	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")

	accentNo := th.Styles.Accent.Render("no")
	require.NotContains(t, joined, accentNo,
		"non-discoverable 'no' must not be accent-styled")
}

// ---------------------------------------------------------------------------
// Fix wave 3: column-bounded cell styling regression tests
// ---------------------------------------------------------------------------

// TestStyleCell_YesProdLabel_LabelCellNotCorrupted verifies that a target
// labeled "yes-prod" does not get accent ANSI injected into the Label cell.
// The "yes" substring in the label must not be confused with the Discoverable
// "yes" cell in col 2. (a)
func TestStyleCell_YesProdLabel_LabelCellNotCorrupted(t *testing.T) {
	th := theme.New("formae")
	tgt := &pkgmodel.Target{
		Label:        "yes-prod",
		Namespace:    "AWS",
		Discoverable: true,
	}
	rows := []row{targetRow(tgt)}

	tm := buildTargetsTabLoaded(th, rows)
	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")

	// The accent-styled "yes" must appear exactly for the Discoverable col, not
	// injected into "yes-prod" in the Label col. Check that "yes-prod" appears
	// in the output WITHOUT any ANSI escape injection within it.
	accentYes := th.Styles.Accent.Render("yes")

	// The rendered output must contain accent-styled "yes" (Discoverable col).
	require.Contains(t, joined, accentYes,
		"Discoverable 'yes' must be accent-styled")

	// "yes-prod" must appear plain (no accent codes embedded in the label).
	// We check that the accent prefix (ESC code) does not appear immediately
	// before "yes-prod" by verifying no ANSI-prefixed "yes-prod" substring.
	// The accent style wraps as "<ESC>...<m>yes<ESC>[0m" — so
	// accentYes + "-prod" would indicate corruption.
	require.NotContains(t, joined, accentYes+"-prod",
		"Label cell 'yes-prod' must not have accent ANSI injected into it")
}

// TestStyleCell_MultiRowBothDiscoverable verifies two rows both discoverable
// both get styled. (b)
func TestStyleCell_MultiRowBothDiscoverable(t *testing.T) {
	th := theme.New("formae")
	rows := []row{
		targetRow(&pkgmodel.Target{Label: "aws-prod", Namespace: "AWS", Discoverable: true}),
		targetRow(&pkgmodel.Target{Label: "azure-dev", Namespace: "Azure", Discoverable: true}),
	}

	tm := buildTargetsTabLoaded(th, rows)
	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")

	accentYes := th.Styles.Accent.Render("yes")
	count := strings.Count(joined, accentYes)
	assert.Equal(t, 2, count, "both rows must have accent-styled 'yes'")
}

// TestStyleCell_AlignmentPreserved verifies that applying cell styles does not
// change the visual width of any rendered line. (c)
func TestStyleCell_AlignmentPreserved(t *testing.T) {
	th := theme.New("formae")
	rows := []row{
		targetRow(&pkgmodel.Target{Label: "yes-prod", Namespace: "AWS", Discoverable: true}),
		targetRow(&pkgmodel.Target{Label: "no-env", Namespace: "Azure", Discoverable: false}),
	}

	// Build a version with styling applied.
	tm := buildTargetsTabLoaded(th, rows)
	styledLines := tm.view(th, 0, "⠋")

	// Build a version without styling: empty styleCell so replacements are identity.
	specs := newSpecs(nil)
	spec := specs[TabTargets]
	spec.styleCell = nil
	tmPlain := newTabModel(th, spec)
	tmPlain.state = tabLoaded
	tmPlain.allRows = rows
	tmPlain = tmPlain.setSize(100, 24)
	tmPlain = tmPlain.sync(0)
	plainLines := tmPlain.view(th, 0, "⠋")

	require.Equal(t, len(plainLines), len(styledLines), "line count must match")
	for i := range plainLines {
		pw := lipgloss.Width(plainLines[i])
		sw := lipgloss.Width(styledLines[i])
		assert.Equalf(t, pw, sw, "line %d visual width changed: plain=%d styled=%d", i, pw, sw)
	}
}

// TestStyleCell_CursorOnStyledRow_NoCorruption verifies that when the cursor
// sits on a row with a styled cell (⚠ unmanaged), the output lines have
// unchanged visual widths. (d)
func TestStyleCell_CursorOnStyledRow_NoCorruption(t *testing.T) {
	th := theme.New("formae")
	// Use the resources tab which styles the Stack col for unmanaged resources.
	rows := []row{resourceRow(pkgmodel.Resource{
		NativeID: "arn:aws:s3:::old-logs",
		Stack:    "$unmanaged",
		Type:     "AWS::S3::Bucket",
		Label:    "old-logs",
	})}

	// Plain version (no styleCell) for width reference.
	specs := newSpecs(nil)
	spec := specs[TabResources]
	spec.styleCell = nil
	tmPlain := newTabModel(th, spec)
	tmPlain.state = tabLoaded
	tmPlain.allRows = rows
	tmPlain = tmPlain.setSize(100, 24)
	tmPlain = tmPlain.sync(0)
	plainLines := tmPlain.view(th, 0, "⠋")

	// Styled version (cursor on row 0 by default).
	tm := buildResourcesTabLoaded(th, rows)
	styledLines := tm.view(th, 0, "⠋")

	require.Equal(t, len(plainLines), len(styledLines))
	for i := range plainLines {
		pw := lipgloss.Width(plainLines[i])
		sw := lipgloss.Width(styledLines[i])
		assert.Equalf(t, pw, sw,
			"line %d visual width changed (cursor row styling corruption): plain=%d styled=%d", i, pw, sw)
	}
}

// TestStyleCell_TruncateBeforeStyle verifies the truncate-then-style contract:
// a cell longer than the column width must be truncated BEFORE styling, so the
// rendered output contains the truncated-then-styled text (not the full text
// styled), and every output line fits within the terminal width.
func TestStyleCell_TruncateBeforeStyle(t *testing.T) {
	th := theme.New("formae")
	// Build a spec with a narrow column (width 8) and a styleCell that wraps with
	// the accent style. The cell value is 20 characters — longer than 8.
	const colWidth = 8
	const termWidth = 40

	styleApplied := false
	spec := tabSpec{
		title:  "Test",
		entity: "tests",
		columns: []components.Column{
			{Title: "Name", Width: colWidth, Priority: 0},
		},
		styleCell: func(th *theme.Theme, col int, cell string) string {
			styleApplied = true
			return th.Styles.Accent.Render(cell)
		},
	}

	longCell := "verylongcellvalue!!" // 19 chars, exceeds colWidth=8
	r := row{cells: []string{longCell}}

	tm := newTabModel(th, spec)
	tm.state = tabLoaded
	tm.allRows = []row{r}
	tm = tm.setSize(termWidth, 10)
	tm = tm.sync(0)

	// styleCell must have been invoked.
	require.True(t, styleApplied, "styleCell must be called during sync")

	// The rendered text must contain the truncated-then-styled fragment, not the full cell.
	truncated := components.Truncate(longCell, colWidth)
	require.NotEqual(t, longCell, truncated, "fixture: cell must be longer than colWidth")
	expectedStyled := th.Styles.Accent.Render(truncated)

	rendered := tm.view(th, 0, "⠋")
	joined := strings.Join(rendered, "\n")
	require.Contains(t, joined, expectedStyled,
		"rendered output must contain the truncated-then-styled text")

	// Full-cell styled must NOT appear (styling was applied after truncation).
	fullStyled := th.Styles.Accent.Render(longCell)
	require.NotContains(t, joined, fullStyled,
		"full (un-truncated) styled text must not appear")

	// Every output line must fit within the terminal width.
	for i, line := range rendered {
		w := lipgloss.Width(line)
		assert.LessOrEqualf(t, w, termWidth,
			"line %d is %d cols wide (limit %d)", i, w, termWidth)
	}
}
