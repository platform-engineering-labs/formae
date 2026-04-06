// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"io"
	"net/rpc"
	"os"
	"time"
)

// Serve runs the auth plugin RPC server over the given connection.
// Plugin authors call this from main() with os.Stdin/os.Stdout.
// The function blocks until the connection is closed.
func Serve(plugin AuthPlugin, conn io.ReadWriteCloser) {
	srv := rpc.NewServer()
	srv.RegisterName("AuthPlugin", plugin)
	srv.ServeConn(conn)
}

// ReadySignal is the single byte written to stdout by Run before starting
// the RPC server. The host process uses this to detect that the plugin is
// alive and ready to accept RPC calls.
const ReadySignal byte = 0x01

// Run is a convenience function for plugin authors. It creates a stdioConn
// from os.Stdin/os.Stdout and calls Serve. Before entering the RPC loop it
// writes a single ready-signal byte so the host can detect readiness without
// polling.
//
// The plugin exits automatically when the host process dies: a background
// goroutine monitors the parent PID and calls os.Exit when it changes
// (i.e., the parent was killed and the plugin was reparented to init/PID 1).
func Run(plugin AuthPlugin) {
	// Signal readiness before creating the stdioConn so the marker byte
	// is separate from the RPC stream.
	os.Stdout.Write([]byte{ReadySignal})
	os.Stdout.Sync()

	// Monitor parent process — exit if reparented (parent died).
	go monitorParent()

	conn := &stdioConn{
		Reader:      stdinReader(),
		WriteCloser: stdoutWriteCloser(),
	}
	Serve(plugin, conn)
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
