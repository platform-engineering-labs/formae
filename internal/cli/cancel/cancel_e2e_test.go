// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"io"
	"sort"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestCancelE2E_PerIDSubmit verifies that the human cancel flow submits one
// cancel request per non-terminal command ID discovered in the pre-fetch,
// not using the original query string — flowing through the real HTTP stack.
func TestCancelE2E_PerIDSubmit(t *testing.T) {
	fake := &apitest.FakeMetastructure{
		// Pre-fetch returns 2 in-progress commands
		ListResponses: []apitest.WrappedListResponse{
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-10 * time.Second)},
						{CommandID: "cmd-b", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
					},
				},
			},
		},
		// Cancel responses per ID (consumed in FIFO order)
		CancelResponses: []apitest.WrappedCancelResponse{
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{"cmd-a"},
					ResourceUpdateStates: map[string]apimodel.CancelResourceState{
						"formae://r1#": {State: "Canceled", CommandID: "cmd-a"},
					},
				},
			},
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{"cmd-b"},
					ResourceUpdateStates: map[string]apimodel.CancelResourceState{
						"formae://r2#": {State: "Canceled", CommandID: "cmd-b"},
					},
				},
			},
		},
	}

	// Real API server wrapping the fake metastructure; real client against it.
	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	client := api.NewClient(pkgmodel.APIConfig{URL: baseURL, Port: 80}, nil, nil)

	// Stub seams to route through the real HTTP stack
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		confirmForceCancel = origConfirm
	})

	isTerminal = func(w io.Writer) bool { return true }
	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) { return true, nil }

	// Route through the real HTTP stack
	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		resp, err := client.GetFormaCommandsStatus(query, "e2e-client", n)
		return resp, nil, err
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return client.CancelCommands(query, force, "e2e-client")
	}

	opts := &CancelOptions{
		Query:          "stack:mystack",
		Force:          false,
		Yes:            true,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	a := &app.App{Config: &pkgmodel.Config{}}
	err := runCancelForHumans(a, opts)
	require.NoError(t, err)

	// The fake should have recorded 2 cancel calls via CancelCommandsByQuery
	assert.Equal(t, 2, len(fake.RecordedCancelQueries), "expected 2 per-ID cancel calls via HTTP")
	assert.Contains(t, fake.RecordedCancelQueries, "id:cmd-a")
	assert.Contains(t, fake.RecordedCancelQueries, "id:cmd-b")

	// Original query must never appear
	for _, q := range fake.RecordedCancelQueries {
		assert.NotEqual(t, "stack:mystack", q, "original query must not reach the server")
	}
}

// TestCancelE2E_ForceWatch_AbandonedInView verifies that cancel --force --watch
// with ForceCanceled resource entries launches the watch view with the correct
// AbandonedResources list and FocusCommandID/ExitWhenDone set.
// The abandoned ksuids are P3-normalized (ksuidFromURI) at the call site.
func TestCancelE2E_ForceWatch_AbandonedInView(t *testing.T) {
	fake := &apitest.FakeMetastructure{
		ListResponses: []apitest.WrappedListResponse{
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{
							CommandID: "cmd-force-watch",
							State:     "InProgress",
							StartTs:   time.Now().Add(-15 * time.Second),
							ResourceUpdates: []apimodel.ResourceUpdate{
								{ResourceID: "ksuid-ab-1", ResourceLabel: "bucket-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
								{ResourceID: "ksuid-ab-2", ResourceLabel: "instance-1", ResourceType: "AWS::EC2::Instance", StackName: "prod", Operation: "create", State: "Pending"},
							},
						},
					},
				},
			},
		},
		CancelResponses: []apitest.WrappedCancelResponse{
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{"cmd-force-watch"},
					Forced:     true,
					ResourceUpdateStates: map[string]apimodel.CancelResourceState{
						"formae://ksuid-ab-1#": {State: "Canceled", CommandID: "cmd-force-watch", ForceCanceled: true},
						"formae://ksuid-ab-2#": {State: "Canceled", CommandID: "cmd-force-watch", ForceCanceled: true},
					},
				},
			},
		},
	}

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	client := api.NewClient(pkgmodel.APIConfig{URL: baseURL, Port: 80}, nil, nil)

	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origConfirm := confirmForceCancel
	origLaunch := launchCancelWatch
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		confirmForceCancel = origConfirm
		launchCancelWatch = origLaunch
	})

	isTerminal = func(w io.Writer) bool { return true }
	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) { return true, nil }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		resp, err := client.GetFormaCommandsStatus(query, "e2e-client", n)
		return resp, nil, err
	}
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return client.CancelCommands(query, force, "e2e-client")
	}

	var capturedOpts statuswatch.Options
	launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
		capturedOpts = opts
		return nil
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          true,
		Yes:            true,
		Watch:          true,
		OutputConsumer: printer.ConsumerHuman,
	}

	a := &app.App{Config: &pkgmodel.Config{}}
	err := runCancelForHumans(a, opts)
	require.NoError(t, err)

	// Single command: FocusCommandID must be set
	assert.Equal(t, "cmd-force-watch", capturedOpts.FocusCommandID)
	assert.Equal(t, "id:cmd-force-watch", capturedOpts.Query)
	assert.True(t, capturedOpts.ExitWhenDone)

	// Both force-canceled resources must be in AbandonedResources (normalized ksuids)
	sort.Strings(capturedOpts.AbandonedResources)
	assert.Equal(t, []string{"ksuid-ab-1", "ksuid-ab-2"}, capturedOpts.AbandonedResources)

	// Verify that the statuswatch model with these Options actually renders "Abandoned"
	// when fed a terminal command with those resource IDs in Canceled state.
	terminalCmd := apimodel.Command{
		CommandID: "cmd-force-watch",
		Command:   "apply",
		Mode:      "reconcile",
		State:     "Canceled",
		StartTs:   time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		EndTs:     time.Date(2026, 7, 17, 10, 1, 0, 0, time.UTC),
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "ksuid-ab-1", ResourceLabel: "bucket-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "Canceled"},
			{ResourceID: "ksuid-ab-2", ResourceLabel: "instance-1", ResourceType: "AWS::EC2::Instance", StackName: "prod", Operation: "create", State: "Canceled"},
		},
	}
	fc := &fakeStatusClient{cmd: terminalCmd}
	swModel := statuswatch.New(
		(&app.App{Config: &pkgmodel.Config{}}).Theme(),
		fc,
		capturedOpts,
	)

	var swm tea.Model = swModel
	swm, _ = swm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	swm, _ = swm.Update(statuswatch.CommandsMsgForTest([]apimodel.Command{terminalCmd}))

	// FocusCommandID should auto drill-in, so we're already in detail view
	view := statuswatchPlain(swm.(statuswatch.Model).View())
	assert.Contains(t, view, "Abandoned", "statuswatch view must contain 'Abandoned' label for force-canceled resources")
	assert.Contains(t, view, "in-progress updates were abandoned", "statuswatch view must contain footer reminder")
}

// fakeStatusClient is a minimal statuswatch.Client for E2E view tests.
type fakeStatusClient struct {
	cmd apimodel.Command
}

func (f *fakeStatusClient) GetCommandsStatus(query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{f.cmd}}, nil, nil
}

func statuswatchPlain(s string) string {
	return stripANSI(s)
}
