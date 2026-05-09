// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"io"
	"os"
)

// stdioConn adapts a Reader and WriteCloser into an io.ReadWriteCloser
// suitable for net/rpc. Used by both the plugin server (stdin/stdout)
// and the CLI client (os/exec pipes).
type stdioConn struct {
	io.Reader
	io.WriteCloser
}

func (c *stdioConn) Close() error {
	return c.WriteCloser.Close()
}

func stdinReader() io.Reader {
	return os.Stdin
}

func stdoutWriteCloser() io.WriteCloser {
	return os.Stdout
}
