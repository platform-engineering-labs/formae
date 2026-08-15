// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package agent

import (
	"context"
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
	gotCtx  context.Context
}

func (s *stubBootRecorder) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotCtx
}

func (s *stubBootRecorder) RecordAgentBoot(ctx context.Context, version string) error {
	s.mu.Lock()
	s.gotCtx = ctx
	s.mu.Unlock()
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
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

	<-recordBoot(context.Background(), rec, "0.89.0")

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
		<-recordBoot(context.Background(), rec, "0.89.0")
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
		recordBoot(context.Background(), rec, "0.89.0")
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("recordBoot blocked on an unresponsive datastore; startup must not wait on it")
	}

	assert.Empty(t, rec.recorded(), "the write is still in flight, and startup carried on regardless")
}

// Running off the startup goroutine stops a stalled write delaying startup, but
// it hands the same stall to shutdown: the insert keeps a pooled connection
// checked out, and closing the pool waits for it. The write must therefore be
// cancellable by the agent's own context, or a database that stops responding
// turns an ordinary stop into a hang.
func TestRecordBootIsCancelledWithTheAgent(t *testing.T) {
	rec := &stubBootRecorder{release: make(chan struct{})}
	defer close(rec.release)

	ctx, cancel := context.WithCancel(context.Background())
	done := recordBoot(ctx, rec, "0.89.0")

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the agent context must abandon the in-flight boot write")
	}

	require.NotNil(t, rec.context(), "the recorder must be handed a cancellable context")
	assert.ErrorIs(t, rec.context().Err(), context.Canceled)
}
