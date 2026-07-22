// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Styles contains all the lipgloss styles used by TUI components.
// Created from a Palette via NewStyles().
type Styles struct {
	// Typography
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Label    lipgloss.Style
	Body     lipgloss.Style

	// Panels
	Panel      lipgloss.Style
	ErrorPanel lipgloss.Style
	WarnPanel  lipgloss.Style
	Header     lipgloss.Style
	Footer     lipgloss.Style

	// Accents
	Accent          lipgloss.Style
	SecondaryAccent lipgloss.Style

	// Status indicators
	StatusDone       lipgloss.Style
	StatusInProgress lipgloss.Style
	StatusPending    lipgloss.Style
	StatusFailed     lipgloss.Style
	StatusWarning    lipgloss.Style

	// Interactive elements
	KeybindingKey  lipgloss.Style
	KeybindingDesc lipgloss.Style

	// Progress
	ProgressBar     lipgloss.Style
	ProgressBarFill lipgloss.Style

	// Table
	TableHeader lipgloss.Style
	TableRow    lipgloss.Style
	TableRowAlt lipgloss.Style

	// Search/Filter
	FilterPrompt lipgloss.Style
	FilterInput  lipgloss.Style
}

// NewStyles creates a Styles from the given palette.
func NewStyles(p Palette) Styles {
	return Styles{
		Title: lipgloss.NewStyle().
			Foreground(p.TextPrimary).
			Bold(true),

		Subtitle: lipgloss.NewStyle().
			Foreground(p.TextSecondary),

		Label: lipgloss.NewStyle().
			Foreground(p.TextSubtle).
			Transform(strings.ToUpper),

		Body: lipgloss.NewStyle().
			Foreground(p.TextPrimary),

		Panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border).
			Padding(0, 1),

		ErrorPanel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Error).
			Padding(0, 1),

		WarnPanel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Warning).
			Padding(0, 1),

		Header: lipgloss.NewStyle().
			Foreground(p.TextPrimary).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(p.Border),

		Footer: lipgloss.NewStyle().
			Foreground(p.TextSubtle).
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(p.Border),

		Accent: lipgloss.NewStyle().
			Foreground(p.PrimaryAccent),

		SecondaryAccent: lipgloss.NewStyle().
			Foreground(p.SecondaryAccent),

		StatusDone: lipgloss.NewStyle().
			Foreground(p.Done),

		StatusInProgress: lipgloss.NewStyle().
			Foreground(p.InProgress),

		StatusPending: lipgloss.NewStyle().
			Foreground(p.Pending),

		StatusFailed: lipgloss.NewStyle().
			Foreground(p.Error).
			Bold(true),

		StatusWarning: lipgloss.NewStyle().
			Foreground(p.Warning),

		KeybindingKey: lipgloss.NewStyle().
			Foreground(p.PrimaryAccent).
			Bold(true),

		KeybindingDesc: lipgloss.NewStyle().
			Foreground(p.TextSubtle),

		ProgressBar: lipgloss.NewStyle().
			Foreground(p.TextSubtle),

		ProgressBarFill: lipgloss.NewStyle().
			Foreground(p.SecondaryAccent),

		TableHeader: lipgloss.NewStyle().
			Foreground(p.TextSecondary).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(p.Border),

		TableRow: lipgloss.NewStyle().
			Foreground(p.TextPrimary),

		TableRowAlt: lipgloss.NewStyle().
			Foreground(p.TextPrimary),

		FilterPrompt: lipgloss.NewStyle().
			Foreground(p.PrimaryAccent),

		FilterInput: lipgloss.NewStyle().
			Foreground(p.TextPrimary),
	}
}
