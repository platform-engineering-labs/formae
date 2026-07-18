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

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// newCancelTestApp returns a minimal *app.App for use in cancel tests.
func newCancelTestApp() *app.App {
	return &app.App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{},
		},
	}
}

// TestCancelForHumans_PreFetch_PerID_D6 verifies that D6 frozen-set logic fires
// per-ID for each non-terminal command, never passing the original query.
func TestCancelForHumans_PreFetch_PerID_D6(t *testing.T) {
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

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-10 * time.Second)},
				{CommandID: "cmd-b", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	var cancelQueries []string
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelQueries = append(cancelQueries, query)
		return &apimodel.CancelCommandResponse{
			CommandIDs: []string{query[3:]}, // strip "id:"
		}, nil
	}

	opts := &CancelOptions{
		Query:          "some-original-query",
		Force:          false,
		Yes:            true, // skip confirm
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	// Must use per-ID queries, not the original query
	assert.Equal(t, 2, len(cancelQueries), "expected exactly 2 cancel calls")
	assert.Contains(t, cancelQueries, "id:cmd-a")
	assert.Contains(t, cancelQueries, "id:cmd-b")
	// Original query must NOT appear
	for _, q := range cancelQueries {
		assert.NotEqual(t, "some-original-query", q)
	}
}

// TestCancelForHumans_ForceDeclined_NoCalls verifies that declining force confirm → 0 cancel calls.
func TestCancelForHumans_ForceDeclined_NoCalls(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origIsInteractive := isInteractive
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		isInteractive = origIsInteractive
		confirmForceCancel = origConfirm
	})

	isTerminal = func(w io.Writer) bool { return true }
	isInteractive = func() bool { return true } // H1: must be interactive to reach confirmForceCancel

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-10 * time.Second)},
			},
		}, nil, nil
	}

	cancelCalled := false
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelCalled = true
		return &apimodel.CancelCommandResponse{}, nil
	}

	// User declines force confirmation
	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
		return false, nil
	}

	opts := &CancelOptions{
		Query:          "stack:test",
		Force:          true,
		Yes:            false, // force confirmation is required
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.False(t, cancelCalled, "cancel must not be called when user declines force confirm")
}

// TestCancelForHumans_EmptyPreFetch_NoCalls verifies that empty pre-fetch → no cancel calls.
func TestCancelForHumans_EmptyPreFetch_NoCalls(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
	})

	isTerminal = func(w io.Writer) bool { return true }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{},
		}, nil, nil
	}

	cancelCalled := false
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelCalled = true
		return &apimodel.CancelCommandResponse{}, nil
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          false,
		Yes:            true,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.False(t, cancelCalled, "cancel must not be called when no commands in pre-fetch")
}

// TestCancelForHumans_NonTTY_LegacyPath verifies that on non-TTY output the
// legacy flow runs unchanged: a single cancel call with the user's original
// query and no pre-fetch.
func TestCancelForHumans_NonTTY_LegacyPath(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
	})

	isTerminal = func(w io.Writer) bool { return false }

	preFetchCalled := false
	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		preFetchCalled = true
		return &apimodel.ListCommandStatusResponse{}, nil, nil
	}

	var cancelQueries []string
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelQueries = append(cancelQueries, query)
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-a"}}, nil
	}

	opts := &CancelOptions{
		Query:          "stack:test",
		Force:          false,
		Yes:            true,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.False(t, preFetchCalled, "legacy path must not pre-fetch")
	require.Equal(t, 1, len(cancelQueries), "legacy path submits exactly one cancel call")
	assert.Equal(t, "stack:test", cancelQueries[0], "legacy path uses the original query")
}

// TestForceCancelSummary_ExpectationBullets verifies that the summary string passed to
// confirmForceCancel includes per-command expectation bullets derived from
// ResourceUpdates[].State (8 Success + 2 InProgress + 5 Pending → completed/abandoned/pending).
func TestForceCancelSummary_ExpectationBullets(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origIsInteractive := isInteractive
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		isInteractive = origIsInteractive
		confirmForceCancel = origConfirm
	})

	isTerminal = func(w io.Writer) bool { return true }
	isInteractive = func() bool { return true } // H1: must be interactive to reach confirmForceCancel

	// Pre-fetched command: 8 Success + 2 InProgress + 5 Pending ResourceUpdates.
	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{
					CommandID:       "cmd-abc123",
					Command:         "apply",
					Mode:            "reconcile",
					State:           "InProgress",
					StartTs:         time.Now().Add(-42 * time.Second),
					ResourceUpdates: makeUpdates(8, "Success", 2, "InProgress", 5, "Pending"),
				},
			},
		}, nil, nil
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-abc123"}}, nil
	}

	var capturedSummary string
	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
		capturedSummary = summary
		return false, nil // decline so no cancel call is made
	}

	opts := &CancelOptions{
		Query:          "id:cmd-abc123",
		Force:          true,
		Yes:            false, // trigger confirmation
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	plain := stripANSI(capturedSummary)

	// Must contain the command header line.
	assert.Contains(t, plain, "cmd-abc123", "summary must contain the command ID")
	assert.Contains(t, plain, "apply", "summary must contain the command name")
	assert.Contains(t, plain, "reconcile", "summary must contain the mode")

	// Must contain the expectation bullet with "(will be abandoned)" for the 2 InProgress.
	assert.Contains(t, plain, "8 completed", "summary must show completed count")
	assert.Contains(t, plain, "2 in progress (will be abandoned)", "summary must show in-progress-will-be-abandoned count")
	assert.Contains(t, plain, "5 pending (will cancel)", "summary must show pending-will-cancel count")
}

// TestCancelForHumans_TerminalFiltered verifies that terminal commands (Success, Failed, Canceled)
// in pre-fetch are skipped; only InProgress gets canceled.
func TestCancelForHumans_TerminalFiltered(t *testing.T) {
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

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "success-cmd", State: "Success"},
				{CommandID: "failed-cmd", State: "Failed"},
				{CommandID: "canceled-cmd", State: "Canceled"},
				{CommandID: "inprog-cmd", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	var cancelQueries []string
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelQueries = append(cancelQueries, query)
		return &apimodel.CancelCommandResponse{
			CommandIDs: []string{"inprog-cmd"},
		}, nil
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          false,
		Yes:            true,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	require.Equal(t, 1, len(cancelQueries), "only one cancel call for the InProgress command")
	assert.Equal(t, "id:inprog-cmd", cancelQueries[0])
}
