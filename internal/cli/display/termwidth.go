// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package display

import (
	"os"

	"golang.org/x/term"
)

const defaultTerminalWidth = 100

// TerminalWidth returns the current terminal width, falling back to
// defaultTerminalWidth when the width cannot be determined (e.g., piped output).
func TerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return defaultTerminalWidth
	}
	return width
}
