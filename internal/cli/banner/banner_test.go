// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package banner

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/logo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout redirects os.Stdout to a buffer for the duration of f, then
// restores it and returns what was written.
func captureStdout(f func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// TestPrintBanner_SuppressedNonTTY verifies that PrintBanner produces NO output
// when stdout is not a TTY (piped output, CI, machine consumers).
func TestPrintBanner_SuppressedNonTTY(t *testing.T) {
	origIsTerminal := isTerminal
	origDetect := detect
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		detect = origDetect
	})

	// Stub: stdout is not a TTY.
	isTerminal = func(_ io.Writer) bool { return false }
	// Stub detect to confirm it is never called (suppression happens before it).
	detectCalled := false
	detect = func() logo.Capability {
		detectCalled = true
		return logo.CapText
	}

	out := captureStdout(func() { PrintBanner() })

	assert.Empty(t, out, "PrintBanner must produce no output when stdout is not a TTY")
	assert.False(t, detectCalled, "detect should not be called when suppressed")
}

// TestPrintBanner_TextFallback verifies that when the terminal is a TTY and
// capability is CapText the banner contains "formae v{version}".
func TestPrintBanner_TextFallback(t *testing.T) {
	origIsTerminal := isTerminal
	origDetect := detect
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		detect = origDetect
	})

	isTerminal = func(_ io.Writer) bool { return true }
	detect = func() logo.Capability { return logo.CapText }

	out := captureStdout(func() { PrintBanner() })

	want := "formae v" + formae.Version
	require.NotEmpty(t, out, "PrintBanner must produce output for CapText on a TTY")
	assert.True(t, strings.Contains(out, want),
		"output %q must contain %q", out, want)
}
