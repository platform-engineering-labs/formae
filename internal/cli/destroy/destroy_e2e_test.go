// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package destroy

import (
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// e2eClientAdapter implements statuswatch.Client on top of the real *api.Client,
// mirroring the adapter in internal/cli/apply/apply_e2e_test.go.
type e2eClientAdapter struct{ c *api.Client }

func (a e2eClientAdapter) GetCommandsStatus(query string, n int, _ bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	resp, err := a.c.GetFormaCommandsStatus(query, "e2e-client", n)
	return resp, nil, err
}

// e2eCascadeResourceUpdates returns delete updates with one cascade row.
func e2eCascadeResourceUpdates(state string) []apimodel.ResourceUpdate {
	return []apimodel.ResourceUpdate{
		{
			ResourceLabel: "primary-db",
			ResourceType:  "AWS::RDS::DBInstance",
			Operation:     "delete",
			State:         state,
		},
		{
			ResourceLabel: "web-server",
			ResourceType:  "AWS::EC2::Instance",
			Operation:     "delete",
			State:         state,
			IsCascade:     true,
			CascadeSource: "primary-db",
		},
	}
}

// e2eDestroyCommand builds a ListCommandStatusResponse for the destroy command
// in the given state, with the cascade delete updates in the given update state.
func e2eDestroyCommand(cmdState, updState string) apitest.WrappedListResponse {
	return apitest.WrappedListResponse{
		ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{
					CommandID:       "cmd-e2e-destroy",
					Command:         "destroy",
					State:           cmdState,
					StartTs:         time.Now().Add(-5 * time.Second),
					ResourceUpdates: e2eCascadeResourceUpdates(updState),
				},
			},
		},
	}
}

// TestDestroyTUI_EndToEnd is the layer-3 E2E for the interactive destroy flow:
// FakeMetastructure → real api.Server → real api.Client. The destroy seam posts
// through the real HTTP stack (two POST /commands: simulate + real) using the
// query-based destroy, the simulate response carries cascade deletes, simview
// is stubbed to Confirmed (substituting for --on-dependents=cascade), and the
// watch seam drives the REAL statuswatch model via teatest against the same
// server until ExitWhenDone auto-quits on the terminal poll.
func TestDestroyTUI_EndToEnd(t *testing.T) {
	simulateResp := &apimodel.SubmitCommandResponse{
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID:       "cmd-e2e-sim",
				Command:         "destroy",
				ResourceUpdates: e2eCascadeResourceUpdates(""),
			},
		},
	}
	submitResp := &apimodel.SubmitCommandResponse{
		CommandID: "cmd-e2e-destroy",
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID:       "cmd-e2e-destroy",
				Command:         "destroy",
				ResourceUpdates: e2eCascadeResourceUpdates(""),
			},
		},
	}

	fake := &apitest.FakeMetastructure{
		DestroyResponses: []apitest.WrappedCommandResponse{
			{SubmitCommandResponse: simulateResp},
			{SubmitCommandResponse: submitResp},
		},
		// Status polls progress InProgress → InProgress → Success (sticky tail).
		ListResponses: []apitest.WrappedListResponse{
			e2eDestroyCommand("InProgress", "InProgress"),
			e2eDestroyCommand("InProgress", "InProgress"),
			e2eDestroyCommand("Success", "Success"),
		},
	}

	// Real API server wrapping the fake metastructure; real client against it.
	// Port 80 + http URL passes the httptest URL through formatEndpoint unmodified.
	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	client := api.NewClient(pkgmodel.APIConfig{URL: baseURL, Port: 80}, nil, nil)

	// Stub the seams: destroyFn posts through the REAL HTTP stack; simview is
	// stubbed to Confirmed; launchWatch runs the REAL statuswatch model via
	// teatest, waiting for the ExitWhenDone auto-quit.
	stubSeams(t)

	isTerminal = func(w io.Writer) bool { return true }

	var postedSimulateFlags []bool
	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		postedSimulateFlags = append(postedSimulateFlags, simulate)
		resp, err := client.DestroyByQuery(opts.Query, simulate, "e2e-client")
		return resp, nil, err
	}

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		assert.True(t, sim.ChangesRequired)
		assert.Equal(t, simview.KindDestroy, opts.Kind)
		assert.Equal(t, "query: stack:staging", opts.Source)

		cascadeRows := 0
		for _, ru := range sim.Command.ResourceUpdates {
			if ru.IsCascade {
				cascadeRows++
			}
		}
		assert.Equal(t, 1, cascadeRows, "cascade rows must survive the HTTP round trip")

		return simview.DecisionConfirmed, nil
	}

	watchedCommandID := ""
	launchWatch = func(a *app.App, commandID string) (bool, error) {
		watchedCommandID = commandID
		m := statuswatch.New(theme.New("formae"), e2eClientAdapter{client}, statuswatch.Options{
			Query:          "id:" + commandID,
			FocusCommandID: commandID,
			ExitWhenDone:   true,
			PollInterval:   50 * time.Millisecond,
		})
		tm := tuitest.Run(t, m, 100, 30)
		// The real HTTP polls must surface the command before it finishes. The ID
		// column truncates to its content width (real ksuids are longer), so match
		// a truncation-safe prefix rather than the full 15-char test ID.
		tuitest.WaitForContains(t, tm, "cmd-e2e-dest")
		// ExitWhenDone quits automatically once the poll reports Success.
		tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
		final := tm.FinalModel(t)
		return final.(statuswatch.Model).Finished(), nil
	}

	a := &app.App{Config: &pkgmodel.Config{}}
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		Query:          "stack:staging",
		OnDependents:   OnDependentsAbort, // interactive confirm substitutes for cascade
	}

	err := runDestroyForHumans(a, opts)
	require.NoError(t, err)

	// Full round trip: two POST /commands (simulate then real) — the fake's
	// FIFO DestroyResponses queue must be fully consumed.
	require.Equal(t, []bool{true, false}, postedSimulateFlags)
	assert.Empty(t, fake.DestroyResponses, "both queued destroy responses must be consumed via HTTP")
	assert.True(t, simViewCalled, "simview must be launched on the interactive path")
	assert.Equal(t, "cmd-e2e-destroy", watchedCommandID, "watch must focus the submitted command")
}

// TestDestroyTUI_EndToEnd_CascadeAbort is the abort E2E: --yes with cascade
// deletes in the simulate response and the default --on-dependents=abort must
// stop after the single simulate POST, render the styled abort panel, and
// return a non-nil error.
func TestDestroyTUI_EndToEnd_CascadeAbort(t *testing.T) {
	simulateResp := &apimodel.SubmitCommandResponse{
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID:       "cmd-e2e-sim",
				Command:         "destroy",
				ResourceUpdates: e2eCascadeResourceUpdates(""),
			},
		},
	}

	fake := &apitest.FakeMetastructure{
		DestroyResponses: []apitest.WrappedCommandResponse{
			{SubmitCommandResponse: simulateResp},
		},
	}

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	client := api.NewClient(pkgmodel.APIConfig{URL: baseURL, Port: 80}, nil, nil)

	stubSeams(t)

	isTerminal = func(w io.Writer) bool { return true }

	postCount := 0
	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		postCount++
		resp, err := client.DestroyByQuery(opts.Query, simulate, "e2e-client")
		return resp, nil, err
	}

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	watchCalled := false
	launchWatch = func(a *app.App, commandID string) (bool, error) {
		watchCalled = true
		return true, nil
	}

	a := &app.App{Config: &pkgmodel.Config{}}
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		Query:          "stack:staging",
		OnDependents:   OnDependentsAbort,
		Yes:            true,
	}

	var err error
	out := captureStdout(t, func() {
		err = runDestroyForHumans(a, opts)
	})

	require.Error(t, err, "cascade abort must exit non-zero")
	assert.Equal(t, 1, postCount, "no second POST after the cascade abort")
	assert.Empty(t, fake.DestroyResponses, "exactly the one queued simulate response is consumed")
	assert.False(t, simViewCalled, "simview must not run on the --yes path")
	assert.False(t, watchCalled, "watch must not run after abort")
	assert.Contains(t, out, "Command Aborted")
	assert.Contains(t, out, "To proceed, use --on-dependents=cascade")
}
