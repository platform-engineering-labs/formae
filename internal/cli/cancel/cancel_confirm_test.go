// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// stubCancelLegacySeams saves isInteractive/runConfirm/cancelCommandFn and registers cleanup.
func stubCancelLegacySeams(t *testing.T) {
	t.Helper()
	origIsInteractive := isInteractive
	origRunConfirm := runConfirm
	origCancelFn := cancelCommandFn
	t.Cleanup(func() {
		isInteractive = origIsInteractive
		runConfirm = origRunConfirm
		cancelCommandFn = origCancelFn
	})
}

// newLegacyCancelTestApp returns a minimal *app.App for cancel legacy tests.
func newLegacyCancelTestApp() *app.App {
	return newCancelTestApp()
}

// TestCancelLegacy_NonTTY_NoYes_D8Error verifies that non-TTY without --yes
// returns the D8 error when --force is set (the only interactive confirm path).
func TestCancelLegacy_NonTTY_NoYes_D8Error(t *testing.T) {
	stubCancelLegacySeams(t)

	cancelCalled := false
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelCalled = true
		panic("cancel must not be called in non-TTY D8 test")
	}

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called in non-TTY D8 test")
	}

	a := newLegacyCancelTestApp()
	opts := &CancelOptions{
		Query:          "",
		Force:          true, // force = triggers the confirm path
		Yes:            false,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelLegacy(a, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
	assert.Contains(t, err.Error(), "--yes")
	assert.False(t, cancelCalled, "cancel must not be called")
}

// TestCancelLegacy_NonTTY_WithYes_Proceeds verifies that non-TTY WITH --yes
// skips the force confirm and proceeds to cancel.
func TestCancelLegacy_NonTTY_WithYes_Proceeds(t *testing.T) {
	stubCancelLegacySeams(t)

	cancelCalled := false
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelCalled = true
		return &apimodel.CancelCommandResponse{CommandIDs: []string{"cmd-1"}}, nil
	}

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called when --yes is set")
	}

	a := newLegacyCancelTestApp()
	opts := &CancelOptions{
		Query:          "",
		Force:          true, // force = would trigger confirm if not --yes
		Yes:            true,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelLegacy(a, opts)
	require.NoError(t, err)
	assert.True(t, cancelCalled, "cancel must be called when --yes bypasses confirm")
}

// TestCancelLegacy_TTY_Declined_Aborts verifies that TTY force-confirm declined
// → cancel is NOT submitted.
func TestCancelLegacy_TTY_Declined_Aborts(t *testing.T) {
	stubCancelLegacySeams(t)

	cancelCalled := false
	cancelCommandFn = func(a *app.App, query string, force bool) (*apimodel.CancelCommandResponse, error) {
		cancelCalled = true
		panic("cancel must not be called when user declines")
	}

	isInteractive = func() bool { return true }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		return false, nil // user declined
	}

	a := newLegacyCancelTestApp()
	opts := &CancelOptions{
		Query:          "",
		Force:          true,
		Yes:            false,
		Watch:          false,
		OutputConsumer: printer.ConsumerHuman,
	}

	err := runCancelLegacy(a, opts)
	require.NoError(t, err)
	assert.False(t, cancelCalled, "cancel must not be called when user declines")
}
