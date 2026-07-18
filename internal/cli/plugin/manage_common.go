// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"io"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// pluginIsTerminal is a package seam so tests can force piped (non-TTY) behavior.
var pluginIsTerminal = tui.IsTerminal

// ackLine emits a single acknowledgment line to w. On a TTY it renders with
// lipgloss styling; when piped it writes plain text so output stays ANSI-free.
func ackLine(w io.Writer, tty bool, th *theme.Theme, m components.AckMarker, text string) {
	if tty {
		_, _ = fmt.Fprintln(w, components.AckLine(th, m, text))
		return
	}
	glyph := map[components.AckMarker]string{
		components.AckDone: "✓",
		components.AckSkip: "·",
		components.AckWarn: "!",
		components.AckFail: "✗",
	}[m]
	_, _ = fmt.Fprintf(w, "%s %s\n", glyph, text)
}
