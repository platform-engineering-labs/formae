// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tuitest

import (
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestMain(m *testing.M) {
	PinRendering()
	os.Exit(m.Run())
}

type staticModel struct{ text string }

func (s staticModel) Init() tea.Cmd                       { return nil }
func (s staticModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }
func (s staticModel) View() string                        { return s.text }

func TestRequireGolden_StyledOutputIsDeterministic(t *testing.T) {
	styled := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"}).
		Bold(true).
		Render("formae")
	RequireGolden(t, []byte(styled))
}

func TestRunAndWaitForContains(t *testing.T) {
	tm := Run(t, staticModel{text: "hello tui"}, 80, 24)
	WaitForContains(t, tm, "hello tui")
	if err := tm.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	tm.WaitFinished(t)
}
