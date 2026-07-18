// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package components

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// visibleWidth returns the display width of a string, ANSI-aware.
func visibleWidth(s string) int {
	return lipgloss.Width(s)
}

// Piped output (non-TTY): result lines ONLY — no start line, no ANSI (D9/R10).
func TestStepLine_PipedPrintsResultOnly(t *testing.T) {
	th := theme.New("formae")
	var buf bytes.Buffer
	s := StartStep(&buf, th, "Installing cloudflare…")
	s.Done("Installed cloudflare 1.2.3")
	out := buf.String()
	assert.NotContains(t, out, "Installing")
	assert.Contains(t, out, "Installed cloudflare 1.2.3")
	assert.NotContains(t, out, "\x1b[", "piped output must be ANSI-free")
	assert.NotContains(t, out, "\r")
	assert.True(t, strings.HasSuffix(out, "\n"))
}

// TTY mode: frame line truncates to width, rewrite clears to EOL (D9/R9).
func TestStepLine_TTYFrameLineTruncatesAndClears(t *testing.T) {
	th := theme.New("formae")
	restoreTTY, restoreWidth := stepIsTerminal, stepTermWidth
	stepIsTerminal = func(io.Writer) bool { return true }
	stepTermWidth = func(io.Writer) int { return 20 }
	defer func() { stepIsTerminal, stepTermWidth = restoreTTY, restoreWidth }()

	var buf bytes.Buffer
	s := StartStep(&buf, th, "Installing github.com/acme/formae-plugin-cloudflare…")
	s.Done("Installed cloudflare 1.2.3")
	out := buf.String()
	require.Contains(t, out, "\r\x1b[K", "rewrite must clear to EOL")
	// The animated frame line never exceeds the terminal width.
	frame := stepFrameLine(th, "⠋", "Installing github.com/acme/formae-plugin-cloudflare…", 20)
	assert.LessOrEqual(t, visibleWidth(frame), 20)
	assert.True(t, strings.HasSuffix(out, "Installed cloudflare 1.2.3\n"))
}

// Sequential contract: finishing twice panics loudly rather than corrupting output.
func TestStepLine_DoubleFinishPanics(t *testing.T) {
	th := theme.New("formae")
	var buf bytes.Buffer
	s := StartStep(&buf, th, "step")
	s.Done("done")
	assert.Panics(t, func() { s.Fail("again") })
}
