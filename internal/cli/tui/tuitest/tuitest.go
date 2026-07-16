// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package tuitest provides shared test infrastructure for TUI components:
// deterministic lipgloss rendering, teatest program helpers and golden-file
// assertions. Golden files live in each package's testdata/ directory and
// are regenerated with `go test -update`.
package tuitest

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
)

// PinRendering forces a fixed color profile and background so rendered
// output (and therefore golden files) does not depend on the terminal the
// tests run in. Call it from TestMain before m.Run().
func PinRendering() {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
}

// Run starts a teatest program for the model at a fixed terminal size.
func Run(t *testing.T, m tea.Model, width, height int) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(width, height))
}

// WaitForContains polls the program output until it contains want, failing
// the test after a short timeout.
func WaitForContains(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte(want))
	}, teatest.WithDuration(3*time.Second))
}

// RequireGolden compares out against testdata/<TestName>.golden.
func RequireGolden(t *testing.T, out []byte) {
	t.Helper()
	teatest.RequireEqualOutput(t, out)
}
