// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		{NativeID: "arn:aws:s3:::old-logs", Stack: "unmanaged", Type: "AWS::S3::Bucket", Label: "old-logs"},
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
