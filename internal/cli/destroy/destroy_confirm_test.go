// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package destroy

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// destroySimulationWithChanges returns a minimal simulation that has changes required.
func destroySimulationWithChanges() *apimodel.SubmitCommandResponse {
	return &apimodel.SubmitCommandResponse{
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID:       "sim-cmd",
				ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
			},
		},
	}
}

// stubDestroyConfirmSeams saves isInteractive/runConfirm/destroyFn and registers cleanup.
func stubDestroyConfirmSeams(t *testing.T) {
	t.Helper()
	origIsInteractive := isInteractive
	origRunConfirm := runConfirm
	origDestroyFn := destroyFn
	t.Cleanup(func() {
		isInteractive = origIsInteractive
		runConfirm = origRunConfirm
		destroyFn = origDestroyFn
	})
}

// TestDestroyLegacy_NonTTY_NoYes_D8Error verifies that non-TTY without --yes
// returns the D8 error and does NOT proceed to real destroy.
func TestDestroyLegacy_NonTTY_NoYes_D8Error(t *testing.T) {
	stubDestroyConfirmSeams(t)

	destroyCallCount := 0
	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		if simulate {
			return destroySimulationWithChanges(), nil, nil
		}
		panic("real destroy must not be called in non-TTY D8 test")
	}

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called in non-TTY D8 test")
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
	}

	err := runDestroyLegacy(a, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
	assert.Contains(t, err.Error(), "--yes")
	assert.Equal(t, 1, destroyCallCount, "only simulate call, no real destroy")
}

// TestDestroyLegacy_NonTTY_WithYes_Proceeds verifies that non-TTY WITH --yes
// skips the confirm and proceeds to real destroy.
func TestDestroyLegacy_NonTTY_WithYes_Proceeds(t *testing.T) {
	stubDestroyConfirmSeams(t)

	var destroyCalls []bool
	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCalls = append(destroyCalls, simulate)
		if simulate {
			return destroySimulationWithChanges(), nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd"}, nil, nil
	}

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called when --yes is set")
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            true,
	}

	err := runDestroyLegacy(a, opts)
	require.NoError(t, err)
	assert.Equal(t, []bool{true, false}, destroyCalls, "simulate then real destroy")
}

// TestDestroyLegacy_TTY_Declined_Aborts verifies that TTY declined → does NOT
// proceed to real destroy.
func TestDestroyLegacy_TTY_Declined_Aborts(t *testing.T) {
	stubDestroyConfirmSeams(t)

	destroyCallCount := 0
	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		if simulate {
			return destroySimulationWithChanges(), nil, nil
		}
		panic("real destroy must not be called when user declines")
	}

	isInteractive = func() bool { return true }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		return false, nil // user declined
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
	}

	err := runDestroyLegacy(a, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, destroyCallCount, "only simulate call — real destroy not submitted after decline")
}

// TestDestroyLegacy_TTY_Confirmed_Cascade_ElevatesOnDependents asserts that when
// the legacy path's simulation shows cascade deletes and the user confirms via
// the interactive prompt, the real destroy call receives OnDependentsCascade —
// not the default abort. Without this elevation the server gate returns 409
// even after an explicit interactive confirmation.
func TestDestroyLegacy_TTY_Confirmed_Cascade_ElevatesOnDependents(t *testing.T) {
	stubDestroyConfirmSeams(t)
	origLaunchWatch := launchWatch
	origLegacyWidth := legacyWidth
	t.Cleanup(func() {
		launchWatch = origLaunchWatch
		legacyWidth = origLegacyWidth
	})

	legacyWidth = func(w io.Writer) int { return 100 }
	launchWatch = func(a *app.App, commandID string) (bool, error) { return true, nil }

	var realOnDependents OnDependents
	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{Simulation: cascadeSimulation()}, nil, nil
		}
		realOnDependents = opts.OnDependents
		return &apimodel.SubmitCommandResponse{CommandID: "legacy-cascade-cmd"}, nil, nil
	}

	isInteractive = func() bool { return true }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		return true, nil // user confirmed
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
	}

	err := runDestroyLegacy(a, opts)
	require.NoError(t, err)
	assert.Equal(t, OnDependentsCascade, realOnDependents,
		"legacy interactive confirm with cascades must pass OnDependentsCascade to the real destroy call")
}
