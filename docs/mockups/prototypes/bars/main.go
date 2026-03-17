// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Prototype: Progress bar character comparison
// Run: go run ./docs/mockups/prototypes/bars/
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

type barStyle struct {
	name      string
	filled    string
	empty     string
}

func main() {
	th := theme.New("formae")
	p := th.Palette

	fill := lipgloss.NewStyle().Foreground(p.SecondaryAccent)
	empty := lipgloss.NewStyle().Foreground(p.Pending)
	label := lipgloss.NewStyle().Foreground(p.TextSecondary)
	title := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)

	bars := []barStyle{
		{"Full block  █░", "█", "░"},
		{"Meter       ▰▱", "▰", "▱"},
		{"Half block  ▄▁", "▄", "▁"},
		{"Dash        ━╌", "━", "╌"},
		{"Thick dash  ━─", "━", "─"},
		{"Bullet      ●○", "●", "○"},
		{"Diamond     ◆◇", "◆", "◇"},
		{"Square      ■□", "■", "□"},
		{"Circle      ⬤○", "⬤", "○"},
		{"Pipe        ┃╎", "┃", "╎"},
		{"Braille     ⣿⣀", "⣿", "⣀"},
		{"Lower 3/8   ▃▁", "▃", "▁"},
		{"Upper half  ▀▔", "▀", "▔"},
	}

	fmt.Println(title.Render("\n  Progress Bar Character Comparison"))
	fmt.Println(title.Render("  Each shown at 60% and 100% fill, two rows for blending test\n"))

	for _, b := range bars {
		w := 20
		filled := int(float64(w) * 0.6)
		emptyW := w - filled

		bar60 := fill.Render(strings.Repeat(b.filled, filled)) +
			empty.Render(strings.Repeat(b.empty, emptyW))
		bar100 := fill.Render(strings.Repeat(b.filled, w))

		fmt.Printf("  %s  %s %s  %s %s\n",
			label.Width(16).Render(b.name),
			bar60, label.Render("8/14"),
			bar100, label.Render("14/14"))

		// Second row to show vertical blending
		fmt.Printf("  %s  %s %s  %s %s\n",
			label.Width(16).Render(""),
			bar60, label.Render("5/10"),
			bar100, label.Render("10/10"))

		fmt.Println()
	}
}
