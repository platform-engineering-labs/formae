//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package apply

import (
	"errors"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"testing"
)

func resolutionRejection(id string) apimodel.FormaReconcileRejectedError {
	return apimodel.FormaReconcileRejectedError{ObservationID: id, ModifiedStacks: map[string]apimodel.ModifiedStack{"production": {ModifiedResources: []apimodel.ResourceModification{{ResourceID: "a", Stack: "production", Label: "one", Operation: "update"}, {ResourceID: "b", Stack: "production", Label: "two", Operation: "delete"}}}}}
}

func TestRecordedResolutionCompleteness(t *testing.T) {
	r := resolutionRejection("obs")
	for _, decisions := range [][]pkgmodel.DriftDecision{nil, {{ResourceID: "a", Action: "absorb"}}, {{ResourceID: "a", Action: "absorb"}, {ResourceID: "b", Action: ""}}, {{ResourceID: "a", Action: "absorb"}, {ResourceID: "a", Action: "revert"}}, {{ResourceID: "a", Action: "skip"}, {ResourceID: "b", Action: "revert"}}} {
		require.Error(t, validateDecisions(r, decisions))
	}
	require.NoError(t, validateDecisions(r, []pkgmodel.DriftDecision{{ResourceID: "b", Action: "revert"}, {ResourceID: "a", Action: "absorb"}}))
}

func TestRecordedResolutionFlow(t *testing.T) {
	for _, scenario := range []string{"mixed", "pure", "simulate", "cancel", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			oldApply, oldChoices, oldPreview, oldWatch, oldDelta := applyFn, chooseDrift, launchSimView, launchWatch, desiredDeltaFn
			t.Cleanup(func() {
				applyFn = oldApply
				chooseDrift = oldChoices
				launchSimView = oldPreview
				launchWatch = oldWatch
				desiredDeltaFn = oldDelta
			})
			opts := &ApplyOptions{Mode: pkgmodel.FormaApplyModeReconcile, FormaFile: "source.pkl", Simulate: scenario == "simulate"}
			choices, previews, reals, watches := 0, 0, 0, 0
			chooseDrift = func(_ *theme.Theme, r apimodel.FormaReconcileRejectedError) ([]pkgmodel.DriftDecision, error) {
				choices++
				if scenario == "cancel" {
					return nil, errResolutionAborted
				}
				return []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}, {ResourceID: "b", Action: "revert"}}, nil
			}
			applyFn = func(_ *app.App, o *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
				require.False(t, o.Force)
				if o.Resolution == nil {
					require.True(t, simulate)
					return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{Data: resolutionRejection("fresh")}
				}
				if simulate {
					require.Empty(t, o.Resolution.ReviewID)
					return &apimodel.SubmitCommandResponse{Review: &pkgmodel.DriftReview{ObservationID: o.Resolution.ObservationID, ReviewID: "review", Decisions: o.Resolution.Decisions}, Simulation: apimodel.Simulation{ChangesRequired: true, Warnings: []string{"real planner warning"}, Command: apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "accept", ResourceLabel: "one"}, {Operation: "create", ResourceLabel: "unrelated addition"}}}}}, nil, nil
				}
				reals++
				require.Equal(t, "review", o.Resolution.ReviewID)
				require.NotEmpty(t, o.Resolution.IdempotencyKey)
				if scenario == "stale" && reals == 1 {
					return nil, nil, &apimodel.ErrorResponse[apimodel.DriftResolutionError]{Data: apimodel.DriftResolutionError{Code: "stale-review"}}
				}
				state := "InProgress"
				if scenario == "pure" {
					state = "Success"
				}
				return &apimodel.SubmitCommandResponse{CommandID: "recorded", Simulation: apimodel.Simulation{Command: apimodel.Command{State: state}}}, nil, nil
			}
			launchSimView = func(_ *theme.Theme, sim *apimodel.Simulation, o simview.Options) (simview.Decision, error) {
				previews++
				require.Len(t, sim.Command.ResourceUpdates, 2)
				require.Contains(t, sim.Warnings, "real planner warning")
				return simview.DecisionConfirmed, nil
			}
			launchWatch = func(_ *app.App, id string) (bool, error) {
				watches++
				require.Equal(t, "recorded", id)
				return true, nil
			}
			desiredDeltaFn = func(_ *app.App, id string) (*apimodel.CommandDesiredDelta, error) {
				return &apimodel.CommandDesiredDelta{CommandID: id, State: "Success", Partial: true}, nil
			}
			out := captureStdout(t, func() {
				require.NoError(t, runRecordedDriftFlow(newTestApp(), theme.New("formae"), opts, resolutionRejection("obs")))
			})
			switch scenario {
			case "cancel":
				require.Zero(t, reals)
				require.Zero(t, previews)
			case "simulate":
				require.Zero(t, reals)
				require.Equal(t, 1, previews)
			case "stale":
				require.Equal(t, 2, choices)
				require.Equal(t, 2, previews)
				require.Equal(t, 2, reals)
			case "pure":
				require.Zero(t, watches)
				require.Contains(t, out, "recorded: Success")
			default:
				require.Equal(t, 1, watches)
			}
		})
	}
}

func TestResolutionMessageOwnership(t *testing.T) {
	r := &pkgmodel.DriftReview{Observations: []pkgmodel.DriftObservation{{Stack: "production"}}, Decisions: []pkgmodel.DriftDecision{{Action: "absorb"}, {Action: "revert"}}}
	opts := &ApplyOptions{}
	prepareMessage(opts, r)
	require.Equal(t, "Resolve production drift: absorb 1, revert 1", opts.Message)
	opts.Message = ""
	opts.messageEdited = true
	prepareMessage(opts, r)
	require.Empty(t, opts.Message)
	opts = &ApplyOptions{Message: "", MessageExplicit: true}
	prepareMessage(opts, r)
	require.Empty(t, opts.Message)
	opts = &ApplyOptions{Message: "authored", MessageExplicit: true}
	prepareMessage(opts, r)
	require.Equal(t, "authored", opts.Message)
}

func TestResolutionDecisionCancellation(t *testing.T) {
	old := runDecisionForm
	t.Cleanup(func() { runDecisionForm = old })
	runDecisionForm = func(_ *theme.Theme, items []driftChoice) error { return errors.New("cancelled") }
	decisions, err := defaultChooseDrift(theme.New("formae"), resolutionRejection("obs"))
	require.Error(t, err)
	require.Empty(t, decisions)
	runDecisionForm = func(_ *theme.Theme, items []driftChoice) error { return nil }
	decisions, err = defaultChooseDrift(theme.New("formae"), resolutionRejection("obs"))
	require.Error(t, err)
	require.Empty(t, decisions, "unset choices must never become approvals")
}

func TestResolutionDoesNotPromptWithRedirectedInput(t *testing.T) {
	oldTerminal, oldApply, oldChoose, oldBanner := isTerminal, applyFn, chooseDrift, printBanner
	t.Cleanup(func() { isTerminal = oldTerminal; applyFn = oldApply; chooseDrift = oldChoose; printBanner = oldBanner })
	isTerminal = func(writer io.Writer) bool { return writer == os.Stdout }
	printBanner = func(_ *app.App) {}
	applyFn = func(_ *app.App, _ *ApplyOptions, sim bool) (*apimodel.SubmitCommandResponse, []string, error) {
		require.True(t, sim)
		return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{Data: resolutionRejection("obs")}
	}
	chooseDrift = func(_ *theme.Theme, _ apimodel.FormaReconcileRejectedError) ([]pkgmodel.DriftDecision, error) {
		t.Fatal("redirected stdin must not prompt")
		return nil, errResolutionAborted
	}
	require.Error(t, runApplyForHumans(newTestApp(), &ApplyOptions{Mode: pkgmodel.FormaApplyModeReconcile, FormaFile: "source.pkl"}))
}

func TestFinalMessageFieldPreservesClearedValueAcrossStaleReview(t *testing.T) {
	oldApply, oldChoices, oldPreview, oldWatch, oldDelta := applyFn, chooseDrift, launchSimView, launchWatch, desiredDeltaFn
	t.Cleanup(func() {
		applyFn = oldApply
		chooseDrift = oldChoices
		launchSimView = oldPreview
		launchWatch = oldWatch
		desiredDeltaFn = oldDelta
	})
	opts := &ApplyOptions{Mode: pkgmodel.FormaApplyModeReconcile, FormaFile: "source.pkl"}
	chooseDrift = func(_ *theme.Theme, _ apimodel.FormaReconcileRejectedError) ([]pkgmodel.DriftDecision, error) {
		return []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}, {ResourceID: "b", Action: "revert"}}, nil
	}
	submissions, previews := 0, 0
	applyFn = func(_ *app.App, o *ApplyOptions, sim bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if o.Resolution == nil {
			return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{Data: resolutionRejection("fresh")}
		}
		if sim {
			return &apimodel.SubmitCommandResponse{Review: &pkgmodel.DriftReview{ReviewID: "review", Decisions: o.Resolution.Decisions, Observations: []pkgmodel.DriftObservation{{Stack: "production"}}}, Simulation: apimodel.Simulation{ChangesRequired: true}}, nil, nil
		}
		submissions++
		require.Empty(t, o.Message)
		if submissions == 1 {
			return nil, nil, &apimodel.ErrorResponse[apimodel.DriftResolutionError]{Data: apimodel.DriftResolutionError{Code: "stale-review"}}
		}
		return &apimodel.SubmitCommandResponse{CommandID: "recorded", Simulation: apimodel.Simulation{Command: apimodel.Command{State: "Success"}}}, nil, nil
	}
	launchSimView = func(_ *theme.Theme, _ *apimodel.Simulation, o simview.Options) (simview.Decision, error) {
		previews++
		require.NotNil(t, o.Message)
		if previews == 1 {
			require.Contains(t, *o.Message, "Resolve production drift")
			*o.Message = ""
		} else {
			require.Empty(t, *o.Message)
		}
		return simview.DecisionConfirmed, nil
	}
	desiredDeltaFn = func(_ *app.App, id string) (*apimodel.CommandDesiredDelta, error) {
		return &apimodel.CommandDesiredDelta{CommandID: id, State: "Success", Partial: true}, nil
	}
	captureStdout(t, func() {
		require.NoError(t, runRecordedDriftFlow(newTestApp(), theme.New("formae"), opts, resolutionRejection("obs")))
	})
	require.Equal(t, 2, previews)
	require.Equal(t, 2, submissions)
}

func TestAcceptanceOnlyLegacyReportsRecordedSuccess(t *testing.T) {
	oldApply, oldInteractive := applyFn, isInteractive
	t.Cleanup(func() { applyFn = oldApply; isInteractive = oldInteractive })
	isInteractive = func() bool { return false }
	applyFn = func(_ *app.App, _ *ApplyOptions, sim bool) (*apimodel.SubmitCommandResponse, []string, error) {
		res := &apimodel.SubmitCommandResponse{CommandID: "accepted", Simulation: apimodel.Simulation{ChangesRequired: true, Command: apimodel.Command{ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "accept"}}}}}
		if !sim {
			res.Simulation.Command.State = "Success"
		}
		return res, nil, nil
	}
	out := captureStdout(t, func() { require.NoError(t, runApplyLegacy(newTestApp(), &ApplyOptions{Yes: true})) })
	require.Contains(t, out, "Command accepted: Success")
	require.NotContains(t, out, "asynchronous")
}

func TestLegacyMixedResolutionPrintsRecordedGuidance(t *testing.T) {
	oldApply, oldInteractive, oldWatch, oldDelta := applyFn, isInteractive, launchWatch, desiredDeltaFn
	t.Cleanup(func() {
		applyFn = oldApply
		isInteractive = oldInteractive
		launchWatch = oldWatch
		desiredDeltaFn = oldDelta
	})
	isInteractive = func() bool { return true }
	applyFn = func(_ *app.App, _ *ApplyOptions, sim bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return &apimodel.SubmitCommandResponse{CommandID: "mixed", Review: &pkgmodel.DriftReview{ReviewID: "review"}, Simulation: apimodel.Simulation{ChangesRequired: true}}, nil, nil
	}
	launchWatch = func(_ *app.App, id string) (bool, error) { require.Equal(t, "mixed", id); return true, nil }
	desiredDeltaFn = func(_ *app.App, id string) (*apimodel.CommandDesiredDelta, error) {
		return &apimodel.CommandDesiredDelta{CommandID: id, State: "Success", Partial: true}, nil
	}
	out := captureStdout(t, func() {
		require.NoError(t, runApplyLegacy(newTestApp(), &ApplyOptions{Yes: true, Resolution: &pkgmodel.DriftResolution{ObservationID: "observation", Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}))
	})
	require.Contains(t, out, "formae extract --command 'mixed'")
}

func TestDriftDisplayIdentityEscapesControls(t *testing.T) {
	require.Equal(t, "production", driftDisplayIdentity("production"))
	require.Equal(t, `"prod\n\x1b[31m"`, driftDisplayIdentity("prod\n\x1b[31m"))
}
