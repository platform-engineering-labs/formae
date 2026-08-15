// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package agent

import (
	"context"
	"log/slog"
)

// bootRecorder is the slice of the datastore this file needs: appending one row
// per agent start.
type bootRecorder interface {
	RecordAgentBoot(ctx context.Context, version string) error
}

// recordBoot appends the boot row for this process start, off the startup path.
//
// The row exists so a reader outside this process can answer "which version is
// this agent" for an installation that has not yet run any command. It is a
// display concern, and a display concern must not be able to stop or stall a
// customer's agent from starting.
//
// That takes two things, not one. Swallowing the error covers a datastore that
// says no. Running off the startup goroutine covers a datastore that says
// nothing at all: every backend issues this statement on context.Background()
// with no deadline, so an unresponsive database blocks in the driver rather
// than returning, and a synchronous call would hold up startup indefinitely.
//
// The context is the agent's own, so a stop cancels an in-flight write. Without
// that, the goroutine keeps a pooled connection checked out and closing the pool
// waits for it, which turns the stalled-database case into a hung shutdown
// instead of a hung startup.
//
// The returned channel closes once the attempt finishes. Startup ignores it;
// tests wait on it rather than sleeping.
//
// Not retried: a retry that mattered would have to be waited on, and a
// background one is a backoff and a lifecycle to get right for a version
// string. The cost is that a transient failure leaves the reader on the
// previous boot's version until the next start.
func recordBoot(ctx context.Context, ds bootRecorder, version string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := ds.RecordAgentBoot(ctx, version); err != nil {
			slog.Warn("Failed to record agent boot; continuing startup", "error", err)
		}
	}()
	return done
}
