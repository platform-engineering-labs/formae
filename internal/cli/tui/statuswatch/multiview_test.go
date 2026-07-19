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
	v := multiView{th: theme.New("formae"), rows: buildRows([]apimodel.Command{c}), width: 110, now: now}
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
	v := multiView{th: theme.New("formae"), rows: buildRows([]apimodel.Command{c}), width: 110, now: now, spinView: "◉"}
	out := plain(strings.Join(v.renderRows(10), "\n"))
	for _, seg := range []string{"█", "▒", "░", "⋅"} {
		assert.Contains(t, out, seg)
	}
	assert.Contains(t, out, "1/4") // done/total alongside the bar
}

func TestMultiView_HeaderShowsSortIndicator(t *testing.T) {
	v := multiView{th: theme.New("formae"), width: 110, sortCol: colAge, sortDir: components.SortDesc, sortHi: colAge}
	h := plain(v.headerRow())
	assert.Contains(t, h, "Age ▼")
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
	v := multiView{th: theme.New("formae"), rows: buildRows(cmds), width: 100, now: now}
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
