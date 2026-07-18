// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tui

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultRunOptions(t *testing.T) {
	opts := DefaultRunOptions()
	assert.True(t, opts.AltScreen)
}

func TestBuildProgramOptions(t *testing.T) {
	var buf bytes.Buffer
	opts := DefaultRunOptions()
	opts.Output = &buf
	progOpts := buildProgramOptions(opts)
	assert.NotEmpty(t, progOpts, "should produce at least one program option")
}

func TestBuildProgramOptions_NoAltScreen(t *testing.T) {
	var buf bytes.Buffer
	opts := DefaultRunOptions()
	opts.AltScreen = false
	opts.Output = &buf
	progOpts := buildProgramOptions(opts)
	// Should have output option but not alt screen
	assert.NotEmpty(t, progOpts)
}

func TestIsTerminal_WithFile(t *testing.T) {
	// /dev/null is not a terminal
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("cannot open /dev/null")
	}
	defer func() { _ = f.Close() }()

	assert.False(t, IsTerminal(f))
}

func TestIsTerminal_WithBuffer(t *testing.T) {
	var buf bytes.Buffer
	assert.False(t, IsTerminal(&buf))
}

// TestIsInteractive documents that IsInteractive checks both stdin and stdout.
// In the test runner neither is a TTY, so it must return false.
func TestIsInteractive(t *testing.T) {
	// Under go test, stdin/stdout are not terminals — result must be false.
	assert.False(t, IsInteractive())
}
