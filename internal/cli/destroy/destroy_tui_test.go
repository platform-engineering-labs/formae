// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package destroy

import (
	"io"
	"os"
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
// the destroyFn seam so app.Destroy is never actually called.
func newTestApp() *app.App {
	return &app.App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{},
		},
	}
}

// stubSeams saves the package seams and registers cleanup to restore them.
func stubSeams(t *testing.T) {
	t.Helper()
	origDestroyFn := destroyFn
	origIsTerminal := isTerminal
	origLaunchSimView := launchSimView
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		destroyFn = origDestroyFn
		isTerminal = origIsTerminal
		launchSimView = origLaunchSimView
		launchWatch = origLaunchWatch
	})
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// cascadeSimulation returns a simulation with one direct delete, one cascade
// resource delete, and one cascade target delete.
func cascadeSimulation() apimodel.Simulation {
	return apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "sim-cmd",
			Command:   "destroy",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{Operation: "delete", ResourceLabel: "primary-db", ResourceType: "AWS::RDS::DBInstance"},
				{Operation: "delete", ResourceLabel: "web-server", ResourceType: "AWS::EC2::Instance", IsCascade: true, CascadeSource: "primary-db"},
			},
			TargetUpdates: []apimodel.TargetUpdate{
				{Operation: "delete", TargetLabel: "staging-account", IsCascade: true, CascadeSource: "primary-db"},
			},
		},
	}
}

func TestDestroy_TTY_Confirmed(t *testing.T) {
	stubSeams(t)

	destroyCallCount := 0
	var destroySimulateArgs []bool

	isTerminal = func(w io.Writer) bool { return true }

	var watchCommandID string
	launchWatch = func(a *app.App, commandID string) error {
		watchCommandID = commandID
		return nil
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		destroySimulateArgs = append(destroySimulateArgs, simulate)
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command: apimodel.Command{
						CommandID:       "sim-cmd",
						ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
					},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd-123"}, nil, nil
	}

	var capturedSimOptions simview.Options
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		capturedSimOptions = opts
		return simview.DecisionConfirmed, nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
	}

	out := captureStdout(t, func() {
		err := runDestroyForHumans(a, opts)
		require.NoError(t, err)
	})

	assert.Equal(t, 2, destroyCallCount, "expect two Destroy calls (simulate + real)")
	require.Len(t, destroySimulateArgs, 2)
	assert.True(t, destroySimulateArgs[0], "first call should be simulate=true")
	assert.False(t, destroySimulateArgs[1], "second call should be simulate=false")
	assert.Equal(t, "real-cmd-123", watchCommandID, "launchWatch should receive the real command ID")
	assert.Equal(t, simview.KindDestroy, capturedSimOptions.Kind)
	assert.Equal(t, "forma.pkl", capturedSimOptions.Source, "Source should be the forma file when set")
	assert.False(t, capturedSimOptions.SimulateOnly)
	assert.Contains(t, out, "real-cmd-123", "scrollback record should mention the submitted command ID")
}

func TestDestroy_TTY_Confirmed_QuerySource(t *testing.T) {
	stubSeams(t)

	isTerminal = func(w io.Writer) bool { return true }
	launchWatch = func(a *app.App, commandID string) error { return nil }

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command: apimodel.Command{
						ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
					},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd-456"}, nil, nil
	}

	var capturedSimOptions simview.Options
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		capturedSimOptions = opts
		return simview.DecisionConfirmed, nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		Query:          "stack:staging",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
	}

	_ = captureStdout(t, func() {
		err := runDestroyForHumans(a, opts)
		require.NoError(t, err)
	})

	assert.Equal(t, "query: stack:staging", capturedSimOptions.Source, "Source should be the query when no forma file is set")
}

func TestDestroy_TTY_Aborted(t *testing.T) {
	stubSeams(t)

	destroyCallCount := 0

	isTerminal = func(w io.Writer) bool { return true }

	watchCalled := false
	launchWatch = func(a *app.App, commandID string) error {
		watchCalled = true
		return nil
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		return &apimodel.SubmitCommandResponse{
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
				Command: apimodel.Command{
					ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
				},
			},
		}, nil, nil
	}

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		return simview.DecisionAborted, nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
	}

	_ = captureStdout(t, func() {
		err := runDestroyForHumans(a, opts)
		require.NoError(t, err)
	})

	assert.Equal(t, 1, destroyCallCount, "only simulate call — no real destroy after abort")
	assert.False(t, watchCalled, "launchWatch must not be called on abort")
}

func TestDestroy_Simulate(t *testing.T) {
	stubSeams(t)

	destroyCallCount := 0

	isTerminal = func(w io.Writer) bool { return true }

	watchCalled := false
	launchWatch = func(a *app.App, commandID string) error {
		watchCalled = true
		return nil
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		return &apimodel.SubmitCommandResponse{
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
				Command: apimodel.Command{
					ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket"}},
				},
			},
		}, nil, nil
	}

	var capturedSimOnly bool
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		capturedSimOnly = opts.SimulateOnly
		return simview.DecisionConfirmed, nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            false,
		Simulate:       true,
	}

	_ = captureStdout(t, func() {
		err := runDestroyForHumans(a, opts)
		require.NoError(t, err)
	})

	assert.Equal(t, 1, destroyCallCount, "only one Destroy call when --simulate")
	assert.True(t, capturedSimOnly, "SimulateOnly must be true when --simulate")
	assert.False(t, watchCalled, "launchWatch must not be called for simulate-only")
}

func TestDestroy_Yes_OnTTY(t *testing.T) {
	stubSeams(t)

	isTerminal = func(w io.Writer) bool { return true }

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "x"}}},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "yes-cmd"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            true, // --yes bypasses interactive path
	}

	_ = captureStdout(t, func() {
		err := runDestroyForHumans(a, opts)
		require.NoError(t, err)
	})

	assert.False(t, simViewCalled, "launchSimView must NOT be called when --yes is set")
}

func TestDestroy_NonTTY(t *testing.T) {
	stubSeams(t)

	isTerminal = func(w io.Writer) bool { return false } // non-TTY

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "x"}}},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "nontty-cmd"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            true, // avoid the interactive BasicPrompter on the legacy path
	}

	_ = captureStdout(t, func() {
		err := runDestroyForHumans(a, opts)
		require.NoError(t, err)
	})

	assert.False(t, simViewCalled, "launchSimView must NOT be called on non-TTY")
}

func TestDestroy_Yes_Cascades_Abort(t *testing.T) {
	stubSeams(t)

	destroyCallCount := 0

	isTerminal = func(w io.Writer) bool { return true }

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	watchCalled := false
	launchWatch = func(a *app.App, commandID string) error {
		watchCalled = true
		return nil
	}

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		return &apimodel.SubmitCommandResponse{Simulation: cascadeSimulation()}, nil, nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort,
		Yes:            true,
	}

	var err error
	out := captureStdout(t, func() {
		err = runDestroyForHumans(a, opts)
	})

	require.Error(t, err, "cascade deletes with --yes and --on-dependents=abort must return an error")
	assert.Equal(t, 1, destroyCallCount, "only the simulate call — no real destroy after abort")
	assert.False(t, simViewCalled, "launchSimView must not run on the --yes path")
	assert.False(t, watchCalled, "launchWatch must not run after abort")

	// Styled abort panel (destroy-cascade mockup VIEW 2).
	assert.Contains(t, out, "Command Aborted")
	assert.Contains(t, out, "To proceed, use --on-dependents=cascade")
	assert.Contains(t, out, "web-server (AWS::EC2::Instance)")
	assert.Contains(t, out, "will be deleted because it depends on primary-db")
	assert.Contains(t, out, "staging-account (target)", "target cascades must be listed in the panel")
}

func TestDestroy_Yes_Cascades_CascadeFlag(t *testing.T) {
	stubSeams(t)

	destroyCallCount := 0

	isTerminal = func(w io.Writer) bool { return true }

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		destroyCallCount++
		if simulate {
			return &apimodel.SubmitCommandResponse{Simulation: cascadeSimulation()}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "cascade-cmd"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		return simview.DecisionConfirmed, nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsCascade,
		Yes:            true,
	}

	var err error
	out := captureStdout(t, func() {
		err = runDestroyForHumans(a, opts)
	})

	require.NoError(t, err, "--on-dependents=cascade must proceed despite cascades")
	assert.Equal(t, 2, destroyCallCount, "simulate + real destroy")
	assert.NotContains(t, out, "Command Aborted", "no abort panel when cascade is allowed")
}

func TestDestroy_Interactive_Cascades(t *testing.T) {
	stubSeams(t)

	isTerminal = func(w io.Writer) bool { return true }

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{Simulation: cascadeSimulation()}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "interactive-cascade-cmd"}, nil, nil
	}

	var capturedSim *apimodel.Simulation
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		capturedSim = sim
		return simview.DecisionConfirmed, nil
	}

	var watchCommandID string
	launchWatch = func(a *app.App, commandID string) error {
		watchCommandID = commandID
		return nil
	}

	a := newTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		OnDependents:   OnDependentsAbort, // interactive confirm substitutes for cascade
		Yes:            false,
	}

	var err error
	out := captureStdout(t, func() {
		err = runDestroyForHumans(a, opts)
	})

	require.NoError(t, err, "interactive confirm substitutes for --on-dependents=cascade")
	require.NotNil(t, capturedSim, "launchSimView must receive the simulation")

	cascadeRows := 0
	for _, ru := range capturedSim.Command.ResourceUpdates {
		if ru.IsCascade {
			cascadeRows++
		}
	}
	assert.Equal(t, 1, cascadeRows, "simulation passed to simview must retain cascade rows")

	cascadeTargets := 0
	for _, tu := range capturedSim.Command.TargetUpdates {
		if tu.IsCascade {
			cascadeTargets++
		}
	}
	assert.Equal(t, 1, cascadeTargets, "simulation passed to simview must retain cascade target rows")

	assert.Equal(t, "interactive-cascade-cmd", watchCommandID, "confirmed cascade destroy must proceed to watch")
	assert.NotContains(t, out, "Command Aborted", "no abort panel on the interactive path")
}
