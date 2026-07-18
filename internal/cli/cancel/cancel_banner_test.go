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
)

// TestCancelInteractive_H1_NonInteractive_D8Error verifies H1: on the interactive
// cancel path (isTerminal=true), when opts.Force=true, opts.Yes=false and
// isInteractive()=false, the D8 error is returned and confirmForceCancel is NOT
// called.
func TestCancelInteractive_H1_NonInteractive_D8Error(t *testing.T) {
	origIsInteractive := isInteractive
	origIsTerminal := isTerminal
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		isInteractive = origIsInteractive
		isTerminal = origIsTerminal
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		confirmForceCancel = origConfirm
	})

	isTerminal = func(w io.Writer) bool { return true }
	isInteractive = func() bool { return false }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		panic("cancelCommandFn must not be called in H1 D8 test")
	}

	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
		panic("confirmForceCancel must not be called when isInteractive()=false")
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          true,
		Yes:            false,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
	assert.Contains(t, err.Error(), "--yes")
}

// TestCancelInteractive_H1_Interactive_ReachesConfirm verifies H1: when
// isInteractive()=true, confirmForceCancel IS reached (and proceed on true).
func TestCancelInteractive_H1_Interactive_ReachesConfirm(t *testing.T) {
	origIsInteractive := isInteractive
	origIsTerminal := isTerminal
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		isInteractive = origIsInteractive
		isTerminal = origIsTerminal
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		confirmForceCancel = origConfirm
	})

	isTerminal = func(w io.Writer) bool { return true }
	isInteractive = func() bool { return true }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	cancelCalled := false
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelCalled = true
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-a"}}, nil
	}

	confirmReached := false
	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
		confirmReached = true
		return true, nil // user confirms
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          true,
		Yes:            false,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)
	assert.True(t, confirmReached, "confirmForceCancel must be reached when isInteractive()=true")
	assert.True(t, cancelCalled, "cancel must proceed after confirmation")
}

// TestCancel_BannerNotEmittedBeforeConfirmOrTUI verifies H2: on the interactive
// TTY path (isTerminal=true, force=true, yes=false, isInteractive=true),
// the banner is NOT printed before the huh confirmForceCancel prompt.
func TestCancel_BannerNotEmittedBeforeConfirmOrTUI(t *testing.T) {
	origPrintBanner := printBanner
	origIsInteractive := isInteractive
	origIsTerminal := isTerminal
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		printBanner = origPrintBanner
		isInteractive = origIsInteractive
		isTerminal = origIsTerminal
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		confirmForceCancel = origConfirm
	})

	bannerCalled := false
	printBanner = func(a *app.App) {
		bannerCalled = true
	}

	isTerminal = func(w io.Writer) bool { return true }
	isInteractive = func() bool { return true }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-a"}}, nil
	}

	// Confirm prompt (the interactive huh prompt — the alt-screen UI).
	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
		return false, nil // decline so we don't proceed
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          true,
		Yes:            false, // triggers confirmForceCancel (interactive path)
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.False(t, bannerCalled, "banner must NOT be printed before the huh confirmForceCancel prompt")
}

// TestCancel_BannerEmittedOnYesPath verifies H2: on the --yes path (no interactive
// UI), the banner IS printed (print-and-exit semantics).
func TestCancel_BannerEmittedOnYesPath(t *testing.T) {
	origPrintBanner := printBanner
	origIsTerminal := isTerminal
	origGetStatus := getCommandsStatusFn
	origCancel := cancelCommandFn
	origConfirm := confirmForceCancel
	t.Cleanup(func() {
		printBanner = origPrintBanner
		isTerminal = origIsTerminal
		getCommandsStatusFn = origGetStatus
		cancelCommandFn = origCancel
		confirmForceCancel = origConfirm
	})

	bannerCalled := false
	printBanner = func(a *app.App) {
		bannerCalled = true
	}

	isTerminal = func(w io.Writer) bool { return true }

	getCommandsStatusFn = func(a *app.App, query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{
				{CommandID: "cmd-a", State: "InProgress", StartTs: time.Now().Add(-5 * time.Second)},
			},
		}, nil, nil
	}

	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-a"}}, nil
	}

	confirmForceCancel = func(th *theme.Theme, summary string) (bool, error) {
		panic("confirmForceCancel must not be called when --yes is set")
	}

	opts := &CancelOptions{
		Query:          "",
		Force:          true,
		Yes:            true, // --yes: skip confirm, print-and-exit
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelForHumans(newCancelTestApp(), opts)
	require.NoError(t, err)

	assert.True(t, bannerCalled, "banner MUST be printed on --yes (print-and-exit) path")
}
