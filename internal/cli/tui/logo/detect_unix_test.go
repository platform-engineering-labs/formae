// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit && unix

package logo

import (
	"os"
	"testing"
	"time"
)

// pipeFD returns a pipe whose read end stands in for the terminal.
func pipeFD(t *testing.T) (r *os.File, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// A Kitty reply that arrives in separate bursts (APC answer, then DA1) is read
// to the end of the DA1 response and recognised.
func TestReadUntilDA1_SplitKittyReply(t *testing.T) {
	r, w := pipeFD(t)
	go func() {
		_, _ = w.WriteString("\x1b_Gi=31;OK\x1b\\")
		time.Sleep(20 * time.Millisecond)
		_, _ = w.WriteString("\x1b[?64;1;2;4;6;17;18;21;22;52")
		time.Sleep(20 * time.Millisecond)
		_, _ = w.WriteString("c")
	}()

	resp, complete := readUntilDA1(int(r.Fd()), time.Now().Add(2*time.Second), 0) //nolint:gosec
	if !complete {
		t.Fatalf("reply not recognised as complete; got %q", resp)
	}
	if !parseKittyProbe(resp) {
		t.Errorf("Kitty OK not recognised in %q", resp)
	}
}

// A terminal without Kitty graphics answers only DA1: complete, not Kitty.
// A 'c' inside the payload of something other than DA1 must not end the read.
func TestReadUntilDA1_DA1OnlyAndStrayC(t *testing.T) {
	r, w := pipeFD(t)
	go func() {
		_, _ = w.WriteString("\x1b_Gi=31;ENOTSUPPORTED:cannot\x1b\\")
		time.Sleep(20 * time.Millisecond)
		_, _ = w.WriteString("\x1b[?1;2c")
	}()

	resp, complete := readUntilDA1(int(r.Fd()), time.Now().Add(2*time.Second), 0) //nolint:gosec
	if !complete {
		t.Fatalf("DA1 reply not recognised; got %q", resp)
	}
	if string(resp[len(resp)-len("\x1b[?1;2c"):]) != "\x1b[?1;2c" {
		t.Errorf("read stopped before the DA1 reply; got %q", resp)
	}
	if parseKittyProbe(resp) {
		t.Errorf("error payload must not count as Kitty support: %q", resp)
	}
}

// With no reply the read returns at the deadline instead of blocking, and no
// reader is left behind (the call returns on the calling goroutine).
func TestReadUntilDA1_NoReplyReturnsAtDeadline(t *testing.T) {
	r, _ := pipeFD(t)
	start := time.Now()
	resp, complete := readUntilDA1(int(r.Fd()), start.Add(100*time.Millisecond), 0) //nolint:gosec
	elapsed := time.Since(start)
	if complete || len(resp) != 0 {
		t.Errorf("expected an empty, incomplete read; got complete=%v %q", complete, resp)
	}
	if elapsed < 90*time.Millisecond || elapsed > time.Second {
		t.Errorf("returned after %v, want about the 100ms deadline", elapsed)
	}
}

// A partial reply left at the deadline is drained, so nothing is left to be
// read (or echoed) once the terminal is back in cooked mode.
func TestDrainPendingInput(t *testing.T) {
	r, w := pipeFD(t)
	if _, err := w.WriteString("\x1b_Gi=31;OK\x1b\\\x1b[?62;"); err != nil {
		t.Fatal(err)
	}
	fd := int(r.Fd()) //nolint:gosec
	drainPendingInput(fd)
	if ready, _ := waitReadable(fd, time.Now()); ready {
		t.Error("input still pending after drainPendingInput")
	}
}

// Keystrokes typed after the reply stay queued for the prompt that follows:
// the read stops exactly at the end of the DA1 reply.
func TestReadUntilDA1_LeavesTypeaheadUnread(t *testing.T) {
	r, w := pipeFD(t)
	if _, err := w.WriteString("\x1b_Gi=31;OK\x1b\\\x1b[?1;2cyes\r"); err != nil {
		t.Fatal(err)
	}
	fd := int(r.Fd()) //nolint:gosec
	resp, complete := readUntilDA1(fd, time.Now().Add(time.Second), 0)
	if !complete || !parseKittyProbe(resp) {
		t.Fatalf("reply not read correctly: complete=%v %q", complete, resp)
	}
	rest := make([]byte, 16)
	n, err := r.Read(rest)
	if err != nil || string(rest[:n]) != "yes\r" {
		t.Errorf("typeahead after the reply = %q (err %v), want %q left unread", rest[:n], err, "yes\r")
	}
}

// A reply that has started before the deadline is read to its end within the
// grace period instead of leaving its tail behind.
func TestReadUntilDA1_GraceCompletesSplitReply(t *testing.T) {
	r, w := pipeFD(t)
	go func() {
		_, _ = w.WriteString("\x1b_Gi=31;OK\x1b\\\x1b[?62;")
		time.Sleep(150 * time.Millisecond) // past the 50ms deadline below
		_, _ = w.WriteString("22c")
	}()
	resp, complete := readUntilDA1(int(r.Fd()), time.Now().Add(50*time.Millisecond), time.Second) //nolint:gosec
	if !complete {
		t.Fatalf("split reply not completed within the grace period; got %q", resp)
	}

	// Without grace the same split reply is cut off at the deadline.
	r2, w2 := pipeFD(t)
	go func() {
		_, _ = w2.WriteString("\x1b[?62;")
		time.Sleep(150 * time.Millisecond)
		_, _ = w2.WriteString("22c")
	}()
	if _, complete := readUntilDA1(int(r2.Fd()), time.Now().Add(50*time.Millisecond), 0); complete { //nolint:gosec
		t.Error("without grace the reply must be reported incomplete")
	}
}

// Typed-ahead keys that arrive before any reply do not extend the wait: only
// an ESC (the start of a reply) does.
func TestReadUntilDA1_TypeaheadDoesNotExtendDeadline(t *testing.T) {
	r, w := pipeFD(t)
	if _, err := w.WriteString("y"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, complete := readUntilDA1(int(r.Fd()), start.Add(50*time.Millisecond), time.Second) //nolint:gosec
	if complete {
		t.Fatal("no reply was sent; must be incomplete")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("waited %v: typeahead must not trigger the grace period", elapsed)
	}
}

// writeContinuously keeps the pipe full (no DA1 reply) until the test ends,
// like a large paste: a reader never finds it empty.
func writeContinuously(t *testing.T, w *os.File) {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'x'
	}
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}()
}

// Input that never stops arriving must not keep the probe reading past its
// deadline.
func TestReadUntilDA1_ContinuousInputStopsAtDeadline(t *testing.T) {
	r, w := pipeFD(t)
	writeContinuously(t, w)

	start := time.Now()
	_, complete := readUntilDA1(int(r.Fd()), start.Add(100*time.Millisecond), 0) //nolint:gosec
	if complete {
		t.Fatal("no DA1 was sent, yet the reply was reported complete")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("readUntilDA1 ran %v with a 100 ms deadline", elapsed)
	}
}

// drainPendingInput must return even while input keeps arriving.
func TestDrainPendingInput_ContinuousInputIsBounded(t *testing.T) {
	r, w := pipeFD(t)
	writeContinuously(t, w)
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	drainPendingInput(int(r.Fd())) //nolint:gosec
	if elapsed := time.Since(start); elapsed > drainLimit+400*time.Millisecond {
		t.Fatalf("drainPendingInput ran %v, limit %v", elapsed, drainLimit)
	}
}
