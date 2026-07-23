// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// loopOpts builds a base PluginInitOptions suitable for interactive-loop tests.
// NoInput=false so the form / availability loop runs, not the --no-input path.
// The HubClient and TemplateDownloader fields are set by the caller as needed.
func loopOpts(name string, hubClient HubClient, dl TemplateDownloader, overrides func(*PluginInitOptions)) *PluginInitOptions {
	opts := &PluginInitOptions{
		Name:               name,
		Namespace:          "FOO",
		Description:        "desc",
		Category:           "other",
		Author:             "Tester",
		ModulePath:         "github.com/test/formae-plugin-foo",
		License:            "Apache-2.0",
		NoInput:            false,
		HubClient:          hubClient,
		TemplateDownloader: dl,
	}
	if overrides != nil {
		overrides(opts)
	}
	return opts
}

// ---------------------------------------------------------------------------
// hubCheckResult (unit-test the availability classifier directly)
// ---------------------------------------------------------------------------

func TestHubCheckResult_Available(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: true}}
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "foo", false /*noCheck*/, false /*explicitHub*/, &buf)
	assert.False(t, res.taken)
	assert.False(t, res.unchecked)
	assert.NoError(t, res.err)
	assert.Equal(t, "", res.registrant)
}

func TestHubCheckResult_Taken(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "foo", false, false, &buf)
	assert.True(t, res.taken)
	assert.False(t, res.unchecked)
	assert.Equal(t, "https://github.com/x/y", res.registrant)
	assert.NoError(t, res.err) // taken is not a hard error, loop decides
}

func TestHubCheckResult_TakenAllowConflict(t *testing.T) {
	// allowConflict is handled by the caller (runOneAvailabilityIteration's switch);
	// classifyAvailability always returns taken=true when the name is registered,
	// regardless of the caller's allow-conflict intent.
	fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "foo", false /*noCheck*/, false /*explicitHub*/, &buf)
	// taken is returned so the caller can decide whether to warn-and-proceed or re-open.
	assert.True(t, res.taken)
	assert.NoError(t, res.err)
}

func TestHubCheckResult_Transient_Unchecked(t *testing.T) {
	fc := &fakeHubClient{err: &HubTransientError{Cause: errors.New("hub returned HTTP 503")}}
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "foo", false /*noCheck*/, false /*explicitHub*/, &buf)
	assert.True(t, res.unchecked)
	assert.False(t, res.taken)
	assert.NoError(t, res.err)
}

func TestHubCheckResult_Unreachable_DefaultHub_Unchecked(t *testing.T) {
	fc := &fakeHubClient{err: &HubUnreachableError{Cause: errors.New("connection refused")}}
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "foo", false /*noCheck*/, false /*explicitHub=false*/, &buf)
	assert.True(t, res.unchecked)
	assert.NoError(t, res.err)
}

func TestHubCheckResult_Unreachable_ExplicitHub_HardError(t *testing.T) {
	fc := &fakeHubClient{err: &HubUnreachableError{Cause: errors.New("connection refused")}}
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "foo", false /*noCheck*/, true /*explicitHub*/, &buf)
	assert.False(t, res.unchecked)
	assert.False(t, res.taken)
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "hub availability check failed")
}

func TestHubCheckResult_NoCheck_Unchecked(t *testing.T) {
	// When noCheck=true, classifyAvailability must return unchecked=true without
	// calling the hub client at all.
	fc := &panicHubClient{} // panics if CheckPluginAvailability is called
	var buf bytes.Buffer
	res := classifyAvailability(context.Background(), fc, "any-name", true /*noCheck*/, false /*explicitHub*/, &buf)
	assert.True(t, res.unchecked, "noCheck=true must set unchecked")
	assert.False(t, res.taken, "noCheck must not report taken")
	assert.NoError(t, res.err, "noCheck must not return a hard error")
}

// ---------------------------------------------------------------------------
// D8: non-TTY without --no-input must return a descriptive error
// ---------------------------------------------------------------------------

func TestInteractiveLoop_NonTTY_ReturnsD8Error(t *testing.T) {
	// runPluginInit with NoInput=false and a non-TTY environment (the test
	// runner is never a TTY) must fail fast naming --no-input.
	//
	// We set a no-op HubClient so the test doesn't hit the real Hub if
	// somehow the D8 gate is missing.
	fc := &fakeHubClient{res: AvailabilityResult{Available: true}}
	dl := &spyDownloader{}
	opts := loopOpts("myplugin", fc, dl, nil)
	// NoInput=false (interactive) but no TTY → D8 error.
	opts.NoAvailabilityCheck = true // so availability doesn't interfere

	err := runPluginInit(context.Background(), opts)
	require.Error(t, err, "non-TTY interactive run must fail")
	assert.Contains(t, err.Error(), "--no-input",
		"D8 error must name --no-input so user knows how to fix it")
	// Downloader must NOT be called — we failed before scaffolding.
	assert.Equal(t, 0, dl.calls, "downloader must not be called when D8 gate fires")
}

// ---------------------------------------------------------------------------
// Availability-loop decision logic (via runAvailabilityLoop which is the
// extracted loop body — testable without form.Run())
// ---------------------------------------------------------------------------

// TestAvailabilityLoop_Available_Proceeds verifies that when the hub reports
// the name available, the loop body returns no error and a break signal.
func TestAvailabilityLoop_Available_Proceeds(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: true}}
	var buf bytes.Buffer
	nameErr, hardErr := runOneAvailabilityIteration(context.Background(), fc, "foo", false, false, false, theme.New("formae"), &buf)
	assert.Equal(t, "", nameErr, "no nameErr on available name")
	assert.NoError(t, hardErr)
}

// TestAvailabilityLoop_Taken_ReturnsNameErr verifies that a taken name sets
// nameErr (so the loop re-opens the form with the error pre-marked) and
// hardErr is nil.
func TestAvailabilityLoop_Taken_ReturnsNameErr(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
	var buf bytes.Buffer
	nameErr, hardErr := runOneAvailabilityIteration(context.Background(), fc, "taken-name", false, false, false, theme.New("formae"), &buf)
	require.NotEmpty(t, nameErr, "taken name must set nameErr for form re-open")
	assert.Contains(t, nameErr, "taken-name")
	assert.Contains(t, nameErr, "https://github.com/x/y")
	assert.NoError(t, hardErr, "taken (no --allow-conflict) is not a hard error in the loop")
}

// TestAvailabilityLoop_Taken_AllowConflict_ProceedsWithWarning verifies that
// taken + --allow-conflict emits a warning (R6) and the loop does not re-open
// (nameErr is empty, hardErr is nil).
func TestAvailabilityLoop_Taken_AllowConflict_ProceedsWithWarning(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
	var buf bytes.Buffer
	nameErr, hardErr := runOneAvailabilityIteration(context.Background(), fc, "taken-name", true /*allowConflict*/, false, false, theme.New("formae"), &buf)
	assert.Equal(t, "", nameErr, "allow-conflict must NOT re-open the form")
	assert.NoError(t, hardErr)
	// The warning must have been written.
	assert.Contains(t, buf.String(), "already registered",
		"allow-conflict must emit 'already registered' warning")
}

// TestAvailabilityLoop_Transient_Unchecked verifies that a transient hub error
// sets unchecked (○) and does NOT re-open the form (nameErr empty, no hardErr).
func TestAvailabilityLoop_Transient_Unchecked(t *testing.T) {
	fc := &fakeHubClient{err: &HubTransientError{Cause: errors.New("hub returned HTTP 503")}}
	var buf bytes.Buffer
	nameErr, hardErr := runOneAvailabilityIteration(context.Background(), fc, "foo", false, false, false, theme.New("formae"), &buf)
	assert.Equal(t, "", nameErr, "transient error must NOT re-open the form")
	assert.NoError(t, hardErr)
	assert.Contains(t, buf.String(), "unchecked",
		"transient error must emit unchecked/Hub-will-re-validate message")
}

// TestAvailabilityLoop_NoAvailabilityCheck_SkipsHubCall verifies that when
// NoAvailabilityCheck=true, the hub client is never called, and we get
// an unchecked step (nameErr empty, no hardErr).
func TestAvailabilityLoop_NoAvailabilityCheck_SkipsHubCall(t *testing.T) {
	// Pass a client that panics if called.
	fc := &panicHubClient{}
	var buf bytes.Buffer
	nameErr, hardErr := runOneAvailabilityIteration(context.Background(), fc, "foo", false, true /*noCheck*/, false, theme.New("formae"), &buf)
	assert.Equal(t, "", nameErr)
	assert.NoError(t, hardErr)
}

// TestAvailabilityLoop_ExplicitHub_Unreachable_HardError verifies that an
// explicit hub that is unreachable returns a hard error (no loop re-open).
func TestAvailabilityLoop_ExplicitHub_Unreachable_HardError(t *testing.T) {
	fc := &fakeHubClient{err: &HubUnreachableError{Cause: errors.New("connection refused")}}
	var buf bytes.Buffer
	nameErr, hardErr := runOneAvailabilityIteration(context.Background(), fc, "foo", false, false, true /*explicit*/, theme.New("formae"), &buf)
	assert.Equal(t, "", nameErr)
	require.Error(t, hardErr)
	assert.Contains(t, hardErr.Error(), "hub availability check failed")
}

// TestAvailabilityLoop_FieldsPreservedAcrossReopen verifies R5 via the runFormFn
// seam: runPluginInitInteractive's loop reuses the SAME v across iterations so
// non-name fields (Namespace, Description, Author, etc.) survive the re-open.
//
// Seam contract (runFormFn receives *initFormValues):
//   - iteration 1: v comes pre-filled from opts; form stub changes only Name to
//     the taken value (simulating user leaving other fields unchanged). Hub returns taken.
//   - iteration 2: form stub asserts non-name fields are still set, then switches
//     Name to a free name so the loop exits.
func TestAvailabilityLoop_FieldsPreservedAcrossReopen(t *testing.T) {
	// Hub: taken on first call, available on second.
	hubCalls := 0
	fc := &fakeHubClientFn{fn: func(_ context.Context, _ string) (AvailabilityResult, error) {
		hubCalls++
		if hubCalls == 1 {
			return AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}, nil
		}
		return AvailabilityResult{Available: true}, nil
	}}

	formCalls := 0
	orig := runFormFn
	t.Cleanup(func() { runFormFn = orig })
	runFormFn = func(_ *theme.Theme, v *initFormValues, _ string) error {
		formCalls++
		switch formCalls {
		case 1:
			// Iteration 1: opts pre-filled all non-name fields; just confirm name and leave it.
			// (v.Name == "taken-name" from opts; hub will return taken.)
		case 2:
			// Iteration 2: v is the SAME struct — non-name fields must be unchanged.
			assert.Equal(t, "MYPLUGIN", v.Namespace, "Namespace must survive re-open")
			assert.Equal(t, "my important description", v.Description, "Description must survive re-open")
			assert.Equal(t, "cloud", v.Category, "Category must survive re-open")
			assert.Equal(t, "Acme Corp", v.Author, "Author must survive re-open")
			assert.Equal(t, "github.com/acme/formae-plugin-myplugin", v.ModulePath, "ModulePath must survive re-open")
			assert.Equal(t, "MIT", v.License, "License must survive re-open")
			// Simulate user picking a free name.
			v.Name = "new-name"
		}
		return nil
	}

	dl := &spyDownloader{}
	opts := &PluginInitOptions{
		Name:                "taken-name",
		Namespace:           "MYPLUGIN",
		Description:         "my important description",
		Category:            "cloud",
		Author:              "Acme Corp",
		ModulePath:          "github.com/acme/formae-plugin-myplugin",
		License:             "MIT",
		NoInput:             false,
		NoAvailabilityCheck: false,
		HubClient:           fc,
		TemplateDownloader:  dl,
	}

	// Drive the interactive loop directly (bypasses the TTY gate in runPluginInit).
	err := runPluginInitInteractive(context.Background(), opts)

	assert.Equal(t, 2, formCalls, "form seam must be called twice (re-open after taken)")
	assert.Equal(t, 2, hubCalls, "hub must be called once per iteration")

	// Accept nil or a scaffolding error; the loop must NOT return an availability error.
	if err != nil {
		assert.NotContains(t, err.Error(), "hub availability",
			"loop must not surface availability error after successful name resolution")
	}
}

// ---------------------------------------------------------------------------
// stepline output checks: verify that progress lines contain expected text
// (done, unchecked, taken markers)
// ---------------------------------------------------------------------------

func TestAvailabilityLoop_Available_StepOutput(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: true}}
	var buf bytes.Buffer
	_, _ = runOneAvailabilityIteration(context.Background(), fc, "myplugin", false, false, false, theme.New("formae"), &buf)
	out := buf.String()
	// Should contain the "available" or "✓" marker.
	assert.True(t, strings.Contains(out, "available") || strings.Contains(out, "✓"),
		"available step must render a done marker; got: %q", out)
}

func TestAvailabilityLoop_Taken_StepOutput(t *testing.T) {
	fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
	var buf bytes.Buffer
	_, _ = runOneAvailabilityIteration(context.Background(), fc, "taken-name", false, false, false, theme.New("formae"), &buf)
	out := buf.String()
	// Should contain a taken/✗ marker.
	assert.True(t, strings.Contains(out, "taken") || strings.Contains(out, "✗"),
		"taken step must render a fail marker; got: %q", out)
}

func TestAvailabilityLoop_NoCheck_StepOutput(t *testing.T) {
	fc := &panicHubClient{}
	var buf bytes.Buffer
	_, _ = runOneAvailabilityIteration(context.Background(), fc, "foo", false, true, false, theme.New("formae"), &buf)
	out := buf.String()
	assert.True(t, strings.Contains(out, "unchecked") || strings.Contains(out, "○") || strings.Contains(out, "·"),
		"no-check step must render an unchecked marker; got: %q", out)
}

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

// panicHubClient is a HubClient that panics if CheckPluginAvailability is called.
type panicHubClient struct{}

func (p *panicHubClient) CheckPluginAvailability(_ context.Context, name string) (AvailabilityResult, error) {
	panic("panicHubClient: CheckPluginAvailability must not be called for name=" + name)
}

// fakeHubClientFn is a HubClient backed by an arbitrary function, used when
// the response must vary across calls (e.g., taken on first call, available on second).
type fakeHubClientFn struct {
	fn func(context.Context, string) (AvailabilityResult, error)
}

func (f *fakeHubClientFn) CheckPluginAvailability(ctx context.Context, name string) (AvailabilityResult, error) {
	return f.fn(ctx, name)
}
