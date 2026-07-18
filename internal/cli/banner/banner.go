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
// For braille/graphics, rows is exact (braille) or an approximation (graphics).
// Graphics vertical spacing is a live-tuning item (D2): the 8 px/cell
// approximation in graphicsRowCount likely undercounts real terminal rows
// (cells are ~16–24 px). We add a conservative newline gap after graphics art
// so subsequent output does not overlap the image.
func PrintBanner() {
	// Suppression gate: never print a banner when stdout is not a TTY.
	if !isTerminal(os.Stdout) {
		return
	}

	cap := detect()
	art, rows := logo.Render(cap, logo.SizeFull, formae.Version)

	switch cap {
	case logo.CapKitty, logo.CapITerm2:
		// Graphics art: rows is an approximation (8 px/cell underestimates real
		// cell height of ~16–24 px). Print the art then add a conservative
		// newline gap so subsequent output does not overlap the image.
		// TODO(D2): tune the exact vertical gap once live-terminal verification
		// is run across Kitty/iTerm2/WezTerm/Ghostty — this is a required
		// user-run follow-up step.
		_, _ = fmt.Print(art)
		for range max(1, rows/2) {
			_, _ = fmt.Println()
		}
	default:
		// CapText and CapBraille: rows is exact; just print the art.
		_, _ = fmt.Println(art)
	}
	_ = rows
}

// DefaultLinks returns the formatted links block used in help templates.
func DefaultLinks() string {
	return "\n" + goldStyle.Render("Code: ") + "https://github.com/platform-engineering-labs/formae" +
		"\n" + goldStyle.Render("Docs: ") + DocRoot +
		"\n" + goldStyle.Render("Bugs: ") + "https://github.com/platform-engineering-labs/formae/issues"
}
