package renderer

import (
	"os"

	"golang.org/x/term"
)

const defaultTerminalWidth = 100

// terminalWidth returns the current terminal width, falling back to
// defaultTerminalWidth when the width cannot be determined (e.g., piped output).
func terminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return defaultTerminalWidth
	}
	return width
}
