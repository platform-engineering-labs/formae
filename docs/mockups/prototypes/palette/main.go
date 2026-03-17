// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Prototype: Color palette demo
// Run: go run docs/mockups/prototypes/palette/main.go
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func main() {
	for _, name := range []string{"formae", "classic"} {
		th := theme.New(name)
		renderTheme(th)
		fmt.Println()
	}
}

func renderTheme(th *theme.Theme) {
	s := th.Styles
	p := th.Palette
	w := 78

	title := s.Title.Render(fmt.Sprintf(" Theme: %s ", th.Name))
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		Width(w).
		Padding(1, 2)

	var sections []string

	// Base colors
	sections = append(sections, s.Title.Render("Base Colors"))
	sections = append(sections, colorSwatch(p.Base, "Base")+
		"  "+colorSwatch(p.Surface, "Surface")+
		"  "+colorSwatch(p.Border, "Border"))
	sections = append(sections, "")

	// Text hierarchy
	sections = append(sections, s.Title.Render("Text Hierarchy"))
	sections = append(sections,
		lipgloss.NewStyle().Foreground(p.TextPrimary).Render("Primary text — active, important content"))
	sections = append(sections,
		lipgloss.NewStyle().Foreground(p.TextSecondary).Render("Secondary text — labels, metadata"))
	sections = append(sections,
		lipgloss.NewStyle().Foreground(p.TextSubtle).Render("Subtle text — hints, disabled items"))
	sections = append(sections, "")

	// Accent colors
	sections = append(sections, s.Title.Render("Accents"))
	sections = append(sections,
		s.Accent.Render("Blue accent — IDs, links, interactive")+
			"  "+s.SecondaryAccent.Render("Orange accent — brand, progress"))
	sections = append(sections, "")

	// Semantic colors
	sections = append(sections, s.Title.Render("Semantic"))
	sections = append(sections,
		s.StatusFailed.Render("Error/Failed")+
			"  "+s.StatusWarning.Render("Warning")+
			"  "+s.StatusDone.Render("Done")+
			"  "+s.StatusInProgress.Render("In Progress")+
			"  "+s.StatusPending.Render("Pending"))
	sections = append(sections, "")

	// Status indicators
	sections = append(sections, s.Title.Render("Status Indicators"))
	sections = append(sections,
		s.StatusDone.Render("  my-bucket (AWS::S3::Bucket)                         Done"))
	sections = append(sections,
		s.StatusInProgress.Render("  primary (AWS::RDS::DBInstance)                  ◐ 00:12"))
	sections = append(sections,
		s.StatusPending.Render("  cdn (AWS::CloudFront::Distribution)                   · "))
	sections = append(sections,
		s.StatusFailed.Render("  old-data (AWS::S3::Bucket)                       FAILED"))
	sections = append(sections, "")

	// Progress bar
	sections = append(sections, s.Title.Render("Progress Bar"))
	filled := 24
	empty := 16
	bar := s.ProgressBarFill.Render(strings.Repeat("█", filled)) +
		s.ProgressBar.Render(strings.Repeat("░", empty))
	sections = append(sections,
		fmt.Sprintf("  %s  %s",
			bar,
			lipgloss.NewStyle().Foreground(p.TextSecondary).Render("12/15 updates")))
	sections = append(sections, "")

	// Panels
	sections = append(sections, s.Title.Render("Panels"))
	infoPanel := s.Panel.Width(w - 6).Render(
		lipgloss.NewStyle().Foreground(p.TextPrimary).Render("Info panel with rounded border"))
	errorPanel := s.ErrorPanel.Width(w - 6).Render(
		s.StatusFailed.Render("Error panel with red border"))
	warnPanel := s.WarnPanel.Width(w - 6).Render(
		s.StatusWarning.Render("Warning panel with warning border"))
	sections = append(sections, infoPanel)
	sections = append(sections, errorPanel)
	sections = append(sections, warnPanel)
	sections = append(sections, "")

	// Keybindings
	sections = append(sections, s.Title.Render("Keybindings"))
	keys := []struct{ key, desc string }{
		{"d", "detail/summary"},
		{"f", "filter"},
		{"/", "search"},
		{"q", "quit"},
		{"?", "help"},
	}
	var keyParts []string
	for _, k := range keys {
		keyParts = append(keyParts,
			s.KeybindingKey.Render(k.key)+
				s.KeybindingDesc.Render(": "+k.desc))
	}
	sections = append(sections, "  "+strings.Join(keyParts, "  "))

	content := strings.Join(sections, "\n")
	fmt.Println(title)
	fmt.Println(border.Render(content))
}

func colorSwatch(c lipgloss.AdaptiveColor, label string) string {
	return lipgloss.NewStyle().
		Background(c).
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#000000"}).
		Padding(0, 1).
		Render(label)
}
