// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package update

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/orbital/opm/records"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// renderUpdateList renders `formae update list`. On a TTY it produces the themed
// list (renderVersionList); when piped it produces the plain, ANSI-free listing
// (formatAvailableVersions) so scripts get stable output. Both read the FULL
// candidate list (never orbital's AvailableForSimple, which drops the index-0
// candidate — see formatAvailableVersions), so a cold index still lists the
// newest version.
func renderUpdateList(th *theme.Theme, tty bool, available []*records.Package) string {
	if !tty {
		return formatAvailableVersions(available)
	}

	var installedShort string
	var installedDate time.Time
	seen := make(map[string]bool, len(available))
	versions := make([]string, 0, len(available))
	for _, pkg := range available {
		if pkg == nil || pkg.Version == nil {
			continue
		}
		short := pkg.Version.Short()
		if pkg.Installed && installedShort == "" {
			installedShort = short
			installedDate = pkg.Version.Timestamp
		}
		if seen[short] {
			continue
		}
		seen[short] = true
		versions = append(versions, short)
	}
	return renderVersionList(th, installedShort, installedDate, versions)
}

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

	// Header line: "Installed: <version>  (<yyyy-mm-dd>)" — omitted on a cold
	// index where nothing is installed yet.
	if installed != "" {
		dateStr := installedDate.Format("2006-01-02")
		sb.WriteString(
			labelStyle.Render("Installed:") + " " +
				valueStyle.Render(installed) + "  " +
				labelStyle.Render("("+dateStr+")"),
		)
		sb.WriteString("\n\n")
	}

	// Section header at column 0 (CLI convention); versions indented tight below.
	sb.WriteString(components.SectionHeader(th, "Available versions"))
	sb.WriteString("\n")

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
			// "● <version>   installed" — marker, version, and "installed" all in Done role
			marker := doneStyle.Render("●")
			ver := doneStyle.Render(v)
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
