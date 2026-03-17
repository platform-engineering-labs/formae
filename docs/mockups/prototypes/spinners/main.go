// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Prototype: Spinner/busy indicator comparison
// Run: go run docs/mockups/prototypes/spinners/main.go
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

type model struct {
	theme    *theme.Theme
	spinners []namedSpinner
	wave     int
	quitting bool
}

type namedSpinner struct {
	name    string
	spinner spinner.Model
}

type waveTickMsg time.Time

func waveTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return waveTickMsg(t)
	})
}

func newModel() model {
	th := theme.New("formae")
	p := th.Palette

	blue := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	orange := lipgloss.NewStyle().Foreground(p.SecondaryAccent)

	spinners := []namedSpinner{
		{"MiniDot (blue)", newSpinner(spinner.MiniDot, blue)},
		{"MiniDot (orange)", newSpinner(spinner.MiniDot, orange)},
		{"Dot (blue)", newSpinner(spinner.Dot, blue)},
		{"Pulse (orange)", newSpinner(spinner.Pulse, orange)},
		{"Points (blue)", newSpinner(spinner.Points, blue)},
		{"Meter (orange)", newSpinner(spinner.Meter, orange)},
		{"Ellipsis (blue)", newSpinner(spinner.Ellipsis, blue)},
	}

	return model{
		theme:    th,
		spinners: spinners,
	}
}

func newSpinner(s spinner.Spinner, style lipgloss.Style) spinner.Model {
	sp := spinner.New()
	sp.Spinner = s
	sp.Style = style
	return sp
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.spinners {
		cmds = append(cmds, m.spinners[i].spinner.Tick)
	}
	cmds = append(cmds, waveTick())
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	case waveTickMsg:
		m.wave++
		return m, waveTick()
	}

	var cmds []tea.Cmd
	for i := range m.spinners {
		var cmd tea.Cmd
		m.spinners[i].spinner, cmd = m.spinners[i].spinner.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	s := m.theme.Styles
	p := m.theme.Palette

	var b strings.Builder

	b.WriteString(s.Title.Render("Spinner / Busy Indicator Comparison"))
	b.WriteString("\n\n")

	// Built-in spinners
	b.WriteString(s.Subtitle.Render("Built-in bubbles spinners:"))
	b.WriteString("\n\n")

	label := lipgloss.NewStyle().Foreground(p.TextSecondary).Width(22)
	status := lipgloss.NewStyle().Foreground(p.TextPrimary)

	for _, ns := range m.spinners {
		b.WriteString(fmt.Sprintf("  %s %s %s\n",
			label.Render(ns.name),
			ns.spinner.View(),
			status.Render("creating resource...")))
	}

	b.WriteString("\n")
	b.WriteString(s.Subtitle.Render("Custom block wave (blue shades, 5 chars):"))
	b.WriteString("\n\n")
	b.WriteString("  " + label.Render("Block wave") + " " + m.renderWave(5) + " " + status.Render("creating resource..."))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(s.Subtitle.Render("In context — resource update line:"))
	b.WriteString("\n\n")

	done := lipgloss.NewStyle().Foreground(p.Done)
	inProgress := lipgloss.NewStyle().Foreground(p.InProgress)
	pending := lipgloss.NewStyle().Foreground(p.Pending)
	failed := lipgloss.NewStyle().Foreground(p.Error).Bold(true)
	accent := lipgloss.NewStyle().Foreground(p.PrimaryAccent)

	miniDot := m.spinners[0].spinner.View() // MiniDot blue

	b.WriteString(done.Render("    create  my-bucket (AWS::S3::Bucket)                          Done"))
	b.WriteString("\n")
	b.WriteString(inProgress.Render(fmt.Sprintf("    update  primary (AWS::RDS::DBInstance)                   %s 00:12", miniDot)))
	b.WriteString("\n")
	b.WriteString(pending.Render("    create  cdn (AWS::CloudFront::Distribution)                    · "))
	b.WriteString("\n")
	b.WriteString(failed.Render("    delete  old-data (AWS::S3::Bucket)                        FAILED"))
	b.WriteString("\n")
	b.WriteString(inProgress.Render(fmt.Sprintf("    create  web-server (AWS::EC2::Instance)                 %s 00:05", miniDot)))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(s.Subtitle.Render("Alternative — pulse instead of MiniDot:"))
	b.WriteString("\n\n")

	pulse := m.spinners[3].spinner.View() // Pulse orange

	b.WriteString(done.Render("    create  my-bucket (AWS::S3::Bucket)                          Done"))
	b.WriteString("\n")
	b.WriteString(inProgress.Render(fmt.Sprintf("    update  primary (AWS::RDS::DBInstance)                   %s 00:12", pulse)))
	b.WriteString("\n")
	b.WriteString(pending.Render("    create  cdn (AWS::CloudFront::Distribution)                    · "))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(s.Subtitle.Render("Alternative — block wave:"))
	b.WriteString("\n\n")

	wave := m.renderWave(3)
	b.WriteString(done.Render("    create  my-bucket (AWS::S3::Bucket)                        Done"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("%s  %s %s",
		inProgress.Render("    update  primary (AWS::RDS::DBInstance)"),
		wave,
		accent.Render("00:12")))
	b.WriteString("\n")
	b.WriteString(pending.Render("    create  cdn (AWS::CloudFront::Distribution)                  · "))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(p.TextSubtle).Render("  Press q to quit"))
	b.WriteString("\n")

	return b.String()
}

func (m model) renderWave(width int) string {
	blues := []lipgloss.Color{
		"#1a1a2e",
		"#16213e",
		"#0f3460",
		"#2563eb",
		"#60a5fa",
	}

	var result string
	for i := 0; i < width; i++ {
		colorIdx := (i + m.wave) % len(blues)
		style := lipgloss.NewStyle().Foreground(blues[colorIdx])
		result += style.Render("█")
	}
	return result
}

func main() {
	p := tea.NewProgram(newModel())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
