// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
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
