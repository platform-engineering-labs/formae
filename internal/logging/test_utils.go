// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logging

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// TestLogCapture is a thread-safe log writer for test assertions
type TestLogCapture struct {
	mu      sync.RWMutex
	Entries []string
	tee     io.Writer // optional writer to tee output to (e.g., os.Stderr)
}

func NewTestLogCapture() *TestLogCapture {
	return &TestLogCapture{
		Entries: make([]string, 0),
		tee:     os.Stderr, // Default: also write to stderr so logs are visible in tests
	}
}

// NewTestLogCaptureQuiet creates a TestLogCapture that doesn't output to stderr
func NewTestLogCaptureQuiet() *TestLogCapture {
	return &TestLogCapture{
		Entries: make([]string, 0),
		tee:     nil,
	}
}

func (c *TestLogCapture) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Entries = append(c.Entries, string(p))
	if c.tee != nil {
		_, _ = c.tee.Write(p)
	}
	return len(p), nil
}

// ContainsAll returns true if all substrings are found in the log entries
func (c *TestLogCapture) ContainsAll(substrs ...string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, substr := range substrs {
		found := false
		for _, entry := range c.Entries {
			if strings.Contains(entry, substr) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// WaitForLog polls for a log entry containing substr within timeout
func (c *TestLogCapture) WaitForLog(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		for _, entry := range c.Entries {
			if strings.Contains(entry, substr) {
				c.mu.RUnlock()
				return true
			}
		}
		c.mu.RUnlock()
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// GetEntries returns a copy of all log entries
func (c *TestLogCapture) GetEntries() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := make([]string, len(c.Entries))
	copy(entries, c.Entries)
	return entries
}

// Clear clears all captured log entries
func (c *TestLogCapture) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Entries = make([]string, 0)
}
