// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"fmt"
	"io"
	"net/rpc"
	"os"
	"time"
)

// serve registers plugin as the "AuthPlugin" RPC service and runs the
// server over conn, returning an error rather than leaving a server with
// nothing registered when registration fails.
//
// A nil plugin is the one registration failure that reaches here: net/rpc's
// RegisterName panics on a nil interface (reflect has no method set to
// inspect), so it's rejected explicitly before ever reaching RegisterName.
func serve(plugin AuthPlugin, conn io.ReadWriteCloser) error {
	if plugin == nil {
		return fmt.Errorf("auth server: plugin is nil")
	}
	srv := rpc.NewServer()
	if err := srv.RegisterName("AuthPlugin", plugin); err != nil {
		return fmt.Errorf("auth server: register plugin: %w", err)
	}
	srv.ServeConn(conn)
	return nil
}

// Serve runs the auth plugin RPC server over the given connection.
// Plugin authors call this from main() with os.Stdin/os.Stdout.
// The function blocks until the connection is closed.
//
// A plugin that can't be registered can't serve any RPC, so Serve reports
// the failure on stderr and exits non-zero instead of leaving a server with
// nothing registered running.
func Serve(plugin AuthPlugin, conn io.ReadWriteCloser) {
	if err := serve(plugin, conn); err != nil {
		fatal(err)
	}
}

// ReadySignal is the single byte written to stdout by Run before starting
// the RPC server. The host process uses this to detect that the plugin is
// alive and ready to accept RPC calls.
const ReadySignal byte = 0x01

// readyWriter is the subset of *os.File that the readiness handshake needs:
// write the marker byte, then attempt to flush it.
type readyWriter interface {
	io.Writer
	Sync() error
}

// signalReady writes the readiness marker byte and attempts to flush it.
// Only a failed write is treated as fatal: the write already makes the byte
// visible to a reader on the other end of a pipe. Sync is best-effort — in
// the normal deployment the plugin's stdout is a pipe (the host spawns the
// plugin via os/exec with a pipe, not a regular file), and fsync on a pipe
// always fails with EINVAL, so that failure is expected and not a sign the
// handshake didn't reach the host.
func signalReady(ready readyWriter) error {
	if _, err := ready.Write([]byte{ReadySignal}); err != nil {
		return fmt.Errorf("auth run: signal ready: %w", err)
	}
	_ = ready.Sync()
	return nil
}

// run wires up Run's startup sequence and returns an error instead of
// exiting, so the caller controls how a failure surfaces.
func run(plugin AuthPlugin, ready readyWriter) error {
	// Signal readiness before creating the stdioConn so the marker byte
	// is separate from the RPC stream.
	if err := signalReady(ready); err != nil {
		return err
	}

	// Monitor parent process — exit if reparented (parent died).
	go monitorParent()

	conn := &stdioConn{
		Reader:      stdinReader(),
		WriteCloser: stdoutWriteCloser(),
	}
	return serve(plugin, conn)
}

// Run is a convenience function for plugin authors. It creates a stdioConn
// from os.Stdin/os.Stdout and calls Serve. Before entering the RPC loop it
// writes a single ready-signal byte so the host can detect readiness without
// polling.
//
// The plugin exits automatically when the host process dies: a background
// goroutine monitors the parent PID and calls os.Exit when it changes
// (i.e., the parent was killed and the plugin was reparented to init/PID 1).
//
// If the readiness handshake or registration fails, the host would
// otherwise wait forever for a byte that never arrives, with no diagnostic.
// Run reports the failure on stderr instead and exits the process non-zero.
func Run(plugin AuthPlugin) {
	if err := run(plugin, os.Stdout); err != nil {
		fatal(err)
	}
}

// processExit is a package-level indirection over os.Exit so tests can
// observe a fatal startup failure without terminating the test process.
var processExit = os.Exit

// fatal reports a fatal startup error on the plugin's stderr — which the
// host process already forwards to its own stderr, see client.go's
// cmd.Stderr wiring — and exits non-zero.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "auth plugin:", err)
	processExit(1)
}

// monitorParent polls the parent PID. On Linux, when the parent process dies
// the child is reparented to PID 1 (or a subreaper). Detecting this change
// lets us exit cleanly even if stdin pipes aren't closed promptly.
func monitorParent() {
	ppid := os.Getppid()
	for {
		time.Sleep(1 * time.Second)
		if os.Getppid() != ppid {
			os.Exit(0)
		}
	}
}
