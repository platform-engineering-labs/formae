// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// stepIsTerminal and stepTermWidth are package seams so tests can force TTY
// behavior without a pty.
var (
	stepIsTerminal = tui.IsTerminal
	stepTermWidth  = defaultTermWidth
)

func defaultTermWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 80
}

var stepFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Step is a single spinner→result line. Strictly sequential: at most one
// live Step at a time per writer, and callers must not write to w while a
// Step is live (D9). Not a bubbletea app.
type Step struct {
	w        io.Writer
	th       *theme.Theme
	tty      bool
	finished bool
	mu       sync.Mutex
	stop     chan struct{}
	stopped  chan struct{}
}

// StartStep begins a step. On a TTY it renders an animated "⠋ text" line;
// when piped it renders nothing until the result (result lines only, R10).
func StartStep(w io.Writer, th *theme.Theme, text string) *Step {
	s := &Step{w: w, th: th, tty: stepIsTerminal(w)}
	if !s.tty {
		return s
	}
	s.stop = make(chan struct{})
	s.stopped = make(chan struct{})
	width := stepTermWidth(w)
	// Write the first frame synchronously before spawning the goroutine to
	// keep output deterministic in tests (avoids ticker-first-frame race).
	_, _ = fmt.Fprint(s.w, "\r\x1b[K"+stepFrameLine(s.th, stepFrames[0], text, width))
	go func() {
		defer close(s.stopped)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				i = (i + 1) % len(stepFrames)
				_, _ = fmt.Fprint(s.w, "\r\x1b[K"+stepFrameLine(s.th, stepFrames[i], text, width))
			}
		}
	}()
	return s
}

// stepFrameLine renders one animated line truncated to the terminal width.
// Truncate plain text first, style after (never slice styled strings).
func stepFrameLine(th *theme.Theme, frame, text string, width int) string {
	avail := width - 2 // frame + space
	if avail < 1 {
		avail = 1
	}
	plain := Truncate(text, avail)
	spin := lipgloss.NewStyle().Foreground(th.Palette.SecondaryAccent).Render(frame)
	body := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary).Render(plain)
	return spin + " " + body
}

func (s *Step) Done(text string) { s.finish(AckDone, text) }
func (s *Step) Fail(text string) { s.finish(AckFail, text) }
func (s *Step) Skip(text string) { s.finish(AckSkip, text) }
func (s *Step) Warn(text string) { s.finish(AckWarn, text) }

func (s *Step) finish(m AckMarker, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		panic("components.Step: finished twice — steps are strictly sequential")
	}
	s.finished = true
	if s.tty {
		close(s.stop)
		<-s.stopped
		_, _ = fmt.Fprint(s.w, "\r\x1b[K"+AckLine(s.th, m, text)+"\n")
		return
	}
	// Piped: plain result line, no ANSI.
	glyph := map[AckMarker]string{AckDone: "✓", AckSkip: "·", AckWarn: "!", AckFail: "✗"}[m]
	_, _ = fmt.Fprintf(s.w, "%s %s\n", glyph, text)
}
