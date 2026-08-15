// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"time"
)

// AgentBootWriteTimeout bounds a single boot write.
//
// RecordAgentBoot is the only write issued off the request path: the agent
// starts it on a detached goroutine so a stalled database cannot delay startup.
// That leaves nothing else to limit how long it may run, and an unbounded
// insert keeps a connection checked out, which closing the pool then waits for,
// turning an ordinary stop into a hang.
//
// It is therefore also the worst case a shutdown waits on the write, so it is
// deliberately short: a display-only record must not visibly delay a stop.
const AgentBootWriteTimeout = 10 * time.Second

// AgentBootContext returns the bounded context a backend uses for its boot
// write, so all four share one definition of how long that write may take
// rather than each inventing its own.
//
// The caller must call the returned cancel function.
func AgentBootContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), AgentBootWriteTimeout)
}
