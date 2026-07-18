// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"time"

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

	// Env hints — insufficient alone to enable graphics
	KittyWindowID bool   // KITTY_WINDOW_ID is set
	WezTerm       bool   // WEZTERM_EXECUTABLE or similar is set
	TermProgram   string // value of TERM_PROGRAM
	Term          string // value of TERM
}

// probeResult holds the results of the raw-mode escape-sequence query.
// Both fields are false when the probe was skipped (tmux/ssh) or timed out.
type probeResult struct {
	Kitty  bool
	ITerm2 bool
}

// decide is a pure function: given env signals and probe results it returns
// the appropriate Capability.  The layered, fail-safe order is:
//
//  1. Hard disqualifiers → CapText
//  2. Probe results (trusted only when NOT inside tmux/screen/ssh)
//     probe.Kitty → CapKitty; probe.ITerm2 → CapITerm2
//  3. Env hints are NOT sufficient to enable graphics; they only confirm
//     braille is a safe choice
//  4. TTY + Unicode, no confirmed graphics → CapBraille
func decide(env envInfo, probe probeResult) Capability {
	// Layer 1: hard disqualifiers
	if !env.IsTTY || env.Dumb || env.CI || !env.Unicode {
		return CapText
	}

	// Layer 2: probe results — only trust them outside tmux/screen/ssh
	if !env.Tmux && !env.SSH {
		if probe.Kitty {
			return CapKitty
		}
		if probe.ITerm2 {
			return CapITerm2
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
// Under tmux/ssh the seam returns an empty probeResult immediately.
//
// Fail-safe invariant: any error, timeout, or uncertainty leaves all fields
// false — decide() then lands on braille/text.  The probe only ever *adds*
// graphics capability on a clear positive signal.
//
//nolint:gochecknoglobals
var probe = func(env envInfo) probeResult {
	// Skip the escape-sequence probe entirely when inside tmux/screen/ssh —
	// a garbled escape in the banner is worse than no logo graphics.
	if env.Tmux || env.SSH {
		return probeResult{}
	}

	// Only probe when stdout is a real TTY; we need raw mode.
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return probeResult{}
	}

	old, err := term.MakeRaw(fd)
	if err != nil {
		return probeResult{}
	}
	defer func() { _ = term.Restore(fd, old) }()

	// Send the Kitty APC graphics query followed by a Primary Device
	// Attributes (DA1) request.  A Kitty-capable terminal answers the APC
	// query (ESC _ G i=31;OK ESC \) BEFORE the DA1 reply (ESC [ ? … c).
	// A non-Kitty terminal silently ignores the APC and replies only to DA1.
	//
	// Query breakdown:
	//   \x1b_G          — APC introducer + 'G' (Kitty graphics)
	//   i=31,s=1,v=1,   — image id 31, size 1×1
	//   a=q,t=d,f=24;   — action=query, transmission=direct, format=24bpp
	//   AAAAAAAA        — minimal base64 payload (3 zero bytes)
	//   \x1b\\          — String Terminator
	//   \x1b[c          — DA1 request (forces a reply from any VT100+ terminal)
	const kittyQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAAAAAA\x1b\\\x1b[c"
	if _, werr := os.Stdout.WriteString(kittyQuery); werr != nil {
		return probeResult{}
	}

	// Read the response with a hard timeout using a goroutine + select so the
	// main flow can NEVER block.  os.Stdout.SetDeadline does not work on
	// *os.File — do NOT rely on it.  Instead, the goroutine reads from the
	// tty and the select races against time.After.  A possibly-blocked reader
	// goroutine at one-time startup is acceptable — it unblocks at program exit
	// or when the terminal eventually sends the DA1 reply.
	tty, terr := os.Open("/dev/tty")
	if terr != nil {
		return probeResult{}
	}
	defer tty.Close()

	type readResult struct {
		buf []byte
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := tty.Read(buf)
		ch <- readResult{buf: buf[:n], err: err}
	}()

	var result probeResult
	select {
	case rr := <-ch:
		if rr.err == nil {
			result.Kitty = parseKittyProbe(rr.buf)
		}
		// On read error → result.Kitty stays false (fail-safe).
	case <-time.After(150 * time.Millisecond):
		// Timeout → treat as no graphics support (fail-safe).
	}

	// iTerm2: there is NO escape-sequence query for iTerm2 inline images —
	// this is a documented exception.  TERM_PROGRAM=iTerm.app is set
	// exclusively by iTerm2 itself (not inherited like KITTY_WINDOW_ID), so
	// it is a strong enough signal to use without a protocol probe.
	// We only set ITerm2 when Kitty was not confirmed, to avoid double-enable.
	if !result.Kitty && env.TermProgram == "iTerm.app" {
		result.ITerm2 = true
	}

	return result
}

// detectOnce and detectCached hold the memoized Detect() result.
//
//nolint:gochecknoglobals
var (
	detectOnce   sync.Once
	detectCached Capability
)

// Detect gathers environment signals, runs the probe seam, calls decide, and
// returns the result — computing it only once (cached via sync.Once).
func Detect() Capability {
	detectOnce.Do(func() {
		env := gatherEnv()
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
