// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"fmt"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// TestCancelWatch_Single_LaunchCancelWatchOptions verifies that a single canceled
// command with ForceCanceled entries causes launchCancelWatch to be called with
// FocusCommandID, ExitWhenDone=true, and the normalized abandoned ksuids.
func TestCancelWatch_Single_LaunchCancelWatchOptions(t *testing.T) {
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
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{
					CommandID: "cmd-single",
					State:     "InProgress",
					StartTs:   time.Now().Add(-10 * time.Second),
					ResourceUpdates: []apimodel.ResourceUpdate{
						{ResourceID: "ksuid-force-1", ResourceLabel: "res-1", ResourceType: "AWS::S3::Bucket", StackName: "prod", Operation: "create", State: "InProgress"},
						{ResourceID: "ksuid-force-2", ResourceLabel: "res-2", ResourceType: "AWS::EC2::Instance", StackName: "prod", Operation: "create", State: "Pending"},
					},
				},
			},
		}, nil, nil
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return &apimodel.CancelCommandResponse{
			CommandIDs: []string{"cmd-single"},
			Forced:     true,
			ResourceUpdateStates: map[string]apimodel.CancelResourceState{
				"formae://ksuid-force-1#": {State: "Canceled", CommandID: "cmd-single", ForceCanceled: true},
				"formae://ksuid-force-2#": {State: "Canceled", CommandID: "cmd-single", ForceCanceled: false},
			},
		}, nil
	}

	var capturedOpts statuswatch.Options
	launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
		capturedOpts = opts
		return nil
	}

	opts := &CancelOptions{
		Query:          "id:cmd-single",
		Force:          true,
		Yes:            true,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.Equal(t, "id:cmd-single", capturedOpts.Query, "single command: query must be id:<cmdID>")
	assert.Equal(t, "cmd-single", capturedOpts.FocusCommandID, "single command: FocusCommandID must be set")
	assert.True(t, capturedOpts.ExitWhenDone, "ExitWhenDone must be true")

	// Only ksuid-force-1 has ForceCanceled=true; ksuid-force-2 does not.
	sort.Strings(capturedOpts.AbandonedResources)
	assert.Equal(t, []string{"ksuid-force-1"}, capturedOpts.AbandonedResources, "only ForceCanceled=true entries must appear")
}

// TestCancelWatch_Multi_LaunchCancelWatchOptions verifies multiple canceled commands
// produce a Query of "id:a id:b" form with no FocusCommandID.
func TestCancelWatch_Multi_LaunchCancelWatchOptions(t *testing.T) {
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
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-alpha", State: "InProgress", StartTs: time.Now().Add(-10 * time.Second)},
				{CommandID: "cmd-beta", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	callN := 0
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		callN++
		var cmdID string
		if callN == 1 {
			cmdID = "cmd-alpha"
		} else {
			cmdID = "cmd-beta"
		}
		return &apimodel.CancelCommandResponse{
			CommandIDs: []string{cmdID},
			Forced:     true,
			ResourceUpdateStates: map[string]apimodel.CancelResourceState{
				"formae://ksuid-" + cmdID + "#": {State: "Canceled", CommandID: cmdID, ForceCanceled: true},
			},
		}, nil
	}

	var capturedOpts statuswatch.Options
	launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
		capturedOpts = opts
		return nil
	}

	opts := &CancelOptions{
		Query:          "stack:test",
		Force:          true,
		Yes:            true,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	// Multi: no FocusCommandID; query contains both id: terms
	assert.Empty(t, capturedOpts.FocusCommandID, "multi: FocusCommandID must be empty")
	assert.True(t, capturedOpts.ExitWhenDone)
	assert.Contains(t, capturedOpts.Query, "id:cmd-alpha")
	assert.Contains(t, capturedOpts.Query, "id:cmd-beta")
	// MaxResults must cover every canceled command so the watch doesn't stop at
	// the default page size (10) and auto-exit while later commands still run.
	assert.Equal(t, 2, capturedOpts.MaxResults, "multi: MaxResults must cover all canceled commands")

	sort.Strings(capturedOpts.AbandonedResources)
	assert.Equal(t, []string{"ksuid-cmd-alpha", "ksuid-cmd-beta"}, capturedOpts.AbandonedResources, "both force-canceled ksuids must be in abandoned list")
}

// TestCancelWatch_NoForce_EmptyAbandoned verifies that when not --force,
// AbandonedResources is empty.
func TestCancelWatch_NoForce_EmptyAbandoned(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origLaunch := launchCancelWatch
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		launchCancelWatch = origLaunch
	})

	isTerminal = func(w io.Writer) bool { return true }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-noforce", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return &apimodel.CancelCommandResponse{
			CommandIDs: []string{"cmd-noforce"},
			ResourceUpdateStates: map[string]apimodel.CancelResourceState{
				"formae://ksuid-x#": {State: "Canceled", CommandID: "cmd-noforce", ForceCanceled: false},
			},
		}, nil
	}

	var capturedOpts statuswatch.Options
	launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
		capturedOpts = opts
		return nil
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          false,
		Yes:            true,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.Empty(t, capturedOpts.AbandonedResources, "no --force: AbandonedResources must be empty")
	assert.True(t, capturedOpts.ExitWhenDone)
}

// TestCancelWatch_ManyCommands_NoAutoExit verifies that when more commands are
// canceled than the datastore page cap (cancelWatchPageLimit), the watch does
// NOT set ExitWhenDone (it would auto-exit while off-page commands still run).
func TestCancelWatch_ManyCommands_NoAutoExit(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origLaunch := launchCancelWatch
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		launchCancelWatch = origLaunch
	})

	isTerminal = func(w io.Writer) bool { return true }

	const n = cancelWatchPageLimit + 1
	cmds := make([]apimodel.Command, n)
	for i := 0; i < n; i++ {
		cmds[i] = apimodel.Command{CommandID: fmt.Sprintf("cmd-%02d", i), State: "InProgress", StartTs: time.Now().Add(-time.Second)}
	}
	getCommandsStatusFn = func(a *app.App, query string, cnt int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{Commands: cmds}, nil, nil
	}
	callN := 0
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		id := fmt.Sprintf("cmd-%02d", callN)
		callN++
		return &apimodel.CancelCommandResponse{CommandIDs: []string{id}}, nil
	}

	var capturedOpts statuswatch.Options
	launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
		capturedOpts = opts
		return nil
	}

	opts := &CancelOptions{Query: "stack:test", Yes: true, OutputConsumer: printer.ConsumerHuman}
	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.Equal(t, n, capturedOpts.MaxResults, "MaxResults must cover every canceled command")
	assert.False(t, capturedOpts.ExitWhenDone, "must not auto-exit when more commands than the page cap are canceled")
}

// TestCancelWatch_NonInteractiveStdin_FireAndForget verifies that when stdout is
// a TTY but stdin is not interactive (e.g. `formae cancel --yes </dev/null`), the
// cancel is submitted but the watch TUI is NOT launched (it would hang/fail with
// no interactive stdin). The command stays fire-and-forget.
func TestCancelWatch_NonInteractiveStdin_FireAndForget(t *testing.T) {
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origIsTerminal := isTerminal
	origIsInteractive := isInteractive
	origLaunch := launchCancelWatch
	t.Cleanup(func() {
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		isTerminal = origIsTerminal
		isInteractive = origIsInteractive
		launchCancelWatch = origLaunch
	})

	isTerminal = func(w io.Writer) bool { return true }    // stdout IS a TTY
	isInteractive = func() bool { return false }           // stdin is NOT interactive

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-nonint", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-nonint"}}, nil
	}

	watchCalled := false
	launchCancelWatch = func(a *app.App, th *theme.Theme, opts statuswatch.Options) error {
		watchCalled = true
		return nil
	}

	opts := &CancelOptions{Query: "", Force: false, Yes: true, OutputConsumer: printer.ConsumerHuman}
	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.False(t, watchCalled, "watch TUI must NOT launch when stdin is not interactive")
}
