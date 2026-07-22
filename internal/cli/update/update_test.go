// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package update

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
)

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

type stubInstaller struct {
	stopCalled    bool
	stopErr       error
	installCalled bool
	installPkg    string
	installErr    error
}

func (s *stubInstaller) stop() error {
	s.stopCalled = true
	return s.stopErr
}

func (s *stubInstaller) install(pkg string) error {
	s.installCalled = true
	s.installPkg = pkg
	return s.installErr
}

// captureWriter records all writes and exposes them as a lowercase string
// so assertion comparisons are case-insensitive.
type captureWriter struct {
	bytes.Buffer
}

func (c *captureWriter) lower() string {
	return strings.ToLower(c.Buffer.String())
}

func seamsFor(stub *stubInstaller, interactive bool, confirmResult bool) updateSeams {
	return updateSeams{
		isInteractiveFn: func() bool { return interactive },
		runConfirmFn: func(_ *theme.Theme, _, _ string) (bool, error) {
			return confirmResult, nil
		},
		stopAgentFn: stub.stop,
		installFn:   stub.install,
	}
}

// ----------------------------------------------------------------------------
// D8 gate tests — runUpdateFlow
// ----------------------------------------------------------------------------

// Non-TTY without --yes must error with "interactive input requires a TTY".
func TestUpdateFlow_NonTTY_NoYes(t *testing.T) {
	stub := &stubInstaller{}
	var buf captureWriter
	th := theme.New("formae")

	err := runUpdateFlow(&buf, th, seamsFor(stub, false, false), "0.83.0", "pkg-id", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
	assert.False(t, stub.installCalled, "install must not run on D8 error path")
	assert.False(t, stub.stopCalled, "stop must not run on D8 error path")
}

// Non-TTY with --yes must proceed without a confirm prompt.
func TestUpdateFlow_NonTTY_WithYes(t *testing.T) {
	stub := &stubInstaller{}
	var buf captureWriter
	th := theme.New("formae")

	err := runUpdateFlow(&buf, th, seamsFor(stub, false, false), "0.83.0", "pkg-id", true)
	require.NoError(t, err)
	assert.True(t, stub.stopCalled, "agent stop must run")
	assert.True(t, stub.installCalled, "install must run")
}

// TTY with confirm answered No must return without installing.
func TestUpdateFlow_TTY_ConfirmDeclined(t *testing.T) {
	stub := &stubInstaller{}
	var buf captureWriter
	th := theme.New("formae")

	err := runUpdateFlow(&buf, th, seamsFor(stub, true, false /* No */), "0.83.0", "pkg-id", false)
	require.NoError(t, err)
	assert.False(t, stub.installCalled, "install must not run when confirm is declined")
	assert.False(t, stub.stopCalled, "agent stop must not run when confirm is declined")
}

// Consequence sentence must appear in output BEFORE runConfirmFn is invoked.
func TestUpdateFlow_ConsequenceBeforeConfirm(t *testing.T) {
	stub := &stubInstaller{}
	th := theme.New("formae")

	var outputBeforeConfirm string
	cw := &captureWriter{}

	seams := updateSeams{
		isInteractiveFn: func() bool { return true },
		runConfirmFn: func(_ *theme.Theme, _, _ string) (bool, error) {
			// Snapshot output at the moment confirm fires.
			outputBeforeConfirm = strings.ToLower(cw.Buffer.String())
			return false, nil // decline → no install needed
		},
		stopAgentFn: stub.stop,
		installFn:   stub.install,
	}

	_ = runUpdateFlow(cw, th, seams, "0.83.0", "pkg-id", false)

	assert.Contains(t, outputBeforeConfirm, "updating stops the local formae agent",
		"consequence sentence must appear in output BEFORE the confirm call")
}

// ----------------------------------------------------------------------------
// D8 gate tests — runInitConfirmDecision
// ----------------------------------------------------------------------------

// Non-TTY without --yes must error with "interactive input requires a TTY".
func TestInitConfirm_NonTTY_NoYes(t *testing.T) {
	stub := &stubInstaller{}
	var buf captureWriter
	th := theme.New("formae")

	_, err := runInitConfirmDecision(&buf, th, seamsFor(stub, false, false), "/some/path", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
}

// Non-TTY with --yes must proceed (return true, nil) without calling confirm.
func TestInitConfirm_NonTTY_WithYes(t *testing.T) {
	stub := &stubInstaller{}
	var confirmCalled bool

	seams := updateSeams{
		isInteractiveFn: func() bool { return false },
		runConfirmFn: func(_ *theme.Theme, _, _ string) (bool, error) {
			confirmCalled = true
			return true, nil
		},
		stopAgentFn: stub.stop,
		installFn:   stub.install,
	}

	var buf captureWriter
	th := theme.New("formae")

	result, err := runInitConfirmDecision(&buf, th, seams, "/some/path", true)
	require.NoError(t, err)
	assert.True(t, result, "should proceed when --yes on non-TTY")
	assert.False(t, confirmCalled, "confirm must not be called with --yes")
}

// ----------------------------------------------------------------------------
// formatAvailableVersions tests (cold-index correctness — #555)
// ----------------------------------------------------------------------------

func pkg(major, minor, patch uint64, installed bool) *records.Package {
	return &records.Package{
		Installed: installed,
		Header: &ops.Header{
			Version: &ops.Version{
				Major:     major,
				Minor:     minor,
				Patch:     patch,
				Timestamp: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
}

// A cold index with a single, not-yet-installed version must still list that
// version. orbital's AvailableForSimple drops the candidate at index 0, so a
// cold index rendered an empty list; formatAvailableVersions must not.
func TestFormatAvailableVersions_ColdSingleCandidateNotDropped(t *testing.T) {
	out := formatAvailableVersions([]*records.Package{pkg(0, 88, 0, false)})

	if !strings.Contains(out, "0.88.0") {
		t.Errorf("expected the only available version 0.88.0 to be listed, got:\n%s", out)
	}
	if strings.Contains(out, "installed:") {
		t.Errorf("expected no installed line when nothing is installed, got:\n%s", out)
	}
}

// With multiple not-installed candidates and nothing installed, every distinct
// version must appear (AvailableForSimple would omit the first).
func TestFormatAvailableVersions_MultipleCandidatesAllListed(t *testing.T) {
	out := formatAvailableVersions([]*records.Package{
		pkg(0, 88, 0, false),
		pkg(0, 87, 1, false),
	})

	for _, want := range []string{"0.88.0", "0.87.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected version %s to be listed, got:\n%s", want, out)
		}
	}
}

// The installed version is surfaced on its own line and de-duplicated in the
// available list.
func TestFormatAvailableVersions_InstalledSurfacedAndDeduped(t *testing.T) {
	out := formatAvailableVersions([]*records.Package{
		pkg(0, 87, 1, true),
		pkg(0, 87, 1, false), // same version present in both tree and repo
		pkg(0, 88, 0, false),
	})

	if !strings.Contains(out, "installed: 0.87.1") {
		t.Errorf("expected installed line for 0.87.1, got:\n%s", out)
	}
	if !strings.Contains(out, "0.88.0") {
		t.Errorf("expected available version 0.88.0, got:\n%s", out)
	}
	if n := strings.Count(out, "0.87.1"); n != 2 { // installed line + one deduped entry
		t.Errorf("expected 0.87.1 exactly twice (installed line + one list entry), got %d in:\n%s", n, out)
	}
}
