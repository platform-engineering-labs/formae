// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unix

package logo

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	// probeTimeout bounds how long the banner waits for the terminal to start
	// answering. Only terminals known to answer are probed (graphicsCandidate),
	// and they answer the DA1 request within milliseconds (iTerm2 3.6.11 for
	// reference: p99 26 ms over 200 probes); the budget only matters for a
	// terminal that is busy.
	probeTimeout = 200 * time.Millisecond
	// probeGrace is the extra time granted once the terminal has started
	// answering (an ESC has arrived), so a reply split across the deadline is
	// read to its end instead of leaking its tail into the shell or the next
	// prompt. Typed-ahead keys alone do not extend the wait.
	probeGrace = 300 * time.Millisecond
	// drainLimit bounds drainPendingInput when input keeps arriving.
	drainLimit = 100 * time.Millisecond
)

// kittyQuery is the Kitty APC graphics query followed by a Primary Device
// Attributes (DA1) request. A Kitty-capable terminal answers the APC query
// (ESC _ G i=31;OK ESC \) BEFORE the DA1 reply (ESC [ ? … c); a terminal
// without Kitty graphics ignores the APC and answers only DA1, so the DA1 reply
// marks the end of everything the terminal sends back.
//
// Query breakdown:
//
//	\x1b_G          — APC introducer + 'G' (Kitty graphics)
//	i=31,s=1,v=1,   — image id 31, size 1×1
//	a=q,t=d,f=24;   — action=query, transmission=direct, format=24bpp
//	AAAA            — minimal base64 payload (3 zero bytes)
//	\x1b\\          — String Terminator
//	\x1b[c          — DA1 request (forces a reply from any VT100+ terminal)
const kittyQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"

// da1Reply matches a complete DA1 response (ESC [ ? params c) at the end of
// the bytes read so far.
var da1Reply = regexp.MustCompile(`\x1b\[\?[0-9;]*c$`)

// queryKittyGraphicsTTY sends kittyQuery to the terminal in raw mode and reads
// the reply on the calling goroutine.
//
// The previous reader was a goroutine blocked in read(2) on the terminal. On
// macOS a blocking terminal read is not interrupted by Close (measured: still
// blocked 2 s after Close), so after a timeout that goroutine survived, the
// terminal went back to cooked mode, and a late reply was echoed as text and
// then consumed — together with the user's first keystrokes — by the stale read
// instead of the prompt that followed. Here the read is driven by select(2) with
// a deadline, so nothing outlives the probe.
//
// The probe only runs when stdin and stdout are the same terminal: the query
// goes out on stdout, raw mode is set on that terminal, and the reply is read
// from stdin, so the query, the mode change and the reply all concern the
// terminal the banner is drawn on. Input is read one byte at a time and reading
// stops exactly at the end of the DA1 reply, so keystrokes typed after it stay
// queued for the prompt (keys typed in the few milliseconds before the reply
// arrives are indistinguishable from it and are consumed, as before).
func queryKittyGraphicsTTY() probeResult {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd()) //nolint:gosec // descriptor numbers fit in int
	// select(2) cannot wait on descriptors at or above FD_SETSIZE; check before
	// anything is sent so no reply can be left unread.
	if in >= unix.FD_SETSIZE || !term.IsTerminal(in) || !term.IsTerminal(out) || !sameTerminal(in, out) {
		return probeResult{}
	}
	// A background job (`formae … &`) must not touch the terminal: setting raw
	// mode from a background process group raises SIGTTOU, whose default action
	// stops the process. termenv applies the same check before its queries.
	if !isForeground(in) {
		return probeResult{}
	}

	old, err := term.MakeRaw(in)
	if err != nil {
		return probeResult{}
	}
	defer func() { _ = term.Restore(in, old) }()

	if _, err := os.Stdout.WriteString(kittyQuery); err != nil {
		return probeResult{}
	}

	resp, complete := readUntilDA1(in, time.Now().Add(probeTimeout), probeGrace)
	if !complete {
		drainPendingInput(in)
	}
	return probeResult{Kitty: parseKittyProbe(resp)}
}

// isForeground reports whether this process's group is the terminal's
// foreground process group.
func isForeground(fd int) bool {
	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return false
	}
	return pgrp == unix.Getpgrp()
}

// sameTerminal reports whether two descriptors refer to the same terminal
// device: both character devices with the same device number.
func sameTerminal(a, b int) bool {
	var sa, sb unix.Stat_t
	if unix.Fstat(a, &sa) != nil || unix.Fstat(b, &sb) != nil {
		return false
	}
	return sa.Rdev == sb.Rdev && sa.Mode&unix.S_IFMT == unix.S_IFCHR && sb.Mode&unix.S_IFMT == unix.S_IFCHR
}

// readUntilDA1 reads fd one byte at a time until a complete DA1 reply ends the
// input read so far, or the deadline passes. Once a reply has started (an ESC
// has arrived) the deadline is extended once by grace, so a reply split across
// the deadline is still read to its end. It reports whether the DA1 reply was seen; the
// bytes read are returned either way.
func readUntilDA1(fd int, deadline time.Time, grace time.Duration) ([]byte, bool) {
	var acc []byte
	extended := false
	var b [1]byte
	for {
		// The deadline is checked before every wait, not only when nothing is
		// readable: input that keeps arriving (a paste, a stuck key) must not
		// keep the probe reading past its budget.
		if !time.Now().Before(deadline) {
			if !extended && bytes.IndexByte(acc, 0x1b) >= 0 {
				extended = true
				deadline = deadline.Add(grace)
				continue
			}
			return acc, false
		}
		ready, ok := waitReadable(fd, deadline)
		if !ok {
			return acc, false
		}
		if !ready {
			continue
		}
		n, err := unix.Read(fd, b[:])
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil || n <= 0 {
			return acc, false
		}
		acc = append(acc, b[0])
		if b[0] == 'c' && da1Reply.Match(acc) {
			return acc, true
		}
	}
}

// drainPendingInput reads and discards whatever input is already waiting on fd,
// without blocking, so a partial reply cannot surface as user input later. It
// stops after drainLimit even if input keeps arriving.
func drainPendingInput(fd int) {
	buf := make([]byte, 256)
	stop := time.Now().Add(drainLimit)
	for time.Now().Before(stop) {
		ready, ok := waitReadable(fd, time.Now())
		if !ok || !ready {
			return
		}
		if n, err := unix.Read(fd, buf); err != nil || n <= 0 {
			return
		}
	}
}

// waitReadable waits until fd is readable or the deadline passes (a deadline
// in the past polls once). ok is false when fd cannot be waited on.
//
// It uses select(2), not poll(2): macOS poll does not support terminal devices
// (it reports POLLNVAL for them; see BUGS in poll(2)), which is also why
// termenv waits on the terminal with select. A signal interrupting the wait is
// retried with the time remaining until the same deadline.
func waitReadable(fd int, deadline time.Time) (ready, ok bool) {
	if fd < 0 || fd >= unix.FD_SETSIZE {
		return false, false
	}
	for {
		var set unix.FdSet
		set.Zero()
		set.Set(fd)
		tv := unix.NsecToTimeval(max(0, time.Until(deadline)).Nanoseconds())
		n, err := unix.Select(fd+1, &set, nil, nil, &tv)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, false
		}
		return n > 0 && set.IsSet(fd), true
	}
}
