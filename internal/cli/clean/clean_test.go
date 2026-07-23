// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package clean

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func assertNoSpinner(t *testing.T, out string) {
	t.Helper()
	assert.NotContains(t, out, "\r", "piped output must not carry carriage returns")
	assert.NotContains(t, out, "\x1b[", "piped output must be ANSI-free")
	for _, f := range spinnerFrames {
		assert.NotContains(t, out, f, "piped output must not contain spinner frames")
	}
}

// TestRunClean_PipedDefault verifies the piped (non-TTY) default path: the
// clean op runs, a single ✓ result line, no ANSI, no spinner frames.
func TestRunClean_PipedDefault(t *testing.T) {
	var buf bytes.Buffer
	cleanCalled, clearCalled := false, false
	err := runClean(&buf, theme.New("formae"), false,
		func() error { cleanCalled = true; return nil },
		func() error { clearCalled = true; return nil })
	require.NoError(t, err)

	assert.True(t, cleanCalled, "default path must call Clean")
	assert.False(t, clearCalled, "default path must not call Clear")

	out := buf.String()
	assertNoSpinner(t, out)
	assert.Contains(t, out, "✓ removed old versions")
	assert.NotContains(t, out, "metadata")
}

// TestRunClean_PipedAll verifies --all routes to Clear and reflects the wider
// scope in the result line.
func TestRunClean_PipedAll(t *testing.T) {
	var buf bytes.Buffer
	cleanCalled, clearCalled := false, false
	err := runClean(&buf, theme.New("formae"), true,
		func() error { cleanCalled = true; return nil },
		func() error { clearCalled = true; return nil })
	require.NoError(t, err)

	assert.False(t, cleanCalled, "--all path must not call Clean")
	assert.True(t, clearCalled, "--all path must call Clear")

	out := buf.String()
	assertNoSpinner(t, out)
	assert.Contains(t, out, "✓ removed old versions and update metadata")
}

// TestRunClean_ErrorFlipsToFail verifies a failing op emits ✗ and returns the error.
func TestRunClean_ErrorFlipsToFail(t *testing.T) {
	var buf bytes.Buffer
	boom := errors.New("permission denied")
	err := runClean(&buf, theme.New("formae"), false,
		func() error { return boom },
		func() error { return nil })
	require.ErrorIs(t, err, boom)

	out := buf.String()
	assert.Contains(t, out, "✗")
	assert.Contains(t, out, "failed to clean up old versions")
	assert.NotContains(t, out, "✓")
}
