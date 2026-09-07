// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package nag

import (
	"fmt"
	"math/rand"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// defaultShouldNag returns true ~50% of the time (coin-flip gate).
func defaultShouldNag() bool {
	return rand.Intn(101)%2 == 0
}

// shouldNag is a package-level seam so tests can force or suppress nag output.
var shouldNag = defaultShouldNag

// MaybePrintNags prints each nag as a themed "Tip:" line when the coin flip
// is true. The prefix is rendered in the Warning palette role and the body in
// TextPrimary.
func MaybePrintNags(th *theme.Theme, nags []string) {
	if !shouldNag() {
		return
	}
	if len(nags) > 0 {
		fmt.Printf("\n")
		for _, nag := range nags {
			prefix := lipgloss.NewStyle().Foreground(th.Palette.Warning).Render("Tip:")
			body := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(nag)
			fmt.Printf("%s %s\n", prefix, body)
		}
	}
}
