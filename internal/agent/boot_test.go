// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package agent

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubBootRecorder struct {
	mu      sync.Mutex
	calls   []string
	err     error
	release chan struct{} // when non-nil, RecordAgentBoot blocks until closed
}

func (s *stubBootRecorder) RecordAgentBoot(version string) error {
	if s.release != nil {
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, version)
	return s.err
}

func (s *stubBootRecorder) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func TestRecordBootWritesOnce(t *testing.T) {
	rec := &stubBootRecorder{}

	<-recordBoot(rec, "0.89.0")

	require.Len(t, rec.recorded(), 1, "exactly one boot row per process start")
	assert.Equal(t, []string{"0.89.0"}, rec.recorded())
}

// A failing boot record must not stop the agent. The row exists to tell a
// console which version is running; letting a display feature take down a
// customer's agent inverts the value entirely, which is the same reasoning that
// makes the exporter sidecar non-essential.
func TestRecordBootSwallowsFailure(t *testing.T) {
	rec := &stubBootRecorder{err: errors.New("datastore unavailable")}

	assert.NotPanics(t, func() {
		<-recordBoot(rec, "0.89.0")
	}, "a failed boot record must never propagate or panic")

	assert.Len(t, rec.recorded(), 1, "the failure is not retried in-line")
}

// Swallowing the error is not enough to make the write best-effort: an
// unresponsive datastore blocks in the driver rather than returning, and every
// backend runs this statement on context.Background() with no deadline. If the
// call were synchronous, a stalled database would hold up agent startup
// indefinitely, which is the same outage the swallowed error exists to prevent.
func TestRecordBootDoesNotBlockStartup(t *testing.T) {
	rec := &stubBootRecorder{release: make(chan struct{})}
	defer close(rec.release)

	returned := make(chan struct{})
	go func() {
		recordBoot(rec, "0.89.0")
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("recordBoot blocked on an unresponsive datastore; startup must not wait on it")
	}

	assert.Empty(t, rec.recorded(), "the write is still in flight, and startup carried on regardless")
}
