// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package agent

import "log/slog"

// bootRecorder is the slice of the datastore this file needs: appending one row
// per agent start.
type bootRecorder interface {
	RecordAgentBoot(version string) error
}

// recordBoot appends the boot row for this process start.
//
// Best-effort by design, and never fatal. The row exists so a reader outside
// this process can answer "which version is this agent" for an installation
// that has not yet run any command. A display concern must not be able to stop
// a customer's agent from starting, so a failure is logged and abandoned.
//
// Not retried: a retry that mattered would have to block startup, and a
// background one is a goroutine, a backoff and a lifecycle to get right for a
// version string. The cost is that a transient failure leaves the reader on the
// previous boot's version until the next start.
func recordBoot(ds bootRecorder, version string) {
	if err := ds.RecordAgentBoot(version); err != nil {
		slog.Warn("Failed to record agent boot; continuing startup", "error", err)
	}
}
