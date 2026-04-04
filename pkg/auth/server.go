// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"io"
	"net/rpc"
	"os"
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
func Run(plugin AuthPlugin) {
	// Signal readiness before creating the stdioConn so the marker byte
	// is separate from the RPC stream.
	os.Stdout.Write([]byte{ReadySignal})
	os.Stdout.Sync()

	conn := &stdioConn{
		Reader:      stdinReader(),
		WriteCloser: stdoutWriteCloser(),
	}
	Serve(plugin, conn)
}
