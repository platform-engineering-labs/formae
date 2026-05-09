// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"io"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"
)

// capturingSender records messages sent via the metaPortConn's sender function.
type capturingSender struct {
	messages []meta.MessagePortData
}

func (s *capturingSender) send(alias gen.Alias, msg any) error {
	if data, ok := msg.(meta.MessagePortData); ok {
		s.messages = append(s.messages, data)
	}
	return nil
}

func TestMetaPortConn_Write(t *testing.T) {
	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)

	payload := []byte("hello world")
	n, err := conn.Write(payload)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("expected %d bytes written, got %d", len(payload), n)
	}

	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sender.messages))
	}

	if string(sender.messages[0].Data) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", sender.messages[0].Data)
	}

	// Verify data was copied (not sharing the original slice)
	payload[0] = 'X'
	if sender.messages[0].Data[0] == 'X' {
		t.Fatal("Write must copy data, not share the slice")
	}
}

func TestMetaPortConn_Read(t *testing.T) {
	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)

	// Feed data as the actor's HandleMessage would
	conn.Feed([]byte("hello "))
	conn.Feed([]byte("world"))

	buf := make([]byte, 20)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Should get "hello " from first feed
	got := string(buf[:n])
	if got != "hello world" {
		t.Logf("first read got %q (partial read is OK)", got)
	}

	// Read remaining
	if n < 11 {
		n2, err := conn.Read(buf[n:])
		if err != nil {
			t.Fatalf("second Read failed: %v", err)
		}
		got = string(buf[:n+n2])
	}

	if got != "hello world" {
		t.Fatalf("expected 'hello world', got %q", got)
	}
}

func TestMetaPortConn_ReadBlocksUntilData(t *testing.T) {
	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 10)
		n, err := conn.Read(buf)
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- string(buf[:n])
	}()

	// Read should be blocking, so nothing yet
	select {
	case <-done:
		t.Fatal("Read returned before data was fed")
	case <-time.After(50 * time.Millisecond):
		// good, still blocked
	}

	// Feed data
	conn.Feed([]byte("data"))

	select {
	case result := <-done:
		if result != "data" {
			t.Fatalf("expected 'data', got %q", result)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock after Feed")
	}
}

func TestMetaPortConn_CloseUnblocksRead(t *testing.T) {
	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 10)
		_, err := conn.Read(buf)
		done <- err
	}()

	// Give Read time to block
	time.Sleep(50 * time.Millisecond)

	_ = conn.Close()

	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("expected io.EOF after Close, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock after Close")
	}
}

func TestMetaPortConn_ReadAfterCloseReturnsBufferedData(t *testing.T) {
	sender := &capturingSender{}
	conn := NewMetaPortConn(gen.Alias{}, sender.send)

	conn.Feed([]byte("buffered"))
	_ = conn.Close()

	buf := make([]byte, 20)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("expected buffered data before EOF, got error: %v", err)
	}
	if string(buf[:n]) != "buffered" {
		t.Fatalf("expected 'buffered', got %q", string(buf[:n]))
	}

	// Next read should return EOF
	_, err = conn.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}
