// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apply

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
// mirroring the adapter in internal/cli/status/watch_e2e_test.go.
type e2eClientAdapter struct{ c *api.Client }

func (a e2eClientAdapter) GetCommandsStatus(query string, n int, _ bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	resp, err := a.c.GetFormaCommandsStatus(query, "e2e-client", n)
	return resp, nil, err
}

// e2eCommand builds a ListCommandStatusResponse for the apply command in the
// given state, with one create resource update in the given update state.
func e2eCommand(cmdState, updState string) apitest.WrappedListResponse {
	return apitest.WrappedListResponse{
		ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{
					CommandID: "cmd-e2e-apply",
					Command:   "apply",
					Mode:      "reconcile",
					State:     cmdState,
					StartTs:   time.Now().Add(-5 * time.Second),
					ResourceUpdates: []apimodel.ResourceUpdate{
						{
							ResourceLabel: "my-bucket",
							ResourceType:  "AWS::S3::Bucket",
							Operation:     "create",
							State:         updState,
						},
					},
				},
			},
		},
	}
}

// TestApplyTUI_EndToEnd is the layer-3 E2E for the interactive apply flow:
// FakeMetastructure → real api.Server → real api.Client. The apply seam posts
// through the real HTTP stack (two POST /commands: simulate + real), simview is
// stubbed to Confirmed, and the watch seam drives the REAL statuswatch model
// via teatest against the same server until ExitWhenDone auto-quits on the
// terminal poll.
func TestApplyTUI_EndToEnd(t *testing.T) {
	simulateResp := &apimodel.SubmitCommandResponse{
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID: "cmd-e2e-sim",
				Command:   "apply",
				Mode:      "reconcile",
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						ResourceLabel: "my-bucket",
						ResourceType:  "AWS::S3::Bucket",
						Operation:     "create",
					},
				},
			},
		},
	}
	submitResp := &apimodel.SubmitCommandResponse{
		CommandID: "cmd-e2e-apply",
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID: "cmd-e2e-apply",
				Command:   "apply",
				Mode:      "reconcile",
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						ResourceLabel: "my-bucket",
						ResourceType:  "AWS::S3::Bucket",
						Operation:     "create",
					},
				},
			},
		},
	}

	fake := &apitest.FakeMetastructure{
		ApplyResponses: []apitest.WrappedCommandResponse{
			{SubmitCommandResponse: simulateResp},
			{SubmitCommandResponse: submitResp},
		},
		// Status polls progress InProgress → InProgress → Success (sticky tail).
		ListResponses: []apitest.WrappedListResponse{
			e2eCommand("InProgress", "InProgress"),
			e2eCommand("InProgress", "InProgress"),
			e2eCommand("Success", "Success"),
		},
	}

	// Real API server wrapping the fake metastructure; real client against it.
	// Port 80 + http URL passes the httptest URL through formatEndpoint unmodified.
	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	client := api.NewClient(pkgmodel.APIConfig{URL: baseURL, Port: 80}, nil, nil)

	// Stub the seams: applyFn posts through the REAL HTTP stack; simview is
	// stubbed to Confirmed; launchWatch runs the REAL statuswatch model via
	// teatest, waiting for the ExitWhenDone auto-quit.
	origApplyFn := applyFn
	origIsTerminal := isTerminal
	origLaunchSimView := launchSimView
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		applyFn = origApplyFn
		isTerminal = origIsTerminal
		launchSimView = origLaunchSimView
		launchWatch = origLaunchWatch
	})

	isTerminal = func(w io.Writer) bool { return true }

	var postedSimulateFlags []bool
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		postedSimulateFlags = append(postedSimulateFlags, simulate)
		resp, err := client.ApplyForma(&pkgmodel.Forma{}, opts.Mode, simulate, "e2e-client", opts.Force)
		return resp, nil, err
	}

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		assert.True(t, sim.ChangesRequired)
		assert.Equal(t, simview.KindApply, opts.Kind)
		assert.Equal(t, "reconcile", opts.Mode)
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
		// The real HTTP polls must surface the command before it finishes.
		tuitest.WaitForContains(t, tm, "cmd-e2e-apply")
		// ExitWhenDone quits automatically once the poll reports Success.
		tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
		final := tm.FinalModel(t)
		return final.(statuswatch.Model).Finished(), nil
	}

	a := &app.App{Config: &pkgmodel.Config{}}
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	// Full round trip: two POST /commands (simulate then real) — the fake's
	// FIFO ApplyResponses queue must be fully consumed.
	require.Equal(t, []bool{true, false}, postedSimulateFlags)
	assert.Empty(t, fake.ApplyResponses, "both queued apply responses must be consumed via HTTP")
	assert.True(t, simViewCalled, "simview must be launched on the interactive path")
	assert.Equal(t, "cmd-e2e-apply", watchedCommandID, "watch must focus the submitted command")
}
