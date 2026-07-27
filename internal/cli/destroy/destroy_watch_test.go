// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package destroy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// TestDestroyLegacy_InteractiveYes_WatchesByDefault verifies that `destroy --yes`
// on an interactive terminal watches by default (the flag path was removed).
func TestDestroyLegacy_InteractiveYes_WatchesByDefault(t *testing.T) {
	origDestroyFn := destroyFn
	origIsInteractive := isInteractive
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		destroyFn = origDestroyFn
		isInteractive = origIsInteractive
		launchWatch = origLaunchWatch
	})

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{Simulation: apimodel.Simulation{ChangesRequired: true}}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-destroy"}, nil, nil
	}
	isInteractive = func() bool { return true }
	watchedID := ""
	launchWatch = func(_ *app.App, commandID string) (bool, error) {
		watchedID = commandID
		return true, nil
	}

	err := runDestroyLegacy(newDestroyTestApp(), &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman, FormaFile: "forma.pkl",
		Yes: true, OnDependents: OnDependentsAbort,
	})
	require.NoError(t, err)
	assert.Equal(t, "real-destroy", watchedID, "--yes on an interactive terminal must watch by default")
}

// TestDestroyLegacy_NonInteractive_FireAndForget verifies that off a TTY the
// destroy is fire-and-forget (no watch launched).
func TestDestroyLegacy_NonInteractive_FireAndForget(t *testing.T) {
	origDestroyFn := destroyFn
	origIsInteractive := isInteractive
	origLaunchWatch := launchWatch
	t.Cleanup(func() {
		destroyFn = origDestroyFn
		isInteractive = origIsInteractive
		launchWatch = origLaunchWatch
	})

	destroyFn = func(a *app.App, opts *DestroyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return &apimodel.SubmitCommandResponse{Simulation: apimodel.Simulation{ChangesRequired: true}}, nil, nil
		}
		return &apimodel.SubmitCommandResponse{CommandID: "real-destroy"}, nil, nil
	}
	isInteractive = func() bool { return false }
	watchCalled := false
	launchWatch = func(_ *app.App, _ string) (bool, error) {
		watchCalled = true
		return true, nil
	}

	err := runDestroyLegacy(newDestroyTestApp(), &DestroyOptions{
		OutputConsumer: printer.ConsumerHuman, FormaFile: "forma.pkl",
		Yes: true, OnDependents: OnDependentsAbort,
	})
	require.NoError(t, err)
	assert.False(t, watchCalled, "a non-interactive destroy must be fire-and-forget")
}
