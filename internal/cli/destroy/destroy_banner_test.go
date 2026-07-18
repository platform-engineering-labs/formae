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
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func newDestroyTestApp() *app.App {
	return &app.App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{},
		},
	}
}

// TestDestroy_BannerNotEmittedBeforeAltScreenTUI verifies H2: on the interactive
// TTY path (no --yes, isTerminal=true, changes required) the banner is NOT
// printed before launchSimView (the alt-screen TUI).
func TestDestroy_BannerNotEmittedBeforeAltScreenTUI(t *testing.T) {
	origPrintBanner := printBanner
	origIsTerminal := isTerminal
	origDestroyFn := destroyFn
	origLaunchSimView := launchSimView
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		printBanner = origPrintBanner
		isTerminal = origIsTerminal
		destroyFn = origDestroyFn
		launchSimView = origLaunchSimView
		launchWatch = origLaunchWatch
	})

	bannerCalled := false
	printBanner = func(a *app.App) {
		bannerCalled = true
	}

	isTerminal = func(w io.Writer) bool { return true }

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return &apimodel.SubmitCommandResponse{
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
				Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "x"}}},
			},
		}, nil, nil
	}

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		// Alt-screen TUI launched — banner must NOT have been called before this.
		return simview.DecisionAborted, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newDestroyTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Yes:            false, // interactive path
		OnDependents:   OnDependentsAbort,
	}

	err := runDestroyForHumans(a, opts)
	require.NoError(t, err)

	assert.False(t, bannerCalled, "banner must NOT be printed before alt-screen TUI (launchSimView)")
}

// TestDestroy_BannerEmittedOnPrintAndExitPath verifies H2: on the --yes path
// (print-and-exit, no alt-screen TUI), the banner IS printed.
func TestDestroy_BannerEmittedOnPrintAndExitPath(t *testing.T) {
	origPrintBanner := printBanner
	origIsTerminal := isTerminal
	origDestroyFn := destroyFn
	origLaunchSimView := launchSimView
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		printBanner = origPrintBanner
		isTerminal = origIsTerminal
		destroyFn = origDestroyFn
		launchSimView = origLaunchSimView
		launchWatch = origLaunchWatch
	})

	bannerCalled := false
	printBanner = func(a *app.App) {
		bannerCalled = true
	}

	isTerminal = func(w io.Writer) bool { return true }

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "delete", ResourceLabel: "x"}}},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "destroy-cmd"}, nil, nil
	}

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newDestroyTestApp()
	opts := &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Yes:            true, // print-and-exit path: no simView
		OnDependents:   OnDependentsAbort,
	}

	err := runDestroyForHumans(a, opts)
	require.NoError(t, err)

	assert.False(t, simViewCalled, "launchSimView must NOT be called when --yes")
	assert.True(t, bannerCalled, "banner MUST be printed on --yes (print-and-exit) path")
}
