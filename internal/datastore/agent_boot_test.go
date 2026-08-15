// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every other method on the Datastore interface builds its own context
// internally, so the boot write does too rather than being the single method
// that takes one from its caller.
//
// It still has to be bounded. It is the only write issued off the request path,
// on a detached goroutine at startup, so nothing else limits how long it can
// hold a connection: unbounded, a stalled database keeps that connection
// checked out and closing the pool waits for it, turning an ordinary stop into
// a hang.
func TestAgentBootContextIsBounded(t *testing.T) {
	ctx, cancel := AgentBootContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "the boot write context must carry a deadline")

	remaining := time.Until(deadline)
	assert.Positive(t, remaining, "deadline must be in the future")
	assert.LessOrEqual(t, remaining, AgentBootWriteTimeout,
		"deadline must be within the declared timeout")
}

// The timeout is also the worst case a stop waits on an in-flight boot write,
// so it has to stay short enough that a shutdown is not visibly delayed by a
// display-only write.
func TestAgentBootWriteTimeoutIsShort(t *testing.T) {
	assert.Positive(t, AgentBootWriteTimeout)
	assert.LessOrEqual(t, AgentBootWriteTimeout, 30*time.Second,
		"a longer timeout would let a display-only write visibly delay shutdown")
}

func TestAgentBootContextIsCancellable(t *testing.T) {
	ctx, cancel := AgentBootContext()

	cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel must release the context")
	}
}
