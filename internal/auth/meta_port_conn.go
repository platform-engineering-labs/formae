// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"bytes"
	"io"
	"sync"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"
)

// MetaPortConn bridges an Ergo actor's meta.Port messages with an io.ReadWriteCloser
// interface, making it usable as a transport for net/rpc.
//
// Write sends data to the meta.Port (which writes to the child's stdin).
// Read blocks until data arrives from the actor's HandleMessage (child's stdout).
type MetaPortConn struct {
	alias  gen.Alias
	sender func(gen.Alias, any) error

	mu     sync.Mutex
	buf    bytes.Buffer
	signal chan struct{}
	closed bool
}

// NewMetaPortConn creates a new MetaPortConn.
// The sender function should send a message to the meta.Port alias (e.g., actor.SendAlias).
func NewMetaPortConn(alias gen.Alias, sender func(gen.Alias, any) error) *MetaPortConn {
	return &MetaPortConn{
		alias:  alias,
		sender: sender,
		signal: make(chan struct{}, 1),
	}
}

// Write sends data to the meta.Port, which forwards it to the child process's stdin.
// The data is copied to avoid sharing the caller's slice.
func (c *MetaPortConn) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	err := c.sender(c.alias, meta.MessagePortData{Data: cp})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Read blocks until data is available from the actor's HandleMessage.
// Returns io.EOF when the connection is closed and all buffered data is consumed.
func (c *MetaPortConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	for c.buf.Len() == 0 && !c.closed {
		c.mu.Unlock()
		<-c.signal
		c.mu.Lock()
	}
	if c.closed && c.buf.Len() == 0 {
		c.mu.Unlock()
		return 0, io.EOF
	}
	n, err := c.buf.Read(p)
	c.mu.Unlock()
	return n, err
}

// Feed is called by the actor's HandleMessage when binary data arrives
// from the child process's stdout via meta.Port.
func (c *MetaPortConn) Feed(data []byte) {
	c.mu.Lock()
	c.buf.Write(data)
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// Close marks the connection as closed and unblocks any pending Read.
func (c *MetaPortConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
	return nil
}
