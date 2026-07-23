// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package components provides the shared bubbletea/lipgloss building blocks
// every formae command TUI composes from. All rendering derives from
// theme.Theme; the design principle is "brightness for done, color only for
// problems".
package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// State is the lifecycle state of an update as displayed in TUIs.
type State string

const (
	StateDone       State = "done"
	StateInProgress State = "in-progress"
	StatePending    State = "pending"
	StateFailed     State = "failed"
	StateSkipped    State = "skipped"
)

// Glyph returns the leading glyph for problem states in list views;
// non-problem states have none (their trailing indicator speaks).
func Glyph(s State) string {
	switch s {
	case StateFailed:
		return "✗"
	case StateSkipped:
		return "⊘"
	default:
		return ""
	}
}

// NewSpinner returns the shared in-progress spinner: dot style in the
// primary accent.
func NewSpinner(th *theme.Theme) spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(th.Palette.PrimaryAccent)
	return s
}

// Indicator renders the trailing state indicator for a summary line. For
// in-progress rows, pass the current spinner view and elapsed duration.
func Indicator(th *theme.Theme, s State, spinnerView, elapsed string) string {
	p := th.Palette
	switch s {
	case StateDone:
		return lipgloss.NewStyle().Foreground(p.Done).Render("Done")
	case StateInProgress:
		v := strings.TrimSpace(spinnerView + " " + elapsed)
		return lipgloss.NewStyle().Foreground(p.InProgress).Render(v)
	case StatePending:
		return lipgloss.NewStyle().Foreground(p.Pending).Render("·")
	case StateFailed:
		return lipgloss.NewStyle().Foreground(p.Error).Bold(true).Render("FAILED")
	case StateSkipped:
		return lipgloss.NewStyle().Foreground(p.TextSecondary).Render("Skipped")
	}
	return ""
}

// StateCounts renders the header count line, e.g.
// "12 done · 2 in-progress · 1 pending · 0 failed · 5 skipped".
// done, in-progress, pending and failed always appear; skipped only when
// non-zero.
func StateCounts(th *theme.Theme, counts map[State]int) string {
	p := th.Palette
	sep := lipgloss.NewStyle().Foreground(p.TextSubtle).Render(" · ")
	parts := []string{
		lipgloss.NewStyle().Foreground(p.Done).Render(fmt.Sprintf("%d done", counts[StateDone])),
		lipgloss.NewStyle().Foreground(p.InProgress).Render(fmt.Sprintf("%d in-progress", counts[StateInProgress])),
		lipgloss.NewStyle().Foreground(p.Pending).Render(fmt.Sprintf("%d pending", counts[StatePending])),
		lipgloss.NewStyle().Foreground(p.Error).Bold(true).Render(fmt.Sprintf("%d failed", counts[StateFailed])),
	}
	if n := counts[StateSkipped]; n > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(p.TextSecondary).Render(fmt.Sprintf("%d skipped", n)))
	}
	return strings.Join(parts, sep)
}
