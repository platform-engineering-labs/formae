// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func newTestModel(t *testing.T, resp *apimodel.ListCommandStatusResponse) (Model, *fakeClient) {
	t.Helper()
	fc := &fakeClient{resp: resp}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m := New(theme.New("formae"), fc, Options{
		MaxResults:   10,
		PollInterval: time.Hour, // never fires during tests
		Now:          func() time.Time { return now },
	})
	return m, fc
}

func respFix(ids ...string) *apimodel.ListCommandStatusResponse {
	r := &apimodel.ListCommandStatusResponse{}
	for _, id := range ids {
		r.Commands = append(r.Commands, apimodel.Command{
			CommandID: id, Command: "apply", Mode: "reconcile", State: "Success",
			StartTs:         time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC),
			EndTs:           time.Date(2026, 7, 16, 11, 1, 0, 0, time.UTC),
			ResourceUpdates: []apimodel.ResourceUpdate{{State: "Success", ResourceLabel: "bucket-" + id, ResourceType: "AWS::S3::Bucket", Operation: "create"}},
		})
	}
	return r
}

// buildReordered returns []apimodel.Command in the order cmd-b, cmd-a, cmd-c.
func buildReordered() []apimodel.Command {
	startTs := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	endTs := time.Date(2026, 7, 16, 11, 1, 0, 0, time.UTC)
	mk := func(id string) apimodel.Command {
		return apimodel.Command{
			CommandID: id, Command: "apply", Mode: "reconcile", State: "Success",
			StartTs: startTs, EndTs: endTs,
			ResourceUpdates: []apimodel.ResourceUpdate{{State: "Success", ResourceLabel: "bucket-" + id, ResourceType: "AWS::S3::Bucket", Operation: "create"}},
		}
	}
	return []apimodel.Command{mk("cmd-b"), mk("cmd-a"), mk("cmd-c")}
}

func TestModel_InitFetchesAndRenders(t *testing.T) {
	m, _ := newTestModel(t, respFix("cmd-one", "cmd-two"))
	tm := tuitest.Run(t, m, 100, 24)
	tuitest.WaitForContains(t, tm, "cmd-one")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestModel_CursorAnchoredToCommandAcrossRefresh(t *testing.T) {
	// direct Update() testing — no teatest needed for pure message flows
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(commandsMsg{commands: respFix("cmd-a", "cmd-b", "cmd-c").Commands})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor → cmd-b

	// refresh delivers the same commands in a new order (cmd-b now first)
	mm, _ = mm.Update(commandsMsg{commands: buildReordered()})

	got := mm.(Model)
	assert.Equal(t, "cmd-b", got.multi.rows[got.multi.cursor].cmd.CommandID, "cursor follows the command, not the index")
}

func TestModel_QueryApplyTriggersRefetch(t *testing.T) {
	m, fc := newTestModel(t, respFix("cmd-one"))
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "state:InProgress" {
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	var cmd tea.Cmd
	_, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	cmd() // execute the returned fetch command
	assert.Equal(t, "state:InProgress", fc.queries[len(fc.queries)-1])
}

func TestModel_ExitWhenDone(t *testing.T) {
	fc := &fakeClient{resp: respFix("cmd-done")}
	m := New(theme.New("formae"), fc, Options{MaxResults: 10, PollInterval: time.Hour, ExitWhenDone: true,
		Now: func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }})
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_, cmd := mm.Update(commandsMsg{commands: fc.resp.Commands})
	require.NotNil(t, cmd, "all commands terminal + ExitWhenDone → quit")
	assert.Equal(t, tea.Quit(), cmd())
}

func TestModel_ErrorBanner(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	mm, _ = mm.Update(commandsMsg{err: errors.New("agent unreachable")})
	assert.Contains(t, plain(mm.(Model).View()), "agent unreachable")
}

func TestModel_ViewLineCountMatchesHeight(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	height := 20
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	mm, _ = mm.Update(commandsMsg{commands: respFix("cmd-one", "cmd-two").Commands})
	view := mm.(Model).View()
	lines := strings.Split(view, "\n")
	assert.Equal(t, height, len(lines), "View must fill exactly the terminal height")
}

func TestModel_GoldenMultiView(t *testing.T) {
	m, _ := newTestModel(t, nil)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	mm, _ = mm.Update(commandsMsg{commands: respFix("cmd-one", "cmd-two").Commands})
	tuitest.RequireGolden(t, []byte(mm.(Model).View()))
}
