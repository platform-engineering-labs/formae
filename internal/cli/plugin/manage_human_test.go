// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

// ── stubs ────────────────────────────────────────────────────────────────────

type stubInstaller struct{ err error }

func (s *stubInstaller) LocalInstall(_ []string) error { return s.err }

type stubUninstaller struct{ err error }

func (s *stubUninstaller) LocalUninstall(_ []string) error { return s.err }

type stubUpdater struct{ err error }

func (s *stubUpdater) LocalUpdate(_ []string) error { return s.err }

// ── install human path ───────────────────────────────────────────────────────

// TestInstallHuman_PipedLines verifies the piped (non-TTY) output of the human
// install path: result lines only, ✓ per package, ! hint, no ANSI, no \r.
func TestInstallHuman_PipedLines(t *testing.T) {
	var buf bytes.Buffer
	opts := &InstallOptions{
		Packages:       []string{"cloudflare", "aws@1.2.3"},
		OutputConsumer: "human",
	}
	err := runInstallForHumansWithSeams(nil, opts, &buf, &stubInstaller{})
	require.NoError(t, err)
	out := buf.String()

	// Should not contain \r or ANSI — stepIsTerminal returns false for *bytes.Buffer.
	assert.NotContains(t, out, "\r")
	assert.NotContains(t, out, "\x1b[", "piped output must be ANSI-free")

	// Summary step result line (Done): "✓ Installed 2 plugins\n"
	assert.Contains(t, out, "✓ Installed 2 plugins")

	// Per-package ack lines (piped: no ANSI, plain glyph + text).
	assert.Contains(t, out, "✓ Installed cloudflare")
	assert.Contains(t, out, "✓ Installed aws 1.2.3")

	// Restart hint uses ! marker.
	assert.Contains(t, out, "!")
	assert.Contains(t, out, "restart")

	// No spinner frames on piped output.
	for _, frame := range []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"} {
		assert.NotContains(t, out, frame)
	}
}

// TestInstallHuman_SinglePlugin verifies the singular noun form.
func TestInstallHuman_SinglePlugin(t *testing.T) {
	var buf bytes.Buffer
	opts := &InstallOptions{
		Packages:       []string{"cloudflare"},
		OutputConsumer: "human",
	}
	err := runInstallForHumansWithSeams(nil, opts, &buf, &stubInstaller{})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "✓ Installed 1 plugin")
	assert.Contains(t, out, "✓ Installed cloudflare")
}

// TestInstallHuman_ErrorFlipsToFail verifies that when LocalInstall fails the
// step emits ✗ and the error is returned (not swallowed).
func TestInstallHuman_ErrorFlipsToFail(t *testing.T) {
	var buf bytes.Buffer
	opts := &InstallOptions{
		Packages:       []string{"aws"},
		OutputConsumer: "human",
	}
	boom := errors.New("orbital: package not found")
	err := runInstallForHumansWithSeams(nil, opts, &buf, &stubInstaller{err: boom})
	require.ErrorIs(t, err, boom)
	out := buf.String()
	assert.Contains(t, out, "✗")
	// No success lines should appear after failure.
	assert.NotContains(t, out, "✓ Installed")
}

// ── uninstall human path ─────────────────────────────────────────────────────

func TestUninstallHuman_PipedLines(t *testing.T) {
	var buf bytes.Buffer
	opts := &UninstallOptions{
		Packages:       []string{"cloudflare", "aws"},
		OutputConsumer: "human",
	}
	err := runUninstallForHumansWithSeams(nil, opts, &buf, &stubUninstaller{})
	require.NoError(t, err)
	out := buf.String()

	assert.NotContains(t, out, "\r")
	assert.NotContains(t, out, "\x1b[", "piped output must be ANSI-free")

	assert.Contains(t, out, "✓ Removed 2 plugins")
	assert.Contains(t, out, "✓ Removed cloudflare")
	assert.Contains(t, out, "✓ Removed aws")
	assert.Contains(t, out, "!")
	assert.Contains(t, out, "restart")
}

func TestUninstallHuman_ErrorFlipsToFail(t *testing.T) {
	var buf bytes.Buffer
	opts := &UninstallOptions{
		Packages:       []string{"aws"},
		OutputConsumer: "human",
	}
	boom := errors.New("not installed")
	err := runUninstallForHumansWithSeams(nil, opts, &buf, &stubUninstaller{err: boom})
	require.ErrorIs(t, err, boom)
	out := buf.String()
	assert.Contains(t, out, "✗")
	assert.NotContains(t, out, "✓ Removed")
}

// ── update human path ────────────────────────────────────────────────────────

func TestUpdateHuman_PipedLines_Named(t *testing.T) {
	var buf bytes.Buffer
	opts := &UpdateOptions{
		Packages:       []string{"aws", "cloudflare"},
		OutputConsumer: "human",
	}
	err := runUpdateForHumansWithSeams(nil, opts, &buf, &stubUpdater{})
	require.NoError(t, err)
	out := buf.String()

	assert.NotContains(t, out, "\r")
	assert.NotContains(t, out, "\x1b[", "piped output must be ANSI-free")

	assert.Contains(t, out, "✓ Updated 2 plugins")
	assert.Contains(t, out, "✓ Updated aws")
	assert.Contains(t, out, "✓ Updated cloudflare")
	assert.Contains(t, out, "!")
	assert.Contains(t, out, "restart")
}

func TestUpdateHuman_PipedLines_All(t *testing.T) {
	var buf bytes.Buffer
	opts := &UpdateOptions{
		Packages:       []string{}, // no args: update all
		OutputConsumer: "human",
	}
	err := runUpdateForHumansWithSeams(nil, opts, &buf, &stubUpdater{})
	require.NoError(t, err)
	out := buf.String()

	assert.Contains(t, out, "✓ Updated all installed plugins")
	assert.Contains(t, out, "!")
	assert.Contains(t, out, "restart")
}

func TestUpdateHuman_ErrorFlipsToFail(t *testing.T) {
	var buf bytes.Buffer
	opts := &UpdateOptions{
		Packages:       []string{"aws"},
		OutputConsumer: "human",
	}
	boom := errors.New("update failed")
	err := runUpdateForHumansWithSeams(nil, opts, &buf, &stubUpdater{err: boom})
	require.ErrorIs(t, err, boom)
	out := buf.String()
	assert.Contains(t, out, "✗")
	assert.NotContains(t, out, "✓ Updated")
}

// ── machine output pin tests (R21) ───────────────────────────────────────────
// These golden tests pin the machine JSON output. They must remain byte-identical
// to the pre-change implementation — the machine paths are structurally unchanged.

func TestInstallMachine_Golden(t *testing.T) {
	var buf bytes.Buffer
	opts := &InstallOptions{
		Packages:       []string{"cloudflare"},
		OutputConsumer: "machine",
		OutputSchema:   "json",
	}
	err := runInstallForMachinesWithSeams(nil, opts, &buf, &stubInstaller{})
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

func TestUninstallMachine_Golden(t *testing.T) {
	var buf bytes.Buffer
	opts := &UninstallOptions{
		Packages:       []string{"cloudflare"},
		OutputConsumer: "machine",
		OutputSchema:   "json",
	}
	err := runUninstallForMachinesWithSeams(nil, opts, &buf, &stubUninstaller{})
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

func TestUpdateMachine_Golden(t *testing.T) {
	var buf bytes.Buffer
	opts := &UpdateOptions{
		Packages:       []string{"cloudflare"},
		OutputConsumer: "machine",
		OutputSchema:   "json",
	}
	err := runUpdateForMachinesWithSeams(nil, opts, &buf, &stubUpdater{})
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}
