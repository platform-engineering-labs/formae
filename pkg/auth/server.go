// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"io"
	"net/rpc"
)

// Serve runs the auth plugin RPC server over the given connection.
// Plugin authors call this from main() with os.Stdin/os.Stdout.
// The function blocks until the connection is closed.
func Serve(plugin AuthPlugin, conn io.ReadWriteCloser) {
	srv := rpc.NewServer()
	srv.RegisterName("AuthPlugin", plugin)
	srv.ServeConn(conn)
}

// Run is a convenience function for plugin authors. It creates a stdioConn
// from os.Stdin/os.Stdout and calls Serve.
func Run(plugin AuthPlugin) {
	conn := &stdioConn{
		Reader:      stdinReader(),
		WriteCloser: stdoutWriteCloser(),
	}
	Serve(plugin, conn)
}
