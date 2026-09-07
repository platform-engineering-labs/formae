// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apply

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// simulationWithChanges returns a minimal simulation that has changes required.
func applySimulationWithChanges() *apimodel.SubmitCommandResponse {
	return &apimodel.SubmitCommandResponse{
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID:       "sim-cmd",
				ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
			},
		},
	}
}

// stubConfirmSeams saves isInteractive/runConfirm and registers cleanup.
func stubApplyConfirmSeams(t *testing.T) {
	t.Helper()
	origIsInteractive := isInteractive
	origRunConfirm := runConfirm
	origApplyFn := applyFn
	t.Cleanup(func() {
		isInteractive = origIsInteractive
		runConfirm = origRunConfirm
		applyFn = origApplyFn
	})
}

// TestApplyLegacy_NonTTY_NoYes_D8Error verifies that non-TTY without --yes
// returns the D8 error and does NOT proceed to real apply.
func TestApplyLegacy_NonTTY_NoYes_D8Error(t *testing.T) {
	stubApplyConfirmSeams(t)

	applyCallCount := 0
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		if simulate {
			return applySimulationWithChanges(), nil, nil
		}
		// Real apply — should never be called.
		panic("real apply must not be called in non-TTY D8 test")
	}

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called in non-TTY D8 test")
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            false,
	}

	err := runApplyLegacy(a, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
	assert.Contains(t, err.Error(), "--yes")
	// Only the simulate call should have happened.
	assert.Equal(t, 1, applyCallCount, "only simulate call, no real apply")
}

// TestApplyLegacy_NonTTY_WithYes_Proceeds verifies that non-TTY WITH --yes
// skips the confirm and proceeds to real apply.
func TestApplyLegacy_NonTTY_WithYes_Proceeds(t *testing.T) {
	stubApplyConfirmSeams(t)

	var applyCalls []bool
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCalls = append(applyCalls, simulate)
		if simulate {
			return applySimulationWithChanges(), nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd"}, nil, nil
	}

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called when --yes is set")
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            true,
	}

	err := runApplyLegacy(a, opts)
	require.NoError(t, err)
	assert.Equal(t, []bool{true, false}, applyCalls, "simulate then real apply")
}

// TestApplyLegacy_TTY_Declined_Aborts verifies that TTY declined → does NOT
// proceed to real apply.
func TestApplyLegacy_TTY_Declined_Aborts(t *testing.T) {
	stubApplyConfirmSeams(t)

	applyCallCount := 0
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		if simulate {
			return applySimulationWithChanges(), nil, nil
		}
		panic("real apply must not be called when user declines")
	}

	isInteractive = func() bool { return true }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		return false, nil // user declined
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            false,
	}

	err := runApplyLegacy(a, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, applyCallCount, "only simulate call — real apply not submitted after decline")
}
