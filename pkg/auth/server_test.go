// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestServe_NilPlugin_ReturnsErrorWithoutPanicking exercises the failure
// net/rpc otherwise produces for a nil AuthPlugin: RegisterName panics
// rather than returning an error, because reflect can't inspect a nil
// interface's method set. serve must turn that into a clean error instead
// of letting the panic reach the caller.
func TestServe_NilPlugin_ReturnsErrorWithoutPanicking(t *testing.T) {
	_, serverConn := pipeConn()

	var panicked any
	err := func() (err error) {
		defer func() {
			panicked = recover()
		}()
		return serve(nil, serverConn)
	}()

	if panicked != nil {
		t.Fatalf("serve panicked: %v", panicked)
	}
	if err == nil {
		t.Fatal("expected serve to return an error for a nil plugin")
	}
}

// failingWriter fails every Write, modelling a broken stdout (e.g. a closed
// or full pipe) during the readiness handshake.
type failingWriter struct {
	err error
}

func (f *failingWriter) Write(p []byte) (int, error) { return 0, f.err }
func (f *failingWriter) Sync() error                 { return nil }

// failingSyncer succeeds at Write but fails Sync.
type failingSyncer struct {
	err error
}

func (f *failingSyncer) Write(p []byte) (int, error) { return 1, nil }
func (f *failingSyncer) Sync() error                 { return f.err }

func TestSignalReady_WriteFailure_ReturnsError(t *testing.T) {
	wantErr := errors.New("broken pipe")
	err := signalReady(&failingWriter{err: wantErr})
	if err == nil {
		t.Fatal("expected signalReady to return an error when the write fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error to wrap %v, got %v", wantErr, err)
	}
}

// TestSignalReady_SyncFailure_IsNonFatal covers the case that actually
// occurs in every real deployment: the plugin's stdout is a pipe (the host
// spawns it via os/exec with a pipe, not a regular file), and fsync on a
// pipe always fails with EINVAL. The write already delivered the byte, so
// this must not be treated as a startup failure.
func TestSignalReady_SyncFailure_IsNonFatal(t *testing.T) {
	err := signalReady(&failingSyncer{err: errors.New("sync: invalid argument")})
	if err != nil {
		t.Fatalf("expected a Sync failure to be non-fatal, got %v", err)
	}
}

// TestFatal_ReportsToStderrAndExitsNonZero checks fatal's contract: report
// on stderr, then exit non-zero. processExit is swapped for a recording
// stub so the test process itself doesn't exit.
func TestFatal_ReportsToStderrAndExitsNonZero(t *testing.T) {
	origExit := processExit
	var exitCode int
	exitCalled := false
	processExit = func(code int) {
		exitCalled = true
		exitCode = code
	}
	t.Cleanup(func() { processExit = origExit })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w

	fatal(fmt.Errorf("startup failed"))

	os.Stderr = origStderr
	if err := w.Close(); err != nil {
		t.Fatalf("close write end: %v", err)
	}
	var buf [256]byte
	n, _ := r.Read(buf[:])
	if err := r.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}

	if !exitCalled {
		t.Fatal("expected fatal to call processExit")
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(string(buf[:n]), "startup failed") {
		t.Fatalf("expected stderr to contain the error, got %q", string(buf[:n]))
	}
}

// TestServe_NilPlugin_ReportsFatalInsteadOfPanicking checks the exported
// Serve wrapper: a registration failure must reach fatal (and, through it,
// stderr and a non-zero exit) rather than crash the process.
func TestServe_NilPlugin_ReportsFatalInsteadOfPanicking(t *testing.T) {
	origExit := processExit
	exitCalled := false
	processExit = func(code int) { exitCalled = true }
	t.Cleanup(func() { processExit = origExit })

	_, serverConn := pipeConn()
	Serve(nil, serverConn)

	if !exitCalled {
		t.Fatal("expected Serve to report the failure through fatal/processExit")
	}
}
