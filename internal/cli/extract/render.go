// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package extract

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// extractedResource holds the display-relevant fields for one extracted resource.
type extractedResource struct {
	Label string
	Type  string
	Stack string
}

// renderExtractSummary renders the themed post-extraction summary.
//
// Format:
//
//	Extracted N resources to <path>
//
//	  <label> (<type>) on stack <stack>
//	  ...
func renderExtractSummary(th *theme.Theme, path string, resources []extractedResource) string {
	p := th.Palette

	headerStyle := lipgloss.NewStyle().Foreground(p.TextPrimary)
	labelStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	typeStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	stackStyle := lipgloss.NewStyle().Foreground(p.TextPrimary)

	var sb strings.Builder

	// Header line: "Extracted N resources to <path>"
	n := len(resources)
	noun := "resources"
	if n == 1 {
		noun = "resource"
	}
	sb.WriteString(headerStyle.Render(fmt.Sprintf("Extracted %d %s to %s", n, noun, path)))
	sb.WriteString("\n")

	// Per-resource lines (indented 2 spaces).
	for _, r := range resources {
		line := fmt.Sprintf("  %s (%s) on stack %s",
			labelStyle.Render(r.Label),
			typeStyle.Render(r.Type),
			stackStyle.Render(r.Stack),
		)
		sb.WriteString("\n")
		sb.WriteString(line)
	}

	return sb.String()
}
