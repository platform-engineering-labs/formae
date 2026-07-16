// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func defaultKeyMap() tui.KeyMap {
	return tui.DefaultKeyMap()
}

func makeTerminalCmd() apimodel.Command {
	return apimodel.Command{
		CommandID: "cmd-abc123",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "Success",
		StartTs:   time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC),
		EndTs:     time.Date(2026, 7, 16, 11, 0, 42, 0, time.UTC),
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "aws-us-east-1", Operation: "create", State: "Success", Duration: 3000, Discoverable: true},
		},
		StackUpdates: []apimodel.StackUpdate{
			{StackLabel: "production", Operation: "create", State: "Success", Duration: 1000, Description: "Production environment"},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "auto-reconcile", PolicyType: "ttl", StackLabel: "production", Operation: "update", State: "Success", Duration: 1000},
		},
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create", State: "Success", Duration: 5000, CurrentAttempt: 1, MaxAttempts: 9},
			{ResourceLabel: "web-1", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create", State: "Success", Duration: 8000},
		},
	}
}

func makeTerminalRow() row {
	c := makeTerminalCmd()
	counts := commandCounts(c)
	return row{cmd: c, counts: counts, health: commandHealth(c, counts)}
}

func TestDetailModel_SummaryRowsAndSecondLines(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)

	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "Failed",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceLabel:  "old-data",
				ResourceType:   "AWS::S3::Bucket",
				StackName:      "production",
				Operation:      "delete",
				State:          "Failed",
				Duration:       4000,
				CurrentAttempt: 9,
				MaxAttempts:    9,
				ErrorMessage:   "BucketNotEmpty: The bucket you tried to delete is not empty",
			},
			{
				ResourceLabel: "legacy-3",
				ResourceType:  "AWS::EC2::Instance",
				StackName:     "production",
				Operation:     "delete",
				State:         "Canceled",
				CascadeSource: "legacy-2",
			},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now)

	v := plain(dm.View(30))
	assert.Contains(t, v, "▌ Resources", "section header")
	assert.Contains(t, v, "BucketNotEmpty", "error message second line")
	assert.Contains(t, v, "depends on legacy-2", "cascade second line")
}

func TestDetailModel_ShowMoreRow(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "InProgress",
	}
	for i := 0; i < 25; i++ {
		c.ResourceUpdates = append(c.ResourceUpdates, apimodel.ResourceUpdate{
			ResourceLabel: fmt.Sprintf("res-%02d", i),
			ResourceType:  "AWS::S3::Bucket",
			StackName:     "production",
			Operation:     "create",
			State:         "Success",
		})
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now)

	v := plain(dm.View(40))
	assert.Contains(t, v, "show 10 more (15 remaining)")

	// cursor to show-more row (row index 10, after 10 visible rows)
	// navigate down 10 times from 0
	keys := defaultKeyMap()
	for i := 0; i < 10; i++ {
		dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyDown}, keys)
	}
	// Now cursor should be on the show-more row — press enter
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter}, keys)
	assert.Equal(t, 20, dm.visible[kindResource], "visible should expand by 10 to 20")
}

func TestDetailModel_ExpandCardByKey(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-test",
		State:     "Success",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create", State: "Success", Duration: 5000, CurrentAttempt: 1, MaxAttempts: 9},
			{ResourceLabel: "web-1", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create", State: "Success", Duration: 8000},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now)

	keys := defaultKeyMap()
	// Expand first resource row (cursor=0)
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter}, keys)

	v := plain(dm.View(40))
	assert.Contains(t, v, "AWS::S3::Bucket", "expanded card shows type")

	// Rebuild with same command (simulating a poll refresh)
	dm = dm.SetCommand(c, r, "◉", now)

	// Expansion must survive SetCommand - the key is "resource/production/my-bucket"
	v2 := plain(dm.View(40))
	assert.Contains(t, v2, "AWS::S3::Bucket", "expansion survives SetCommand via key")
}

func TestDetailModel_DetailModeToggle(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now)

	assert.False(t, dm.detailMode)
	keys := defaultKeyMap()
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}, keys)
	assert.True(t, dm.detailMode, "'d' sets detailMode to true")

	// In detail mode all resource rows should render as cards (bordered)
	v := plain(dm.View(40))
	assert.Contains(t, v, "╭", "detail mode shows bordered cards")
}

func TestDetailModel_CancelStateLabels(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-cancel",
		State:     "Canceling",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "running", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
			{ResourceLabel: "canceled", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now)

	v := plain(dm.View(40))
	assert.Contains(t, v, "finishing", "in-progress row on canceling command shows 'finishing'")
	assert.Contains(t, v, "canceled", "canceled row shows 'canceled'")
}

func TestDetailModel_BackReturnsTrue(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)
	keys := defaultKeyMap()

	_, back := dm.Update(tea.KeyMsg{Type: tea.KeyEsc}, keys)
	assert.True(t, back, "esc returns back=true")

	_, back2 := dm.Update(tea.KeyMsg{Type: tea.KeyBackspace}, keys)
	assert.True(t, back2, "backspace returns back=true")
}

func TestModel_EnterDrillsIn_EscReturns(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	c := makeTerminalCmd()
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{c}})

	// Enter should drill into detail view
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm.(Model)
	assert.Equal(t, viewDetail, got.view, "enter sets view to viewDetail")

	v := plain(got.View())
	assert.Contains(t, v, "cmd-abc123", "detail view shows command ID in pinned header")

	// Esc should return to multi view
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got2 := mm.(Model)
	assert.Equal(t, viewMulti, got2.view, "esc returns to viewMulti")
}

func TestModel_RefreshWhileInDetail_UpdatesGroups(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	c := apimodel.Command{
		CommandID: "cmd-refresh",
		Command:   "apply",
		State:     "InProgress",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Pending"},
		},
	}
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{c}})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill in

	assert.Equal(t, viewDetail, mm.(Model).view)

	// State flips to Success
	c2 := c
	c2.ResourceUpdates[0].State = "Success"
	c2.State = "Success"
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{c2}})

	v := plain(mm.(Model).View())
	assert.Contains(t, v, "✓", "refreshed state shows success glyph")
	assert.Equal(t, viewDetail, mm.(Model).view, "still in detail view after refresh")
}

func TestDetailModel_FocusCommandID(t *testing.T) {
	fc := &fakeClient{resp: &apimodel.ListCommandStatusResponse{
		Commands: []apimodel.Command{makeTerminalCmd()},
	}}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m := New(theme.New("formae"), fc, Options{
		MaxResults:     10,
		PollInterval:   time.Hour,
		Now:            func() time.Time { return now },
		FocusCommandID: "cmd-abc123",
	})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm, _ = mm.Update(commandsMsg{commands: fc.resp.Commands})

	got := mm.(Model)
	assert.Equal(t, viewDetail, got.view, "FocusCommandID starts in detail view")
	assert.Equal(t, "cmd-abc123", got.detail.cmdID)
}

func TestDetailModel_PinnedRowUsesInjectedNow(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)

	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	c := apimodel.Command{
		CommandID: "cmd-running",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "InProgress",
		StartTs:   now.Add(-42 * time.Second),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceLabel: "bucket", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	dm = dm.SetCommand(c, r, "◉", now)

	pinnedRow := plain(dm.pinnedRow)
	assert.Contains(t, pinnedRow, "00:42", "pinned Time column derives from injected now")
	assert.NotRegexp(t, `-\d`, pinnedRow, "no garbage negative duration from a zero clock")
}

func TestDetailModel_PinnedHeaderNoAgeAndAligned(t *testing.T) {
	th := theme.New("formae")
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	for _, w := range []int{100, 70} {
		dm := newDetailModel(th, w, 30)
		dm = dm.SetCommand(c, r, "◉", now)

		header := plain(dm.pinnedHeader)
		rowStr := plain(dm.pinnedRow)
		assert.NotContains(t, header, "Age", "pinned header omits Age at width %d", w)
		assert.Equal(t, len([]rune(header)), len([]rune(rowStr)),
			"pinned header and row visible widths must match at width %d", w)
	}
}

func TestDetailModel_Golden(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 30)
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now)

	// Expand first resource card
	keys := defaultKeyMap()
	// Navigate to Resources group (skip targets, stacks, policies)
	// Target=0, Stack=1, Policy=2, Resource starts at index 3
	for i := 0; i < 3; i++ {
		dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyDown}, keys)
	}
	dm, _ = dm.Update(tea.KeyMsg{Type: tea.KeyEnter}, keys)

	tuitest.RequireGolden(t, []byte(dm.View(30)))
}
