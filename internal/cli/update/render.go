// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package update

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// renderVersionList renders the themed output for `formae update list`.
//
// Parameters:
//   - th: the active theme
//   - installed: the installed version string (e.g. "0.82.2")
//   - installedDate: the install timestamp — only the date portion is rendered
//   - available: version strings in newest-first order as returned by the backend
//
// Versions appearing before the installed entry in the list are considered
// newer; those after are older. The installed entry gets a ● marker (Done role)
// and versions newer than it are annotated "newer" in accent (SecondaryAccent).
func renderVersionList(th *theme.Theme, installed string, installedDate time.Time, available []string) string {
	p := th.Palette

	labelStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	valueStyle := lipgloss.NewStyle().Foreground(p.TextPrimary)
	accentStyle := lipgloss.NewStyle().Foreground(p.SecondaryAccent)
	doneStyle := lipgloss.NewStyle().Foreground(p.Done)

	var sb strings.Builder

	// Header line: "Installed: <version>  (<yyyy-mm-dd>)"
	dateStr := installedDate.Format("2006-01-02")
	sb.WriteString(
		labelStyle.Render("Installed:") + " " +
			valueStyle.Render(installed) + "  " +
			labelStyle.Render("("+dateStr+")"),
	)
	sb.WriteString("\n\n")

	// Section label
	sb.WriteString(labelStyle.Render("Available versions:"))
	sb.WriteString("\n\n")

	// Find the position of the installed version in the list (newest-first).
	// Entries before it are newer; entries after are older.
	installedIdx := -1
	for i, v := range available {
		if v == installed {
			installedIdx = i
			break
		}
	}

	for i, v := range available {
		isInstalled := v == installed
		isNewer := installedIdx >= 0 && i < installedIdx

		if isInstalled {
			// "● <version>   installed" — ● and "installed" in Done role
			marker := doneStyle.Render("●")
			ver := valueStyle.Render(v)
			annotation := doneStyle.Render("installed")
			sb.WriteString("  " + marker + " " + ver + "   " + annotation)
		} else if isNewer {
			// "  <version>   newer" — "newer" in accent
			ver := valueStyle.Render(v)
			annotation := accentStyle.Render("newer")
			sb.WriteString("    " + ver + "   " + annotation)
		} else {
			// older or installed-not-found: unannotated
			ver := valueStyle.Render(v)
			sb.WriteString("    " + ver)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
