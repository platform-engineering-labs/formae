// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tui

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

// RunOptions configures how the bubbletea program is launched.
type RunOptions struct {
	// AltScreen uses the terminal alternate screen buffer.
	// Set to false for print-and-exit commands.
	AltScreen bool

	// Output overrides the output writer (default: os.Stdout).
	// Useful for testing.
	Output io.Writer
}

// DefaultRunOptions returns RunOptions suitable for interactive TUI commands.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		AltScreen: true,
		Output:    os.Stdout,
	}
}

// IsTerminal returns true if the given writer is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// buildProgramOptions converts RunOptions into tea.ProgramOption slice.
func buildProgramOptions(opts RunOptions) []tea.ProgramOption {
	var progOpts []tea.ProgramOption

	if opts.AltScreen {
		progOpts = append(progOpts, tea.WithAltScreen())
	}

	if opts.Output != nil {
		progOpts = append(progOpts, tea.WithOutput(opts.Output))
	}

	return progOpts
}

// Run starts a bubbletea program with the given model and options.
// It blocks until the program exits. Returns the final model and
// any error from the program.
func Run(model tea.Model, opts RunOptions) (tea.Model, error) {
	progOpts := buildProgramOptions(opts)
	p := tea.NewProgram(model, progOpts...)

	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("TUI error: %w", err)
	}

	return finalModel, nil
}
