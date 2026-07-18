// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package update

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
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
