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

// applySimulationWithDescription returns a simulation with changes and the
// given description attached.
func applySimulationWithDescription(text string, confirm bool) *apimodel.SubmitCommandResponse {
	resp := applySimulationWithChanges()
	resp.Description = apimodel.Description{Text: text, Confirm: confirm}
	return resp
}

// stubDescriptionSeams saves and restores the seams used by description tests.
func stubDescriptionSeams(t *testing.T) {
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

// panicOnRealApply is an applyFn stub that panics if the real (non-simulate)
// apply is called — used to assert the downstream apply was NOT reached.
func panicOnRealApply(resp *apimodel.SubmitCommandResponse) func(*app.App, *ApplyOptions, bool) (*apimodel.SubmitCommandResponse, []string, error) {
	return func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if !simulate {
			panic("real apply must not be called")
		}
		return resp, nil, nil
	}
}

// TestDescriptionAck_TTY_Declined_DoesNotProceed verifies R3: when
// description.Confirm==true and the user declines the acknowledgment prompt,
// the real apply is NOT submitted.
func TestDescriptionAck_TTY_Declined_DoesNotProceed(t *testing.T) {
	stubDescriptionSeams(t)

	sim := applySimulationWithDescription("This is a dangerous operation.", true)
	applyFn = panicOnRealApply(sim)

	isInteractive = func() bool { return true }

	runConfirmCalls := 0
	runConfirm = func(_ *theme.Theme, prompt, _ string) (bool, error) {
		runConfirmCalls++
		if prompt == "Acknowledge and continue?" {
			return false, nil // user declines the ack
		}
		// Operation confirm — should never be reached if ack declined
		panic("operation confirm must not be reached after ack declined")
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
	assert.Equal(t, 1, runConfirmCalls, "only the ack prompt should have been shown")
}

// TestDescriptionAck_TTY_Accepted_Proceeds verifies R3: when
// description.Confirm==true and the user accepts the acknowledgment prompt,
// the flow continues to the operation confirm.
func TestDescriptionAck_TTY_Accepted_Proceeds(t *testing.T) {
	stubDescriptionSeams(t)

	sim := applySimulationWithDescription("This is a dangerous operation.", true)
	var applyCalls []bool
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCalls = append(applyCalls, simulate)
		if simulate {
			return sim, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd"}, nil, nil
	}

	isInteractive = func() bool { return true }

	runConfirmCalls := 0
	runConfirm = func(_ *theme.Theme, prompt, _ string) (bool, error) {
		runConfirmCalls++
		// Both the ack and the operation confirm should be accepted.
		return true, nil
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
	assert.Equal(t, []bool{true, false}, applyCalls, "simulate then real apply must both run")
	assert.Equal(t, 2, runConfirmCalls, "ack prompt then operation confirm should both be shown")
}

// TestDescriptionAck_NonTTY_NoYes_D8Error verifies that non-TTY without --yes
// returns the D8 error when description.Confirm==true.
func TestDescriptionAck_NonTTY_NoYes_D8Error(t *testing.T) {
	stubDescriptionSeams(t)

	sim := applySimulationWithDescription("Requires acknowledgment.", true)
	applyFn = panicOnRealApply(sim)

	isInteractive = func() bool { return false }
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		panic("runConfirm must not be called in non-TTY test")
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
}

// TestDescriptionAck_WithYes_Proceeds verifies that --yes skips the entire
// description-ack block (via the outer !opts.Yes guard in runApplyLegacy) and
// proceeds directly to real apply. maybePrintDescription is never called when
// opts.Yes is true, so isInteractive and runConfirm are irrelevant here.
func TestDescriptionAck_WithYes_Proceeds(t *testing.T) {
	stubDescriptionSeams(t)

	sim := applySimulationWithDescription("Requires acknowledgment.", true)
	var applyCalls []bool
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCalls = append(applyCalls, simulate)
		if simulate {
			return sim, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd"}, nil, nil
	}

	// maybePrintDescription is never reached with --yes, so these seams are not
	// exercised — but we keep the panic stubs to catch regressions.
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
	assert.Equal(t, []bool{true, false}, applyCalls, "simulate then real apply must both run")
}

// TestDescriptionNoConfirm_PrintsTextNoAck verifies that when
// description.Confirm==false (but Text is non-empty), the text is printed,
// no ack is requested, and the flow proceeds.
func TestDescriptionNoConfirm_PrintsTextNoAck(t *testing.T) {
	stubDescriptionSeams(t)

	sim := applySimulationWithDescription("Informational notice only.", false)
	var applyCalls []bool
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCalls = append(applyCalls, simulate)
		if simulate {
			return sim, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd"}, nil, nil
	}

	isInteractive = func() bool { return true }
	runConfirm = func(_ *theme.Theme, prompt, _ string) (bool, error) {
		// Operation confirm is fine; ack must NOT be called.
		if prompt == "Acknowledge and continue?" {
			panic("ack must not be requested when Confirm==false")
		}
		return true, nil
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
	assert.Equal(t, []bool{true, false}, applyCalls, "simulate then real apply must both run")
}
