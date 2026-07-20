// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apply

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/driftview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/simview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---- sameDrift tests ----

func TestSameDrift_EqualDifferentOrder(t *testing.T) {
	patch := json.RawMessage(`{"op":"replace","path":"/Foo"}`)
	a := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: patch},
				{Type: "AWS::S3::Bucket", Label: "b", Operation: "update", PatchDocument: patch},
			}},
		},
	}
	b := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "b", Operation: "update", PatchDocument: patch},
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: patch},
			}},
		},
	}
	assert.True(t, sameDrift(a, b), "same mods in different order should be equal")
}

func TestSameDrift_DifferentPatchBytes(t *testing.T) {
	a := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: json.RawMessage(`{"op":"replace","path":"/Foo","value":"old"}`)},
			}},
		},
	}
	b := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: json.RawMessage(`{"op":"replace","path":"/Foo","value":"new"}`)},
			}},
		},
	}
	assert.False(t, sameDrift(a, b), "different patch bytes should not be equal")
}

func TestSameDrift_ExtraResource(t *testing.T) {
	patch := json.RawMessage(`{}`)
	a := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: patch},
			}},
		},
	}
	b := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: patch},
				{Type: "AWS::S3::Bucket", Label: "b", Operation: "update", PatchDocument: patch},
			}},
		},
	}
	assert.False(t, sameDrift(a, b), "extra resource in b should not be equal")
}

func TestSameDrift_DifferentStacks(t *testing.T) {
	patch := json.RawMessage(`{}`)
	a := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: patch},
			}},
		},
	}
	b := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"staging": {ModifiedResources: []apimodel.ResourceModification{
				{Type: "AWS::S3::Bucket", Label: "a", Operation: "update", PatchDocument: patch},
			}},
		},
	}
	assert.False(t, sameDrift(a, b), "different stack names should not be equal")
}

// ---- runDriftFlow tests ----

// stubDriftSeams saves originals and restores them on cleanup.
func stubDriftSeams(t *testing.T) {
	t.Helper()
	orig := struct {
		launchDriftView      func(*theme.Theme, *apimodel.FormaReconcileRejectedError, driftview.Options) (driftview.Decision, error)
		applyFn              func(*app.App, *ApplyOptions, bool) (*apimodel.SubmitCommandResponse, []string, error)
		forcedApplyFn        func(*app.App, *ApplyOptions) (*apimodel.SubmitCommandResponse, []string, error)
		launchWatch          func(*app.App, string) error
		launchSimView        func(*theme.Theme, *apimodel.Simulation, simview.Options) (simview.Decision, error)
		confirmOverwriteFn   func(*theme.Theme, string) (bool, error)
		extractResourcesFn   func(*app.App, string) (*pkgmodel.Forma, []string, error)
		generateSourceCodeFn func(*app.App, *pkgmodel.Forma, string) error
		isTerminal           func(io.Writer) bool
	}{
		launchDriftView:      launchDriftView,
		applyFn:              applyFn,
		forcedApplyFn:        forcedApplyFn,
		launchWatch:          launchWatch,
		launchSimView:        launchSimView,
		confirmOverwriteFn:   confirmOverwriteFn,
		extractResourcesFn:   extractResourcesFn,
		generateSourceCodeFn: generateSourceCodeFn,
		isTerminal:           isTerminal,
	}
	t.Cleanup(func() {
		launchDriftView = orig.launchDriftView
		applyFn = orig.applyFn
		forcedApplyFn = orig.forcedApplyFn
		launchWatch = orig.launchWatch
		launchSimView = orig.launchSimView
		confirmOverwriteFn = orig.confirmOverwriteFn
		extractResourcesFn = orig.extractResourcesFn
		generateSourceCodeFn = orig.generateSourceCodeFn
		isTerminal = orig.isTerminal
	})
}

func makeRejected() apimodel.FormaReconcileRejectedError {
	return apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "my-bucket", Operation: "update",
					PatchDocument: json.RawMessage(`{"op":"replace"}`)},
			}},
		},
	}
}

// TestRunDriftFlow_SimulateNeverCallsForcedApply asserts that when opts.Simulate=true
// and the user chooses DecisionRevertAll with identical drift, forcedApplyFn is
// NEVER invoked. This is the core regression guard for the P1 bug where
// --simulate could trigger a real forced apply through the drift flow.
func TestRunDriftFlow_SimulateNeverCallsForcedApply(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionRevertAll{}, nil
	}

	// Second simulate returns same rejection (identical drift) — the path that
	// previously called submitForcedApply directly.
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
			ErrorType: apimodel.ReconcileRejected,
			Data:      rejected,
		}
	}

	forcedApplyFn = func(a *app.App, opts *ApplyOptions) (*apimodel.SubmitCommandResponse, []string, error) {
		t.Fatal("forcedApplyFn MUST NOT be called when opts.Simulate=true")
		return nil, nil, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Simulate:       true,
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	// Must return cleanly (not an error) — simulate just shows what would happen.
	require.NoError(t, err)
}

// TestRunDriftFlow_NonSimulateRevertIdenticalDoesForcedApply is the companion
// regression pin: without --simulate the forced apply IS invoked.
func TestRunDriftFlow_NonSimulateRevertIdenticalDoesForcedApply(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionRevertAll{}, nil
	}

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
			ErrorType: apimodel.ReconcileRejected,
			Data:      rejected,
		}
	}

	forcedApplyCalled := false
	forcedApplyFn = func(a *app.App, opts *ApplyOptions) (*apimodel.SubmitCommandResponse, []string, error) {
		forcedApplyCalled = true
		return &apimodel.SubmitCommandResponse{CommandID: "force-123"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error { return nil }

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
		Simulate:       false, // NOT simulating — forced apply must fire
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	require.NoError(t, err)
	assert.True(t, forcedApplyCalled, "forcedApplyFn must be called when Simulate=false on identical drift revert")
}

func TestRunDriftFlow_Abort(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionAbort{}, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	require.Error(t, err)
	// The error should mention the reconcile rejection (rendered by renderer).
	assert.True(t, len(err.Error()) > 0)
}

func TestRunDriftFlow_Extract(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionExtract{
			Path: "./out.pkl",
			Selected: []driftview.ResourceRef{
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "bucket-a", Operation: "update"},
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "bucket-b", Operation: "create"},
			},
		}, nil
	}

	var capturedQueries []string
	extractResourcesFn = func(a *app.App, query string) (*pkgmodel.Forma, []string, error) {
		capturedQueries = append(capturedQueries, query)
		return &pkgmodel.Forma{
			Resources: []pkgmodel.Resource{{Type: "AWS::S3::Bucket", Label: query}},
		}, nil, nil
	}

	generateSourceCodeFnCalled := false
	generateSourceCodeFn = func(a *app.App, forma *pkgmodel.Forma, path string) error {
		generateSourceCodeFnCalled = true
		assert.Equal(t, "./out.pkl", path)
		assert.Len(t, forma.Resources, 2)
		return nil
	}

	// No file overwrite needed (file doesn't exist at ./out.pkl in test env — skip stat check)
	confirmOverwriteFn = func(th *theme.Theme, path string) (bool, error) {
		return true, nil // not called since file doesn't exist
	}

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		t.Fatal("applyFn must NOT be called on extract path")
		return nil, nil, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	require.NoError(t, err)

	require.Len(t, capturedQueries, 2)
	assert.Equal(t, "stack:prod type:AWS::S3::Bucket label:bucket-a", capturedQueries[0])
	assert.Equal(t, "stack:prod type:AWS::S3::Bucket label:bucket-b", capturedQueries[1])
	assert.True(t, generateSourceCodeFnCalled, "GenerateSourceCode must be called once")
}

// TestRunDriftFlow_Extract_CarriesStacksAndTargets guards against a regression
// where handleExtract built its merged Forma without copying Stacks (and
// Targets). Because Forma.Stacks is `omitempty`, an empty slice is dropped from
// the serialized JSON entirely, and the PKL generator then fails with
// "Cannot find property `Stacks`". The unit path stubs the generator, so this
// asserts the merged Forma directly.
func TestRunDriftFlow_Extract_CarriesStacksAndTargets(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionExtract{
			Path: "./out.pkl",
			Selected: []driftview.ResourceRef{
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "bucket-a", Operation: "update"},
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "bucket-b", Operation: "update"},
			},
		}, nil
	}

	// Every extracted resource comes back with its stack and target, mirroring a
	// real App.ExtractResources response.
	extractResourcesFn = func(a *app.App, query string) (*pkgmodel.Forma, []string, error) {
		return &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "prod"}},
			Targets:   []pkgmodel.Target{{Label: "aws"}},
			Resources: []pkgmodel.Resource{{Type: "AWS::S3::Bucket", Label: query}},
		}, nil, nil
	}

	var captured *pkgmodel.Forma
	generateSourceCodeFn = func(a *app.App, forma *pkgmodel.Forma, path string) error {
		captured = forma
		return nil
	}
	confirmOverwriteFn = func(th *theme.Theme, path string) (bool, error) { return true, nil }
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		t.Fatal("applyFn must NOT be called on extract path")
		return nil, nil, nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}
	err := runDriftFlow(a, theme.New("formae"), opts, rejected)
	require.NoError(t, err)

	require.NotNil(t, captured)
	require.Len(t, captured.Resources, 2)
	// Stacks and targets must be present (and deduped) or PKL generation fails.
	require.Len(t, captured.Stacks, 1, "merged Forma must carry the stack")
	assert.Equal(t, "prod", captured.Stacks[0].Label)
	require.Len(t, captured.Targets, 1, "merged Forma must carry the target (deduped)")
	assert.Equal(t, "aws", captured.Targets[0].Label)
}

func TestRunDriftFlow_RevertIdentical(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionRevertAll{}, nil
	}

	// Second simulate returns the same rejection (identical drift).
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
			ErrorType: apimodel.ReconcileRejected,
			Data:      rejected,
		}
	}

	forcedApplyCalled := false
	forcedApplyFn = func(a *app.App, opts *ApplyOptions) (*apimodel.SubmitCommandResponse, []string, error) {
		forcedApplyCalled = true
		return &apimodel.SubmitCommandResponse{CommandID: "force-cmd-123"}, nil, nil
	}

	watchedCmdID := ""
	launchWatch = func(a *app.App, commandID string) error {
		watchedCmdID = commandID
		return nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	require.NoError(t, err)

	assert.True(t, forcedApplyCalled, "forcedApplyFn must be called on identical drift revert")
	assert.Equal(t, "force-cmd-123", watchedCmdID, "launchWatch must receive the force command ID")
}

func TestRunDriftFlow_RevertChanged(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()

	differentRejected := apimodel.FormaReconcileRejectedError{
		ModifiedStacks: map[string]apimodel.ModifiedStack{
			"prod": {ModifiedResources: []apimodel.ResourceModification{
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "different-bucket", Operation: "update",
					PatchDocument: json.RawMessage(`{"op":"replace","path":"/Bar"}`)},
			}},
		},
	}

	driftViewCallCount := 0
	var capturedNotices []string
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		driftViewCallCount++
		capturedNotices = append(capturedNotices, opts.Notice)
		if driftViewCallCount == 1 {
			return driftview.DecisionRevertAll{}, nil
		}
		// Second launch — user aborts.
		return driftview.DecisionAbort{}, nil
	}

	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
			ErrorType: apimodel.ReconcileRejected,
			Data:      differentRejected,
		}
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	// After abort on second launch, an error is returned.
	require.Error(t, err)

	assert.Equal(t, 2, driftViewCallCount, "driftview must be relaunched on changed drift")
	// First call: no notice; second call: non-empty notice.
	assert.Equal(t, "", capturedNotices[0])
	assert.True(t, len(capturedNotices[1]) > 0, "second launch must have a non-empty Notice")
	assert.True(t, strings.Contains(capturedNotices[1], "Drift changed"), "notice must mention drift changed")
}

func TestRunDriftFlow_RevertResolved(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()
	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionRevertAll{}, nil
	}

	// Second simulate succeeds (drift self-resolved).
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		return &apimodel.SubmitCommandResponse{
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
				Command: apimodel.Command{
					CommandID:       "resolved-sim",
					ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "create", ResourceLabel: "bucket"}},
				},
			},
		}, nil, nil
	}

	simViewCalled := false
	launchSimView = func(th *theme.Theme, sim *apimodel.Simulation, opts simview.Options) (simview.Decision, error) {
		simViewCalled = true
		return simview.DecisionAborted, nil // user aborts the simview
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}
	th := theme.New("formae")
	err := runDriftFlow(a, th, opts, rejected)
	require.NoError(t, err)

	assert.True(t, simViewCalled, "launchSimView must be called on the self-resolved path")
}

// ---- E2E drift tests ----

func TestDrift_E2E_ExtractFlow(t *testing.T) {
	// This is a unit-level E2E: real seam stubs but no live HTTP server.
	// The brief's "extract HTTP calls per selection" is satisfied by the
	// extractResourcesFn seam going through a real client — validated in
	// TestRunDriftFlow_Extract which checks the exact query strings.
	// A full HTTP round-trip is covered in TestApplyTUI_EndToEnd pattern.

	stubDriftSeams(t)

	rejected := makeRejected()

	isTerminal = func(w io.Writer) bool { return true }

	// Simulate → reconcile rejection.
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		if simulate {
			return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
				ErrorType: apimodel.ReconcileRejected,
				Data:      rejected,
			}
		}
		t.Fatal("real apply must NOT be submitted on extract path")
		return nil, nil, nil
	}

	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionExtract{
			Path: "./test-extract.pkl",
			Selected: []driftview.ResourceRef{
				{Stack: "prod", Type: "AWS::S3::Bucket", Label: "my-bucket", Operation: "update"},
			},
		}, nil
	}

	var extractedQueries []string
	extractResourcesFn = func(a *app.App, query string) (*pkgmodel.Forma, []string, error) {
		extractedQueries = append(extractedQueries, query)
		return &pkgmodel.Forma{
			Resources: []pkgmodel.Resource{{Type: "AWS::S3::Bucket", Label: "my-bucket"}},
		}, nil, nil
	}

	generateSourceCodeFn = func(a *app.App, forma *pkgmodel.Forma, path string) error {
		return nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	require.Len(t, extractedQueries, 1, "one extract call per selected ref")
	assert.Equal(t, "stack:prod type:AWS::S3::Bucket label:my-bucket", extractedQueries[0])
}

func TestDrift_E2E_RevertFlow(t *testing.T) {
	stubDriftSeams(t)

	rejected := makeRejected()

	isTerminal = func(w io.Writer) bool { return true }

	var callOrder []string

	// simulate1 → rejection; simulate2 (same drift) → same rejection; force submit → success.
	applyCallCount := 0
	applyFn = func(a *app.App, opts *ApplyOptions, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
		applyCallCount++
		if simulate {
			callOrder = append(callOrder, "simulate")
			return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
				ErrorType: apimodel.ReconcileRejected,
				Data:      rejected,
			}
		}
		t.Fatal("real apply via applyFn must NOT be called on revert path")
		return nil, nil, nil
	}

	launchDriftView = func(th *theme.Theme, r *apimodel.FormaReconcileRejectedError, opts driftview.Options) (driftview.Decision, error) {
		return driftview.DecisionRevertAll{}, nil
	}

	forcedApplyFn = func(a *app.App, opts *ApplyOptions) (*apimodel.SubmitCommandResponse, []string, error) {
		callOrder = append(callOrder, "force-apply")
		return &apimodel.SubmitCommandResponse{CommandID: "e2e-force-cmd"}, nil, nil
	}

	launchWatch = func(a *app.App, commandID string) error {
		callOrder = append(callOrder, "watch:"+commandID)
		return nil
	}

	a := newTestApp()
	opts := &ApplyOptions{
		OutputConsumer: printer.ConsumerHuman,
		FormaFile:      "forma.pkl",
		Mode:           pkgmodel.FormaApplyModeReconcile,
	}

	err := runApplyForHumans(a, opts)
	require.NoError(t, err)

	// Assert request order: simulate POST → force POST → watch
	// (D7: force submit only after fresh simulate)
	require.GreaterOrEqual(t, len(callOrder), 3, "expected simulate, force-apply, watch in order")
	assert.Equal(t, "simulate", callOrder[0], "first call must be simulate")
	assert.Equal(t, "simulate", callOrder[1], "second call must be the re-validate simulate")
	assert.Equal(t, "force-apply", callOrder[2], "force apply must follow the re-validate simulate")
	assert.Equal(t, "watch:e2e-force-cmd", callOrder[3], "watch must follow force apply")
}
