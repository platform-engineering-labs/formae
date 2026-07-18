// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// isTerminal is a package-level var so tests can stub it.
var isTerminal = tui.IsTerminal

// renderProfileList renders the profile listing with the active profile
// marked ● (mockup profile.txt View 1). Empty input renders the hint state.
func renderProfileList(th *theme.Theme, names []string, active string) string {
	if len(names) == 0 {
		hint := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
		return hint.Render("No profiles yet. Create one: formae profile create <name>")
	}
	activeStyle := lipgloss.NewStyle().Foreground(th.Palette.Done)
	subtle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
	body := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary)
	lines := make([]string, 0, len(names))
	for _, n := range names {
		if n == active {
			lines = append(lines, activeStyle.Render("● "+n)+" "+subtle.Render("(active)"))
			continue
		}
		lines = append(lines, "  "+body.Render(n))
	}
	return strings.Join(lines, "\n")
}

// renderCurrentHuman renders the current-profile output for a human reader.
// When tty is false (piped) it returns just the bare name — scripts depend on
// this. When the profile is unset and piped it returns a plain two-line hint;
// when unset and on a TTY it returns the styled hint.
func renderCurrentHuman(th *theme.Theme, active string, tty bool) string {
	if active == "" {
		if !tty {
			return "no active profile yet\nactivate one: formae profile use <name>"
		}
		secondary := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
		subtle := lipgloss.NewStyle().Foreground(th.Palette.TextSubtle)
		return secondary.Render("no active profile yet") + "\n" + subtle.Render("activate one: formae profile use <name>")
	}
	if !tty {
		return active
	}
	return lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(active)
}

// renderAck renders a checkmark acknowledgment line using the shared
// components.AckLine helper.
func renderAck(th *theme.Theme, text string) string {
	return components.AckLine(th, components.AckDone, text)
}
