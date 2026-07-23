// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// TestBuildGroups_AbandonedLabel verifies that a resource update with
// ResourceID in the abandoned set and state Canceled gets stateLabel "Abandoned".
func TestBuildGroups_AbandonedLabel(t *testing.T) {
	c := apimodel.Command{
		CommandID: "cmd-abandoned",
		State:     "Canceled",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-aaa", ResourceLabel: "res-aaa", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ksuid-bbb", ResourceLabel: "res-bbb", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	abandoned := map[string]bool{"ksuid-aaa": true}
	groups := buildGroups(c, abandoned)

	var resGroup *group
	for i := range groups {
		if groups[i].kind == kindResource {
			resGroup = &groups[i]
			break
		}
	}
	if resGroup == nil {
		t.Fatal("expected resource group")
	}

	var aaaRow, bbbRow *updateRow
	for i := range resGroup.rows {
		switch resGroup.rows[i].label {
		case "res-aaa":
			aaaRow = &resGroup.rows[i]
		case "res-bbb":
			bbbRow = &resGroup.rows[i]
		}
	}
	if aaaRow == nil || bbbRow == nil {
		t.Fatal("expected both rows to be present")
	}

	assert.Equal(t, "Abandoned", aaaRow.stateLabel, "resource in abandoned set + Canceled state must get 'Abandoned' label")
	assert.Equal(t, "", bbbRow.stateLabel, "resource NOT in abandoned set gets no text label — the ⊘ glyph conveys canceled")
}

// TestBuildGroups_NonAbandonedCanceledLabel verifies that a Canceled resource
// update with ResourceID NOT in the abandoned set gets NO text label — the ⊘
// glyph conveys canceled; only force-canceled rows get the "Abandoned" label.
func TestBuildGroups_NonAbandonedCanceledLabel(t *testing.T) {
	c := apimodel.Command{
		CommandID: "cmd-x",
		State:     "Canceled",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-ccc", ResourceLabel: "res-ccc", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	groups := buildGroups(c, nil)
	var resGroup *group
	for i := range groups {
		if groups[i].kind == kindResource {
			resGroup = &groups[i]
			break
		}
	}
	if resGroup == nil {
		t.Fatal("expected resource group")
	}
	assert.Equal(t, "", resGroup.rows[0].stateLabel)
}

// TestBuildGroups_EmptyAbandonedSet verifies zero behavior change when set is empty.
func TestBuildGroups_EmptyAbandonedSet(t *testing.T) {
	c := apimodel.Command{
		CommandID: "cmd-y",
		State:     "Canceling",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-ddd", ResourceLabel: "running", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
			{ResourceID: "ksuid-eee", ResourceLabel: "skipped", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	groups := buildGroups(c, map[string]bool{})

	var resGroup *group
	for i := range groups {
		if groups[i].kind == kindResource {
			resGroup = &groups[i]
			break
		}
	}
	if resGroup == nil {
		t.Fatal("expected resource group")
	}
	for _, r := range resGroup.rows {
		assert.NotEqual(t, "Abandoned", r.stateLabel, "empty set must not produce Abandoned labels")
	}
}

// TestDetailModel_AbandonedLabel verifies that after SetCommand with an abandoned set,
// the rendered view contains "Abandoned" text.
func TestDetailModel_AbandonedLabel(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-ab",
		State:     "Canceled",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-111", ResourceLabel: "res-111", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ksuid-222", ResourceLabel: "res-222", ResourceType: "AWS::EC2::Instance", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	abandoned := map[string]bool{"ksuid-111": true}
	dm = dm.SetCommand(c, r, "◉", now, abandoned)

	v := plain(dm.View(40, false))
	assert.Contains(t, v, "Abandoned", "view must contain 'Abandoned' label for abandoned resource")
	assert.Contains(t, v, "res-222", "non-abandoned canceled resource must still render (⊘ glyph, no text label)")
}

// TestDetailModel_FooterReminderWhenTerminalAbandoned verifies the reminder
// appears when command is terminal and there are abandoned rows.
func TestDetailModel_FooterReminderWhenTerminalAbandoned(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-reminder",
		State:     "Canceled",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-aaa", ResourceLabel: "res-a", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ksuid-bbb", ResourceLabel: "res-b", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	abandoned := map[string]bool{"ksuid-aaa": true, "ksuid-bbb": true}
	dm = dm.SetCommand(c, r, "◉", now, abandoned)

	v := plain(dm.View(40, false))
	assert.Contains(t, v, "in-progress updates were abandoned", "footer reminder must appear when terminal and abandoned rows exist")
	assert.Contains(t, v, "Check the synchronizer", "footer reminder must contain synchronizer note")
}

// TestDetailModel_FooterReminderAbsentWhenNotTerminal verifies no reminder
// when the command is not in a terminal state.
func TestDetailModel_FooterReminderAbsentWhenNotTerminal(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-running",
		State:     "Canceling",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-aaa", ResourceLabel: "res-a", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	abandoned := map[string]bool{"ksuid-aaa": true}
	dm = dm.SetCommand(c, r, "◉", now, abandoned)

	v := plain(dm.View(40, false))
	assert.NotContains(t, v, "in-progress updates were abandoned", "footer reminder must NOT appear for non-terminal command")
}

// TestDetailModel_FooterReminderAbsentWhenNoAbandoned verifies no reminder
// when there are no abandoned rows, even if the command is terminal.
func TestDetailModel_FooterReminderAbsentWhenNoAbandoned(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	v := plain(dm.View(30, false))
	assert.NotContains(t, v, "in-progress updates were abandoned", "no footer reminder when abandoned set is empty")
}

// TestModel_AbandonedResources_PlumbedToDetail verifies that Options.AbandonedResources
// is threaded through to the detail view: after drilling in, "Abandoned" is visible.
func TestModel_AbandonedResources_PlumbedToDetail(t *testing.T) {
	cmd := apimodel.Command{
		CommandID: "cmd-plumb",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "Canceled",
		StartTs:   time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		EndTs:     time.Date(2026, 7, 17, 10, 1, 0, 0, time.UTC),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-plumb", ResourceLabel: "res-plumb", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	fc := &fakeClient{resp: &apimodel.ListCommandStatusResponse{
		Commands: []apimodel.Command{cmd},
	}}
	now := time.Date(2026, 7, 17, 10, 2, 0, 0, time.UTC)
	m := New(theme.New("formae"), fc, Options{
		MaxResults:         10,
		PollInterval:       time.Hour,
		Now:                func() time.Time { return now },
		FocusCommandID:     "cmd-plumb",
		AbandonedResources: []string{"ksuid-plumb"},
	})

	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm, _ = mm.Update(commandsMsg{commands: []apimodel.Command{cmd}})

	// FocusCommandID should auto-drill-in
	got := mm.(Model)
	assert.Equal(t, viewDetail, got.view, "FocusCommandID should auto drill-in to detail")

	v := plain(got.View())
	assert.Contains(t, v, "Abandoned", "plumbed AbandonedResources must render 'Abandoned' in detail view")
}

// TestDetailModel_GoldenAbandoned golden test: command in Canceled state,
// 2 abandoned + 3 canceled (non-abandoned) resource updates.
func TestDetailModel_GoldenAbandoned(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 32)

	c := apimodel.Command{
		CommandID: "cmd-golden-ab",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "Canceled",
		StartTs:   time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		EndTs:     time.Date(2026, 7, 17, 10, 1, 30, 0, time.UTC),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ab-001", ResourceLabel: "res-abandoned-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled", Duration: 5000},
			{ResourceID: "ab-002", ResourceLabel: "res-abandoned-2", ResourceType: "AWS::EC2::Instance", StackName: "prod", Operation: "create", State: "Canceled", Duration: 8000},
			{ResourceID: "ok-001", ResourceLabel: "res-canceled-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ok-002", ResourceLabel: "res-canceled-2", ResourceType: "AWS::EC2::Instance", StackName: "prod", Operation: "delete", State: "Canceled"},
			{ResourceID: "ok-003", ResourceLabel: "res-canceled-3", ResourceType: "AWS::RDS::DBInstance", StackName: "prod", Operation: "delete", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 17, 10, 2, 0, 0, time.UTC)
	abandoned := map[string]bool{"ab-001": true, "ab-002": true}
	dm = dm.SetCommand(c, r, "◉", now, abandoned)

	tuitest.RequireGolden(t, []byte(dm.View(32, false)))
}

// TestDetailModel_CancelStateLabelsPreserved verifies that the existing
// finishing/canceled semantics still work with an empty abandoned set.
func TestDetailModel_CancelStateLabelsPreserved(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-cancel-preserved",
		State:     "Canceling",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "running", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
			{ResourceID: "r2", ResourceLabel: "canceled", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	dm = dm.SetCommand(c, r, "◉", now, nil)

	v := plain(dm.View(40, false))
	assert.Contains(t, v, "finishing", "in-progress row on canceling command must still show 'finishing'")
	assert.Contains(t, v, "canceled", "canceled row must still show 'canceled'")
	assert.NotContains(t, v, "Abandoned", "no Abandoned label without abandoned set")
}

// TestDetailModel_AbandonedCountInFooter verifies the exact count N in the reminder.
func TestDetailModel_AbandonedCountInFooter(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)

	c := apimodel.Command{
		CommandID: "cmd-count",
		State:     "Canceled",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ab-1", ResourceLabel: "res-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ab-2", ResourceLabel: "res-2", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ok-1", ResourceLabel: "res-3", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	r := row{cmd: c, counts: commandCounts(c), health: commandHealth(c, commandCounts(c))}
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	abandoned := map[string]bool{"ab-1": true, "ab-2": true}
	dm = dm.SetCommand(c, r, "◉", now, abandoned)

	v := plain(dm.View(40, false))
	assert.Contains(t, v, "2 in-progress updates were abandoned", "count must be exact")
}

// TestDetailModel_UpdateExistingTests ensures the old tests still work by
// calling SetCommand with nil abandoned (backward compat).
func TestDetailModel_NilAbandonedSet(t *testing.T) {
	th := theme.New("formae")
	dm := newDetailModel(th, 100, 40)
	c := makeTerminalCmd()
	r := makeTerminalRow()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	// Must not panic
	dm = dm.SetCommand(c, r, "◉", now, nil)
	v := plain(dm.View(30, false))
	assert.Contains(t, v, "my-bucket")
}

// TestModel_ExitWhenDoneWithAbandoned verifies ExitWhenDone + AbandonedResources
// together: when all commands terminal, model quits.
func TestModel_ExitWhenDoneWithAbandoned(t *testing.T) {
	cmd := apimodel.Command{
		CommandID: "cmd-exit",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "Canceled",
		StartTs:   time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		EndTs:     time.Date(2026, 7, 17, 10, 1, 0, 0, time.UTC),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-x", ResourceLabel: "res-x", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	fc := &fakeClient{resp: &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{cmd}}}
	now := time.Date(2026, 7, 17, 10, 2, 0, 0, time.UTC)
	m := New(theme.New("formae"), fc, Options{
		MaxResults:         10,
		PollInterval:       time.Hour,
		Now:                func() time.Time { return now },
		ExitWhenDone:       true,
		AbandonedResources: []string{"ksuid-x"},
	})

	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// All terminal + ExitWhenDone schedules a DEFERRED quit (grace period so the
	// completed bar is visible); the exitNowMsg then triggers the real quit.
	mm, schedCmd := mm.Update(commandsMsg{commands: []apimodel.Command{cmd}})
	assert.NotNil(t, schedCmd, "ExitWhenDone + all terminal + abandoned → deferred quit scheduled")

	_, quitCmd := mm.Update(exitNowMsg{})
	assert.NotNil(t, quitCmd, "exitNowMsg → quit")
	assert.Equal(t, tea.Quit(), quitCmd())
}
