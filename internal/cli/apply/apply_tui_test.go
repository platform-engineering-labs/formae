// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apply

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// newTestApp returns a minimal *app.App with no API endpoint — tests must use
// applyFn seam so app.Apply is never actually called.
func newTestApp() *app.App {
	return &app.App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{},
		},
	}
}

// simOptions captures the Options that launchSimView was called with.
var capturedSimOptions simview.Options

func TestApply_TTY_Confirmed(t *testing.T) {
	applyCallCount := 0
	var applySimulateArgs []bool

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

	var watchCommandID string
	launchWatch = func(a *app.App, commandID string) error {
		watchCommandID = commandID
		return nil
	}

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		applySimulateArgs = append(applySimulateArgs, simulate)
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command: apimodel.Command{
						CommandID:       "sim-cmd",
						ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
					},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd-123"}, nil, nil
	}

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		capturedSimOptions = opts
		return simview.DecisionConfirmed, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            false,
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.Equal(t, 2, applyCallCount, "expect two Apply calls (simulate + real)")
	require.Len(t, applySimulateArgs, 2)
	assert.True(t, applySimulateArgs[0], "first call should be simulate=true")
	assert.False(t, applySimulateArgs[1], "second call should be simulate=false")
	assert.Equal(t, "real-cmd-123", watchCommandID, "launchWatch should receive the real command ID")
	assert.Equal(t, simview.KindApply, capturedSimOptions.Kind)
	assert.False(t, capturedSimOptions.SimulateOnly)
}

func TestApply_TTY_Aborted(t *testing.T) {
	applyCallCount := 0

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

	watchCalled := false
	launchWatch = func(a *app.App, commandID string) error {
		watchCalled = true
		return nil
	}

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		return &apimodel.SubmitCommandResponse{
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
				Command: apimodel.Command{
					ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
				},
			},
		}, nil, nil
	}

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		return simview.DecisionAborted, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            false,
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.Equal(t, 1, applyCallCount, "only simulate call — no real apply after abort")
	assert.False(t, watchCalled, "launchWatch must not be called on abort")
}

func TestApply_Simulate(t *testing.T) {
	applyCallCount := 0

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

	watchCalled := false
	launchWatch = func(a *app.App, commandID string) error {
		watchCalled = true
		return nil
	}

	var capturedSimOnly bool
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		return &apimodel.SubmitCommandResponse{
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
				Command: apimodel.Command{
					ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
				},
			},
		}, nil, nil
	}

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		capturedSimOnly = opts.SimulateOnly
		// Simulate-only: should not matter what we return — flow exits after TUI anyway.
		return simview.DecisionConfirmed, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            false,
		Simulate:       true,
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.Equal(t, 1, applyCallCount, "only one Apply call when --simulate")
	assert.True(t, capturedSimOnly, "SimulateOnly must be true when --simulate")
	assert.False(t, watchCalled, "launchWatch must not be called for simulate-only")
}

func TestApply_Yes_OnTTY(t *testing.T) {
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

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	applyCallCount := 0
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "x"}}},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "yes-cmd"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            true, // --yes bypasses interactive path
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.False(t, simViewCalled, "launchSimView must NOT be called when --yes is set")
}

func TestApply_NonTTY_WithYes(t *testing.T) {
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

	isTerminal = func(w io.Writer) bool { return false } // non-TTY

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	applyCallCount := 0
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "x"}}},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "nontty-cmd"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            true, // --yes bypasses the D8 gate
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.False(t, simViewCalled, "launchSimView must NOT be called on non-TTY")
}
