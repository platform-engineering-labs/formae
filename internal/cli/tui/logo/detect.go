// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"bytes"
	"os"
	"strings"
	"sync"

	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// envInfo carries all environment signals needed by decide.
// All fields are gathered once in Detect() and passed to decide().
type envInfo struct {
	IsTTY   bool // stdout is a real TTY
	Dumb    bool // TERM=dumb
	CI      bool // any CI env var is set
	Tmux    bool // inside tmux or screen
	SSH     bool // inside an SSH session
	Unicode bool // terminal supports Unicode (braille)
	Herdr   bool // inside a herdr multiplexer pane

	// Env hints — insufficient alone to enable graphics
	KittyWindowID bool   // KITTY_WINDOW_ID is set
	WezTerm       bool   // WEZTERM_EXECUTABLE or similar is set
	TermProgram   string // value of TERM_PROGRAM
	Term          string // value of TERM
}

// probeResult holds the result of the raw-mode escape-sequence query.
// Kitty is false when the probe was skipped or timed out.
type probeResult struct {
	Kitty bool
}

// probeAllowed reports whether the escape-sequence probe may be sent and its
// answer believed.
//
// tmux and screen intercept the escape stream, and over a plain SSH link a
// reply that arrives after the timeout leaks its bytes into the shell, so both
// normally suppress the probe.
//
// A herdr pane is the exception to the SSH rule. herdr's server outlives the
// client that started it, so SSH_CLIENT/SSH_CONNECTION/SSH_TTY inside a pane
// describe how that server was launched rather than this pane's terminal: a
// session started once over SSH leaves them set in every pane it spawns
// afterwards, including panes attached locally. herdr also answers the graphics
// query itself on behalf of the attached client, so the reply is immediate and
// cannot arrive late enough to leak. Its answer is better evidence than the
// inherited variables.
//
// A tmux inside a herdr pane still suppresses the probe: the innermost
// multiplexer owns the escape stream.
func probeAllowed(env envInfo) bool {
	if env.Tmux {
		return false
	}
	return !env.SSH || env.Herdr
}

// decide is a pure function: given env signals and probe results it returns
// the appropriate Capability.  The layered, fail-safe order is:
//
//  1. Hard disqualifiers → CapText
//  2. Probe results (trusted only when probeAllowed says the probe is sound)
//     probe.Kitty → CapKitty
//  3. Env hints are NOT sufficient to enable graphics; they only confirm
//     braille is a safe choice
//  4. TTY + Unicode, no confirmed graphics → CapBraille
func decide(env envInfo, probe probeResult) Capability {
	// Layer 1: hard disqualifiers
	if !env.IsTTY || env.Dumb || env.CI || !env.Unicode {
		return CapText
	}

	// Layer 2: probe results — only trust them where the probe is meaningful
	if probeAllowed(env) {
		if probe.Kitty {
			return CapKitty
		}
	}

	// Layer 3 + 4: env hints and everything else → braille
	// (Env hints such as KITTY_WINDOW_ID, WEZTERM_*, TERM_PROGRAM are
	// considered, but they are NOT sufficient to enable graphics — only a
	// successful probe may do that.  So we always land on CapBraille here.)
	return CapBraille
}

// parseKittyProbe is a pure function that reports whether buf contains a
// Kitty APC graphics OK response.  A response is positive iff the buffer
// contains the APC introducer \x1b_G followed by ";OK" anywhere in the
// payload.  A DA1-only response, an error payload, or an empty buffer all
// return false.
//
// Fail-safe invariant: any ambiguity returns false — decide() then lands on
// braille/text.  The parser only ever *adds* graphics on a clear positive.
func parseKittyProbe(resp []byte) bool {
	// An APC Kitty graphics response starts with ESC _ G (0x1b 0x5f 0x47).
	// The payload contains key=value pairs separated by semicolons.
	// A successful response contains ";OK" (or starts with "OK" for i=1 queries).
	apcPrefix := []byte("\x1b_G")
	idx := bytes.Index(resp, apcPrefix)
	if idx < 0 {
		return false
	}
	// The APC payload runs from after the prefix until the String Terminator
	// \x1b\\ (or end of buffer if ST is missing / not yet received).
	payload := resp[idx+len(apcPrefix):]
	st := []byte("\x1b\\")
	if end := bytes.Index(payload, st); end >= 0 {
		payload = payload[:end]
	}
	// The response fields are semicolon-delimited key=value pairs.
	// The status field is the last segment after the final semicolon.
	// Accept ";OK" anywhere in the payload (covers both "i=31;OK" and plain
	// "OK" for minimal responses).
	return bytes.Contains(payload, []byte(";OK")) ||
		bytes.HasPrefix(payload, []byte("OK"))
}

// probe is the seam for raw-mode escape-sequence queries. It is a var so
// tests can replace it without performing real terminal I/O.
// Where probeAllowed is false the seam returns an empty probeResult immediately.
//
// Fail-safe invariant: any error, timeout, or uncertainty leaves all fields
// false — decide() then lands on braille/text.  The probe only ever *adds*
// graphics capability on a clear positive signal.
//
//nolint:gochecknoglobals
var probe = func(env envInfo) probeResult {
	// Skip the escape-sequence probe entirely where it cannot be trusted —
	// a garbled escape in the banner is worse than no logo graphics.
	if !probeAllowed(env) {
		return probeResult{}
	}

	if !graphicsCandidate(env) {
		return probeResult{}
	}

	return queryKittyGraphics()
}

// graphicsCandidate reports whether the terminal is one that may draw the logo
// as a Kitty graphics image, i.e. whether the probe is worth sending at all.
// The probe only confirms support; this allowlist decides who is asked, so a
// terminal that does not understand the query never sees it:
//
//   - herdr panes: herdr answers the query itself for the attached client, and
//     the pane's TERM_PROGRAM/KITTY_* may be inherited from wherever the herdr
//     server was started, so they are not evidence either way.
//   - iTerm2 (TERM_PROGRAM=iTerm.app): never. It answers the Kitty query OK, but
//     its placements are anchored to absolute line numbers with a pixel frame
//     computed only when placed (iTerm2 3.6.11 KittyImageController.draws), so
//     after a font zoom or a window-width reflow the logo covers the text and
//     prompts below it; and its own OSC 1337 inline images ask "Allow
//     Terminal-Initiated Display?" for every image unless silenced for good
//     (PTYSession screenConfirmDownloadAllowed). It gets the braille logo.
//   - macOS Terminal.app (TERM_PROGRAM=Apple_Terminal): never. It has no
//     graphics protocol and prints the query's payload
//     ("Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA") as text above the banner.
//   - Kitty, Ghostty and WezTerm, recognised by the variables they set
//     themselves: yes. TERM=xterm-kitty / xterm-ghostty is checked before
//     TERM_PROGRAM, which Kitty does not set and may inherit from the shell it
//     was started from. KITTY_WINDOW_ID / WEZTERM_* are not used: any terminal
//     started from a Kitty or WezTerm shell inherits them.
//   - anything else: no — the braille logo is the safe default, and an unknown
//     terminal may print the query as text the way Terminal.app does.
func graphicsCandidate(env envInfo) bool {
	if env.Herdr {
		return true
	}
	// TERM is always set by the terminal itself, while TERM_PROGRAM is inherited
	// when a terminal that does not set it (Kitty) is started from another
	// terminal's shell (iTerm2): TERM wins.
	if strings.HasPrefix(env.Term, "xterm-kitty") || env.Term == "xterm-ghostty" {
		return true
	}
	switch env.TermProgram {
	case "iTerm.app", "Apple_Terminal":
		return false
	case "ghostty", "WezTerm":
		return true
	}
	// KITTY_WINDOW_ID / WEZTERM_* alone are not evidence: they are inherited by
	// any terminal started from a Kitty or WezTerm shell (VS Code, Alacritty,
	// foot ...). A Kitty whose TERM was overridden gets the braille logo.
	return false
}

// queryKittyGraphics performs the raw-mode Kitty graphics query on the
// controlling terminal (queryKittyGraphicsTTY, per platform). It is a var so
// tests can assert when it is (not) used.
//
//nolint:gochecknoglobals
var queryKittyGraphics = queryKittyGraphicsTTY

// detectOnce and detectCached hold the memoized Detect() result.
//
//nolint:gochecknoglobals
var (
	detectOnce   sync.Once
	detectCached Capability
)

// isHardDisqualified reports whether env contains a hard disqualifier that
// makes CapText the only possible result.  This mirrors the first check in
// decide() and is used in Detect() to skip the probe entirely when the
// outcome is already determined.
func isHardDisqualified(env envInfo) bool {
	return !env.IsTTY || env.Dumb || env.CI || !env.Unicode
}

// Detect gathers environment signals, runs the probe seam, calls decide, and
// returns the result — computing it only once (cached via sync.Once).
//
// Hard disqualifiers (non-TTY, dumb, CI, no-Unicode) are checked BEFORE
// calling probe() so that no escape-sequence traffic is ever sent to
// disqualified terminals.
func Detect() Capability {
	detectOnce.Do(func() {
		env := terminalEnv()
		if isHardDisqualified(env) {
			detectCached = CapText
			return
		}
		pr := probe(env)
		detectCached = decide(env, pr)
	})
	return detectCached
}

// gatherEnv reads all environment signals needed by decide.
func gatherEnv() envInfo {
	term := os.Getenv("TERM")
	termProgram := os.Getenv("TERM_PROGRAM")

	isTTY := isTerminalFd(int(os.Stdout.Fd()))

	dumb := strings.EqualFold(term, "dumb")

	ci := os.Getenv("CI") != "" ||
		os.Getenv("GITHUB_ACTIONS") != "" ||
		os.Getenv("GITLAB_CI") != "" ||
		os.Getenv("CIRCLECI") != "" ||
		os.Getenv("BUILDKITE") != "" ||
		os.Getenv("DRONE") != ""

	tmux := os.Getenv("TMUX") != "" || strings.HasPrefix(term, "screen")

	ssh := os.Getenv("SSH_CLIENT") != "" ||
		os.Getenv("SSH_TTY") != "" ||
		os.Getenv("SSH_CONNECTION") != ""

	// termenv profile detects whether the terminal can handle Unicode/braille.
	// The Ascii profile means no Unicode support.
	profile := termenv.ColorProfile()
	unicode := profile != termenv.Ascii

	// herdr sets HERDR_ENV=1 in every pane it spawns; this is the marker herdr
	// documents for detecting that a process runs inside one of its panes.
	herdr := os.Getenv("HERDR_ENV") == "1"

	kittyWindowID := os.Getenv("KITTY_WINDOW_ID") != ""
	wezterm := os.Getenv("WEZTERM_EXECUTABLE") != "" ||
		os.Getenv("WEZTERM_PANE") != ""

	return envInfo{
		IsTTY:         isTTY,
		Dumb:          dumb,
		CI:            ci,
		Tmux:          tmux,
		SSH:           ssh,
		Unicode:       unicode,
		Herdr:         herdr,
		KittyWindowID: kittyWindowID,
		WezTerm:       wezterm,
		TermProgram:   termProgram,
		Term:          term,
	}
}

// isTerminalFd wraps term.IsTerminal for testability.
func isTerminalFd(fd int) bool {
	return term.IsTerminal(fd)
}
