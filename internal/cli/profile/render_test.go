// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profile

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

func TestRenderProfileList_Golden(t *testing.T) {
	th := theme.New("formae")
	out := renderProfileList(th, []string{"prod", "staging", "local-dev"}, "prod")
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderProfileList_EmptyStateHint(t *testing.T) {
	th := theme.New("formae")
	out := renderProfileList(th, nil, "")
	assert.Contains(t, out, "No profiles yet")
	assert.Contains(t, out, "formae profile create <name>")
}

func TestRenderProfileList_ActiveMarker(t *testing.T) {
	th := theme.New("formae")
	out := renderProfileList(th, []string{"prod", "staging"}, "prod")
	assert.Contains(t, out, "●")
	assert.Contains(t, out, "(active)")
	// staging should not have the active marker
	assert.True(t, strings.Count(out, "●") == 1, "only one profile should be marked active")
}

// TestCurrentBare_PipedOutput verifies that when isTerminal is false the human path
// prints just the bare profile name (scripts depend on this). This test drives
// the actual current command so that the isTerminal seam is exercised, not dead.
func TestCurrentBare_PipedOutput(t *testing.T) {
	// Point the store at a temp dir that has an active pointer.
	cfgDir := t.TempDir()
	// Write an active pointer file so Active() returns "staging".
	if err := os.WriteFile(cfgDir+"/active", []byte("staging\n"), 0o600); err != nil {
		t.Fatalf("write active: %v", err)
	}
	t.Setenv("FORMAE_CONFIG_DIR", cfgDir)

	// Stub isTerminal to false — this is what current.go consults.
	orig := isTerminal
	isTerminal = func(_ io.Writer) bool { return false }
	t.Cleanup(func() { isTerminal = orig })

	var buf bytes.Buffer
	cmd := newCurrentCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("current cmd: %v", err)
	}
	// Must be exactly the bare name with a trailing newline (from Fprintln) — no ANSI.
	assert.Equal(t, "staging\n", buf.String())
}

// TestCurrentBare_UnsetPiped verifies that the unset+piped path returns plain
// text with no ANSI — exercises Finding 1 fix via the isTerminal seam.
func TestCurrentBare_UnsetPiped(t *testing.T) {
	// Empty temp dir → Active() returns ErrNotInitialized → active == "".
	cfgDir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", cfgDir)

	// Stub isTerminal to false (piped).
	orig := isTerminal
	isTerminal = func(_ io.Writer) bool { return false }
	t.Cleanup(func() { isTerminal = orig })

	var buf bytes.Buffer
	cmd := newCurrentCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("current cmd: %v", err)
	}
	out := buf.String()
	// Must contain hint text.
	assert.Contains(t, out, "no active profile yet")
	assert.Contains(t, out, "formae profile use <name>")
	// Must NOT contain any ANSI escape sequences.
	assert.NotContains(t, out, "\x1b[", "output must have no ANSI escape sequences when piped")
}

// TestCurrentUnset verifies the hint text when no profile is active (render-level).
func TestCurrentUnset_HintText(t *testing.T) {
	th := theme.New("formae")
	out := renderCurrentHuman(th, "", false)
	assert.Contains(t, out, "no active profile yet")
	assert.Contains(t, out, "formae profile use <name>")
	// Unset + piped must have no ANSI.
	assert.NotContains(t, out, "\x1b[", "plain output must have no ANSI when tty=false")
}

// TestRenderAck verifies that renderAck produces a checkmark line.
func TestRenderAck_ContainsCheckmark(t *testing.T) {
	th := theme.New("formae")
	out := renderAck(th, "switched to staging")
	assert.Contains(t, out, "✓")
	assert.Contains(t, out, "switched to staging")
}
