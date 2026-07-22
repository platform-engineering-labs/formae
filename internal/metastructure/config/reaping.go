// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"fmt"
	"time"
)

// Target-reaping timing configuration.
//
// A target is reaped once it has been continuously unreachable for its
// configured reap-after duration. The reaper actor drives this lifecycle:
//
//   - ReaperInterval is how often the reaper evaluates targets for reaping.
//   - MaxBeatGap is the largest tolerated gap between successive health beats
//     before a target is treated as unobserved. It is the anti-fabrication
//     guard: when the observed_at → last_sample_at gap exceeds it, accrual for
//     that step is dropped (the clock-stop) because we cannot trust that the
//     target was continuously unreachable across such a long unobserved window
//     (e.g. the reaper or agent was itself down).
//   - MinReapDuration is the floor for any target's reap-after duration. It must
//     be strictly greater than MaxBeatGap so a target can never become reapable
//     before at least one full beat gap could have confirmed it unreachable.
//
// Calibration invariant (load-bearing): MaxBeatGap must comfortably exceed the
// WORST healthy synchronization-cycle duration under rate-limit contention, not
// the nominal sync interval. An unreachable target's observed_at is refreshed
// roughly once per sync cycle, so the reaper's per-step gap is one sync cycle
// wide. If MaxBeatGap sat at or below that, every ordinary step would trip the
// clock-stop and accrual would be pinned at zero — the target could never reap.
// Concretely, the default sync interval is 5m (schema Config.pkl
// SynchronizationConfig.interval); a heavily throttled cycle can run many times
// longer. MaxBeatGap is therefore set to 2h — ~24x the nominal interval — which
// absorbs deeply rate-limited cycles while still stopping the clock for genuine
// multi-hour reaper/agent outages. MinReapDuration (4h) > MaxBeatGap (2h), and
// the global default reap-after (24h, pkgmodel.DefaultReapMaxUnreachableSeconds)
// comfortably exceeds both.
const (
	ReaperInterval  = 5 * time.Minute
	MaxBeatGap      = 2 * time.Hour
	MinReapDuration = 4 * time.Hour
)

// ValidateReapingConfig asserts the invariants the reaping constants must hold,
// given the agent's configured synchronization interval. It is wired into agent
// startup (NewMetastructureWithDataStoreAndContext) so a bad edit to the
// constants, or a sync interval raised past MaxBeatGap, fails the agent fast
// instead of silently disabling reaping (accrual pinned at zero).
func ValidateReapingConfig(syncInterval time.Duration) error {
	if MinReapDuration <= MaxBeatGap {
		return fmt.Errorf("reaping config: MinReapDuration (%s) must be greater than MaxBeatGap (%s)",
			MinReapDuration, MaxBeatGap)
	}
	if ReaperInterval <= 0 {
		return fmt.Errorf("reaping config: ReaperInterval (%s) must be positive", ReaperInterval)
	}
	// MaxBeatGap must comfortably exceed the sync interval: an unreachable
	// target's observed_at only refreshes about once per sync cycle, so the
	// reaper's per-step gap is one cycle wide. If MaxBeatGap did not exceed the
	// interval, every ordinary step would trip the clock-stop and accrual would
	// never advance, silently disabling reaping.
	if syncInterval > 0 && MaxBeatGap <= syncInterval {
		return fmt.Errorf("reaping config: MaxBeatGap (%s) must exceed the synchronization interval (%s), "+
			"otherwise every sync-cycle-sized beat gap trips the clock-stop and no target ever accrues toward reaping",
			MaxBeatGap, syncInterval)
	}
	return nil
}
