// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profile

import (
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

// TestCurrentBare verifies that when isTerminal is false the human path
// prints just the bare profile name (scripts depend on this).
func TestCurrentBare_PipedOutput(t *testing.T) {
	// stub isTerminal to false (piped)
	orig := isTerminal
	isTerminal = func(_ io.Writer) bool { return false }
	t.Cleanup(func() { isTerminal = orig })

	th := theme.New("formae")
	out := renderCurrentHuman(th, "staging", false)
	// must be exactly the name — no ANSI, no newline in the returned string
	assert.Equal(t, "staging", out)
}

// TestCurrentUnset verifies the hint text when no profile is active.
func TestCurrentUnset_HintText(t *testing.T) {
	th := theme.New("formae")
	out := renderCurrentHuman(th, "", false)
	assert.Contains(t, out, "no active profile yet")
	assert.Contains(t, out, "formae profile use <name>")
}

// TestRenderAck verifies that renderAck produces a checkmark line.
func TestRenderAck_ContainsCheckmark(t *testing.T) {
	th := theme.New("formae")
	out := renderAck(th, "switched to staging")
	assert.Contains(t, out, "✓")
	assert.Contains(t, out, "switched to staging")
}
