// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package banner provides the formae banner, constants, and related
// terminal helpers. It is a leaf package with no import cycles.
package banner

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/logo"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

const (
	// Tool is the canonical CLI tool name.
	Tool = "formae"

	// DocRoot is the root URL for online documentation.
	DocRoot = "https://docs.formae.io/en/latest"
)

var (
	th        = theme.New("formae")
	goldStyle = lipgloss.NewStyle().Foreground(th.Palette.Warning)
)

// detect is a seam for tests: it wraps logo.Detect so tests can stub capability
// detection without performing real terminal I/O.
//
//nolint:gochecknoglobals
var detect = logo.Detect

// isTerminal is a seam for tests: it wraps tui.IsTerminal so tests can simulate
// a non-TTY environment.
//
//nolint:gochecknoglobals
var isTerminal = func(w io.Writer) bool {
	return tui.IsTerminal(w)
}

// ClearScreen clears the terminal screen.
func ClearScreen() {
	_, _ = fmt.Print("\033[2J")   // Clear screen
	_, _ = fmt.Print("\033[2;1H") // Move cursor to top left
}

// PrintBanner prints the formae logo banner.
//
// Suppression: when stdout is not a TTY (piped output, CI, machine consumers)
// the banner is silently suppressed — scripts and machine-readable output stay
// clean. This uses IsTerminal(os.Stdout), NOT IsInteractive, because the banner
// never reads stdin.
//
// For CapText the art is already "formae v{version}" — just print it.
// For braille/graphics, rows is exact (countRows for braille; graphicsRows for
// graphics). The composeGraphicsWordmark helper uses DEC cursor positioning so
// the cursor is left below the image — one blank line after is sufficient.
func PrintBanner() {
	// Suppression gate: never print a banner when stdout is not a TTY.
	if !isTerminal(os.Stdout) {
		return
	}

	cap := detect()
	art, rows := logo.Render(cap, logo.SizeFull, formae.Version)

	switch cap {
	case logo.CapKitty, logo.CapITerm2:
		// Graphics art: rows is now the exact graphicsRows cell count.
		// composeGraphicsWordmark leaves the cursor below the image via a final
		// cursor-down, so one blank line separates the logo from subsequent output.
		_, _ = fmt.Print(art)
		_, _ = fmt.Println()
	default:
		// CapText and CapBraille: rows is exact; print the art then one blank line.
		_, _ = fmt.Println(art)
		_, _ = fmt.Println()
	}
	_ = rows
}

// DefaultLinks returns the formatted links block used in help templates.
func DefaultLinks() string {
	return "\n" + goldStyle.Render("Code: ") + "https://github.com/platform-engineering-labs/formae" +
		"\n" + goldStyle.Render("Docs: ") + DocRoot +
		"\n" + goldStyle.Render("Bugs: ") + "https://github.com/platform-engineering-labs/formae/issues"
}
