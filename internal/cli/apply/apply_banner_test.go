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

// TestApply_BannerNotEmittedBeforeAltScreenTUI verifies H2: on the interactive
// TTY path (no --yes, isTerminal=true, changes required) the banner is NOT
// printed before launchSimView (the alt-screen TUI).
func TestApply_BannerNotEmittedBeforeAltScreenTUI(t *testing.T) {
	origPrintBanner := printBanner
	origIsTerminal := isTerminal
	origApplyFn := applyFn
	origLaunchSimView := launchSimView
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		printBanner = origPrintBanner
		isTerminal = origIsTerminal
		applyFn = origApplyFn
		launchSimView = origLaunchSimView
		launchWatch = origLaunchWatch
	})

	bannerCalled := false
	printBanner = func(a *app.App) {
		bannerCalled = true
	}

	isTerminal = func(w io.Writer) bool { return true }

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{
				Simulation: apimodel.Simulation{
					ChangesRequired: true,
					Command:         apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "x"}}},
				},
			}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-cmd"}, nil, nil
	}

	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		// Alt-screen TUI launched — banner must NOT have been called before this.
		return simview.DecisionAborted, nil
	}

	launchWatch = func(a *app.App, commandID string) (bool, error) { return true, nil }

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            false, // interactive path
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.False(t, bannerCalled, "banner must NOT be printed before alt-screen TUI (launchSimView)")
}

// TestApply_BannerEmittedOnPrintAndExitPath verifies H2: on the --yes path
// (print-and-exit, no alt-screen TUI), the banner IS printed.
func TestApply_BannerEmittedOnPrintAndExitPath(t *testing.T) {
	origPrintBanner := printBanner
	origIsTerminal := isTerminal
	origApplyFn := applyFn
	origLaunchSimView := launchSimView
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		printBanner = origPrintBanner
		isTerminal = origIsTerminal
		applyFn = origApplyFn
		launchSimView = origLaunchSimView
		launchWatch = origLaunchWatch
	})

	bannerCalled := false
	printBanner = func(a *app.App) {
		bannerCalled = true
	}

	isTerminal = func(w io.Writer) bool { return true }

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
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

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionConfirmed, nil
	}

	launchWatch = func(a *app.App, commandID string) (bool, error) { return true, nil }

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Yes:            true, // print-and-exit path: no simView
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	assert.False(t, simViewCalled, "launchSimView must NOT be called when --yes")
	assert.True(t, bannerCalled, "banner MUST be printed on --yes (print-and-exit) path")
}
