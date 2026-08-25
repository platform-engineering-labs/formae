// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func cmdFix(id, cmdType, state string, started time.Time, failed int) apimodel.Command {
	c := apimodel.Command{CommandID: id, Command: cmdType, State: state, StartTs: started}
	for i := 0; i < failed; i++ {
		c.ResourceUpdates = append(c.ResourceUpdates, apimodel.ResourceUpdate{State: "Failed"})
	}
	c.ResourceUpdates = append(c.ResourceUpdates, apimodel.ResourceUpdate{State: "Success"})
	return c
}

func TestSortRows_DefaultUrgency(t *testing.T) {
	now := time.Now()
	rows := buildRows([]apimodel.Command{
		cmdFix("finished-ok", "apply", "Success", now, 0),
		cmdFix("running-failing", "apply", "InProgress", now, 1),
		cmdFix("finished-failed", "apply", "Failed", now, 1),
		cmdFix("running-healthy", "apply", "InProgress", now, 0),
	})
	sortRows(rows, colStatus, components.SortAsc, now)
	got := []string{rows[0].cmd.CommandID, rows[1].cmd.CommandID, rows[2].cmd.CommandID, rows[3].cmd.CommandID}
	assert.Equal(t, []string{"running-failing", "running-healthy", "finished-failed", "finished-ok"}, got)
}

func TestSortRows_ByAgeUsesRealTimestamp(t *testing.T) {
	now := time.Now()
	rows := buildRows([]apimodel.Command{
		cmdFix("older", "apply", "Success", now.Add(-2*time.Hour), 0),
		cmdFix("newer", "apply", "Success", now.Add(-1*time.Hour), 0),
	})
	sortRows(rows, colAge, components.SortDesc, now) // newest first
	assert.Equal(t, "newer", rows[0].cmd.CommandID)
	sortRows(rows, colAge, components.SortAsc, now)
	assert.Equal(t, "older", rows[0].cmd.CommandID)
}

// Browsing history wants the newest first; monitoring in-flight work wants the
// most urgent first, so the two entry points differ deliberately.
func TestListEntryPointSortsByAgeDescending(t *testing.T) {
	m := New(theme.New("formae"), &fakeClient{}, Options{SortNewestFirst: true})
	if m.multi.sortCol != colAge || m.multi.sortDir != components.SortDesc {
		t.Fatalf("list default sort = (%d,%v), want age descending", m.multi.sortCol, m.multi.sortDir)
	}
	// sortHi drives the header highlight: it must name the column actually
	// sorted on, or the header points at a different column than the order.
	if m.multi.sortHi != colAge {
		t.Fatalf("list header highlight = %d, want the sorted column %d", m.multi.sortHi, colAge)
	}
}

func TestWatchEntryPointKeepsUrgencySort(t *testing.T) {
	m := New(theme.New("formae"), &fakeClient{}, Options{})
	if m.multi.sortCol != colStatus {
		t.Fatal("the watch view must keep urgency ordering")
	}
}

// The TUI header must always name the verb the user actually typed to invoke
// it, not a hardcoded default: `command list`, `command status`, and `cancel`
// must show their own names, and the deprecated `status command` alias must
// keep showing its own name (it still routes to one of the two new
// subcommands' semantics underneath, but the header names what was actually
// typed).
func TestHeaderCommand_NamesTheVerbActuallyTyped(t *testing.T) {
	list := New(theme.New("formae"), &fakeClient{}, Options{HeaderCommand: "command list"})
	if got := list.headerCommand(); got != "command list" {
		t.Fatalf("command list header = %q, want %q", got, "command list")
	}

	single := New(theme.New("formae"), &fakeClient{}, Options{HeaderCommand: "command status"})
	if got := single.headerCommand(); got != "command status" {
		t.Fatalf("command status header = %q, want %q", got, "command status")
	}

	cancel := New(theme.New("formae"), &fakeClient{}, Options{HeaderCommand: "cancel"})
	if got := cancel.headerCommand(); got != "cancel" {
		t.Fatalf("cancel header = %q, want %q", got, "cancel")
	}

	compat := New(theme.New("formae"), &fakeClient{}, Options{HeaderCommand: "status command"})
	if got := compat.headerCommand(); got != "status command" {
		t.Fatalf("deprecated status command alias header = %q, want %q", got, "status command")
	}
}

// TestHeaderCommand_EmptyOptionFallsBackToARealVerb locks the fallback used
// when a caller leaves HeaderCommand unset. It must name a verb that still
// exists once the deprecated aliases are removed, and it cannot silently
// change without a deliberate, reviewed edit to this test.
func TestHeaderCommand_EmptyOptionFallsBackToARealVerb(t *testing.T) {
	m := New(theme.New("formae"), &fakeClient{}, Options{})
	if got := m.headerCommand(); got != "command status" {
		t.Fatalf("empty HeaderCommand fallback = %q, want %q", got, "command status")
	}
}

func TestVisibleColumns_DropTiers(t *testing.T) {
	wide := visibleColumns(120)
	for c := 0; c < colCount; c++ {
		assert.True(t, wide[c], "wide terminal shows all columns")
	}

	medium := visibleColumns(80)
	assert.False(t, medium[colMode])
	assert.False(t, medium[colInProg])
	assert.False(t, medium[colPending])
	assert.False(t, medium[colAge])
	assert.True(t, medium[colCommand])

	narrow := visibleColumns(56)
	assert.False(t, narrow[colCommand])
	for _, c := range []int{colStatus, colID, colProgress, colDone, colFailed, colTime} {
		assert.True(t, narrow[c], "always-on column %d", c)
	}
}

// TestVisibleColumns_UserDropsBeforeOperationalColumns pins the ruling that
// User (supplementary attribution) sheds before the tier-2 operational
// columns (Mode, ◐, ○, Age): a 100-column terminal, which showed those
// columns before User existed, must keep showing them — User only appears
// once there is room left over after them.
func TestVisibleColumns_UserDropsBeforeOperationalColumns(t *testing.T) {
	narrowish := visibleColumns(100)
	assert.False(t, narrowish[colUser], "User must drop at a width that predates it having room")
	assert.True(t, narrowish[colMode], "Mode must stay visible at a width that showed it before this column existed")
	assert.True(t, narrowish[colAge], "Age must stay visible at a width that showed it before this column existed")

	wide := visibleColumns(130)
	assert.True(t, wide[colUser], "User appears once there is genuinely room")
}

func TestBarWidth_ElasticWithFloor(t *testing.T) {
	vis := visibleColumns(120)
	assert.GreaterOrEqual(t, barWidth(120, vis), 10)
	assert.Greater(t, barWidth(160, vis), barWidth(120, vis))
	assert.Equal(t, 10, barWidth(40, visibleColumns(40))) // floor
}

func TestMultiView_RowContent(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	c := apimodel.Command{
		CommandID: "cmd-abc123", Command: "apply", Mode: "reconcile",
		State: "Success", StartTs: now.Add(-10 * time.Minute), EndTs: now.Add(-10*time.Minute + 42*time.Second),
		ResourceUpdates: []apimodel.ResourceUpdate{{State: "Success"}, {State: "Success"}},
	}
	// Wide enough to keep every column visible, including the tier-2 Mode/Age.
	v := multiView{th: theme.New("formae"), rows: buildRows([]apimodel.Command{c}), width: 130, now: now}
	out := plain(strings.Join(v.renderRows(10), "\n"))

	assert.Contains(t, out, "cmd-abc123")
	assert.Contains(t, out, "apply")
	assert.Contains(t, out, "reconcile")
	assert.Contains(t, out, "completed 2/2") // terminal command: text, not bar
	assert.Contains(t, out, "00:42")         // duration MM:SS
	assert.Contains(t, out, "10m")           // age
	assert.Contains(t, out, "✓")
}

func TestMultiView_RunningCommandShowsSegmentedBar(t *testing.T) {
	now := time.Now()
	c := apimodel.Command{
		CommandID: "cmd-run1", Command: "apply", Mode: "reconcile",
		State: "InProgress", StartTs: now.Add(-30 * time.Second),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{State: "Success"}, {State: "Failed"}, {State: "InProgress"}, {State: "Pending"},
		},
	}
	th := theme.New("formae")
	v := multiView{th: th, rows: buildRows([]apimodel.Command{c}), width: 110, now: now, spinView: "◉"}
	out := plain(strings.Join(v.renderRows(10), "\n"))
	// Completed (done+failed) is one fill (red here — the command is failing),
	// then in-progress and pending fills. No separate ▒ failed section.
	for _, seg := range []string{th.Progress.FillDone, th.Progress.FillInProgress, th.Progress.FillPending} {
		assert.Contains(t, out, seg)
	}
	assert.NotContains(t, out, "▒")
	assert.Contains(t, out, "1/4") // done/total alongside the bar
}

func TestMultiView_HeaderShowsSortIndicator(t *testing.T) {
	// Wide enough to keep every column visible, including the tier-2 Age.
	v := multiView{th: theme.New("formae"), width: 130, sortCol: colAge, sortDir: components.SortDesc, sortHi: colAge}
	h := plain(v.headerRow())
	assert.Contains(t, h, "Age ▼")
}

// TestMultiView_HeaderGlyphsFollowTheme asserts the ✓/✗/◐/○ column headers
// pick up the active theme's status glyphs instead of the fixed defaults
// baked into multiCols, mirroring what the row cells already do.
func TestMultiView_HeaderGlyphsFollowTheme(t *testing.T) {
	// Markers chosen to not collide with any other column title (ID, Command,
	// Mode, Progress, Time, Age) so Contains() only matches our override.
	th := *theme.New("formae")
	th.Glyphs.StatusDone = "Z1"
	th.Glyphs.StatusFailed = "Z2"
	th.Glyphs.StatusInProgress = "Z3"
	th.Glyphs.StatusPending = "Z4"
	v := multiView{th: &th, width: 130} // wide enough to keep every column visible

	h := plain(v.headerRow())
	assert.Contains(t, h, "Z1", "done column header follows theme")
	assert.Contains(t, h, "Z2", "failed column header follows theme")
	assert.Contains(t, h, "Z3", "in-progress column header follows theme")
	assert.Contains(t, h, "Z4", "pending column header follows theme")
	assert.NotContains(t, h, "✓")
	assert.NotContains(t, h, "✗")
	assert.NotContains(t, h, "◐")
	assert.NotContains(t, h, "○")
}

// TestMultiView_HeaderThemeHighlight pins the theme-driven header emphasis,
// mirroring simview's TestRenderGroupColHeaderThemeHighlight: under
// "rich" (Header.Highlight="background") the navigated (sortHi) column
// carries a background SGR sequence like the row cursor, and the active-sort
// column (when different from the navigated one) carries the PrimaryAccent
// (blue) foreground instead. Under "quiet" (Header.Highlight="brighten")
// neither column gets a background — both just render bright white/bold.
func TestMultiView_HeaderThemeHighlight(t *testing.T) {
	richTh := theme.New("rich")
	richV := multiView{th: richTh, width: 130, sortHi: colID, sortCol: colAge, sortDir: components.SortAsc}
	richHdr := richV.headerRow()

	quietTh := theme.New("quiet")
	quietV := multiView{th: quietTh, width: 130, sortHi: colID, sortCol: colAge, sortDir: components.SortAsc}
	quietHdr := quietV.headerRow()

	fgPrefix := func(c lipgloss.TerminalColor) string {
		return strings.SplitN(lipgloss.NewStyle().Foreground(c).Bold(true).Render("x"), "x", 2)[0]
	}

	// The pending sort-target (navigated) column uses SecondaryAccent as a
	// distinct "cursor" colour in every header mode — never the Selection
	// background (which the row cursor uses) or an underline.
	assert.Contains(t, richHdr, fgPrefix(richTh.Palette.SecondaryAccent), "rich pending-sort column must carry SecondaryAccent")
	assert.Contains(t, quietHdr, fgPrefix(quietTh.Palette.SecondaryAccent), "quiet pending-sort column must carry SecondaryAccent")

	// The active-sort (Age) column under rich (background mode) renders
	// PrimaryAccent; under quiet (brighten mode) it is bold bright white with no
	// accent colour.
	assert.Contains(t, richHdr, fgPrefix(richTh.Palette.PrimaryAccent), "rich active-sort column must carry PrimaryAccent")
	assert.NotContains(t, quietHdr, fgPrefix(quietTh.Palette.PrimaryAccent), "quiet active-sort column must not carry PrimaryAccent")
	assert.Contains(t, quietHdr, "\x1b[1;", "quiet header should still render bold")
}

func TestMultiView_RunningStatusGlyphIsStatic(t *testing.T) {
	now := time.Now()
	c := apimodel.Command{
		CommandID: "cmd-run-static", Command: "apply", Mode: "reconcile",
		State: "InProgress", StartTs: now.Add(-5 * time.Second),
		ResourceUpdates: []apimodel.ResourceUpdate{{State: "InProgress"}},
	}
	th := theme.New("formae")
	// spinView set to a distinctive animated-spinner frame; it must NOT show
	// up in the rendered row now that the redundant animated spinner has been
	// replaced with the static themed in-progress glyph.
	v := multiView{th: th, rows: buildRows([]apimodel.Command{c}), width: 110, now: now, spinView: "@ANIMATED@"}
	out := plain(strings.Join(v.renderRows(10), "\n"))
	assert.NotContains(t, out, "@ANIMATED@", "the animated spinner frame must not appear")
	assert.Contains(t, out, th.Glyphs.StatusInProgress, "the static themed in-progress glyph must appear instead")
}

func TestMultiView_ScrollWindowFollowsCursor(t *testing.T) {
	now := time.Now()
	var cmds []apimodel.Command
	for i := 0; i < 30; i++ {
		cmds = append(cmds, cmdFix(fmt.Sprintf("cmd-%02d", i), "apply", "Success", now, 0))
	}
	v := multiView{th: theme.New("formae"), rows: buildRows(cmds), width: 110, now: now, cursor: 25}
	out := plain(strings.Join(v.renderRows(10), "\n"))
	assert.Contains(t, out, "cmd-25")
	assert.NotContains(t, out, "cmd-00")
}

func TestMultiView_Golden(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	cmds := []apimodel.Command{
		// terminal fixtures only — spinner frames are not golden-stable
		cmdFix("cmd-ok1", "apply", "Success", now.Add(-3*time.Hour), 0),
		cmdFix("cmd-fail1", "apply", "Failed", now.Add(-10*time.Minute), 1),
	}
	// Wide enough to keep every column visible, including the tier-2 Mode/Age
	// and the new User column, so the golden documents the full layout.
	v := multiView{th: theme.New("formae"), rows: buildRows(cmds), width: 130, now: now}
	tuitest.RequireGolden(t, []byte(v.headerRow()+"\n"+strings.Join(v.renderRows(10), "\n")))
}

func TestMultiView_RowWidthMatchesSpec(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	termWidth := 110
	c := apimodel.Command{
		CommandID: "cmd-width1", Command: "apply", Mode: "reconcile",
		State: "Success", StartTs: now.Add(-5 * time.Minute), EndTs: now.Add(-4 * time.Minute),
		ResourceUpdates: []apimodel.ResourceUpdate{{State: "Success"}},
	}
	v := multiView{th: theme.New("formae"), rows: buildRows([]apimodel.Command{c}), width: termWidth, now: now}
	rendered := v.renderRows(1)
	if len(rendered) == 0 {
		t.Fatal("expected at least one row")
	}
	stripped := plain(rendered[0])
	vis := visibleColumns(termWidth)
	bw := barWidth(termWidth, vis)
	fw := fixedWidth(vis)
	assert.Equal(t, fw+bw, len([]rune(stripped)), "rendered row visible width must equal fixedWidth+barWidth")
}

// TestModeLabel verifies destroy commands render "-" (they have no mode) while
// apply commands render their actual mode.
func TestModeLabel(t *testing.T) {
	assert.Equal(t, "-", modeLabel(apimodel.Command{Command: "destroy", Mode: "patch"}),
		"destroy has no mode → -")
	assert.Equal(t, "reconcile", modeLabel(apimodel.Command{Command: "apply", Mode: "reconcile"}))
	assert.Equal(t, "patch", modeLabel(apimodel.Command{Command: "apply", Mode: "patch"}))
}

// TestUserLabel verifies the User column's display value: SubjectName wins
// when present, a bare Subject is used verbatim (the caller truncates it to
// a short prefix at render width), and a command carrying neither is blank.
func TestUserLabel(t *testing.T) {
	assert.Equal(t, "dpanders", UserLabel(apimodel.Command{Subject: "11111111-1111-4111-8111-111111111111", SubjectName: "dpanders"}),
		"SubjectName wins when present")
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", UserLabel(apimodel.Command{Subject: "11111111-1111-4111-8111-111111111111"}),
		"falls back to the raw Subject when there is no display name")
	assert.Equal(t, "", UserLabel(apimodel.Command{}), "no attribution at all is blank")
}

// colOffset returns the visible-rune start offset of column col within a
// rendered row, given the columns actually shown and the elastic bar width —
// mirrors the layout renderRows itself builds, so a test can slice out a
// single cell.
func colOffset(col int, vis map[int]bool, bw int) int {
	off := 0
	for c := 0; c < colCount; c++ {
		if !vis[c] {
			continue
		}
		if c == col {
			return off
		}
		w := multiCols[c].width
		if c == colProgress {
			w = bw
		}
		off += w
	}
	return off
}

// TestMultiView_UserColumn covers the three-way rendering rule for the User
// cell: SubjectName when present, a short prefix of Subject when only that is
// set, and blank when the command carries no attribution at all.
func TestMultiView_UserColumn(t *testing.T) {
	now := time.Now()
	subjectOnly := "22222222-2222-4222-8222-222222222222"
	cmds := []apimodel.Command{
		cmdFix("cmd-named", "apply", "Success", now, 0),
		cmdFix("cmd-subj", "apply", "Success", now, 0),
		cmdFix("cmd-none", "apply", "Success", now, 0),
	}
	cmds[0].Subject = "11111111-1111-4111-8111-111111111111"
	cmds[0].SubjectName = "dpanders"
	cmds[1].Subject = subjectOnly
	// cmds[2] carries neither field.

	// Wide enough to keep every column visible, including the tier-3 User column.
	v := multiView{th: theme.New("formae"), rows: buildRows(cmds), width: 130, now: now}
	rows := v.renderRows(10)
	vis := v.visibleCols()
	bw := barWidth(v.width, vis)
	off := colOffset(colUser, vis, bw)
	w := multiCols[colUser].width

	cell := func(i int) string {
		r := []rune(plain(rows[i]))
		return strings.TrimRight(string(r[off:off+w]), " ")
	}

	assert.Equal(t, "dpanders", cell(0), "SubjectName wins when present")

	got := cell(1)
	assert.NotEqual(t, "", got, "a bare Subject still renders something")
	assert.Less(t, len([]rune(got)), len([]rune(subjectOnly)), "renders a short prefix, not the full subject")
	assert.True(t, strings.HasSuffix(got, "…"), "bare Subject cell must end with truncation marker …")
	assert.True(t, strings.HasPrefix(subjectOnly, strings.TrimSuffix(got, "…")),
		"the rendered prefix must actually be a prefix of Subject, got %q", got)

	assert.Equal(t, "", cell(2), "no attribution at all renders blank")
}
