// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
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
// MaxBeatGap is DERIVED (DeriveMaxBeatGap) from the agent's actual configured
// synchronization interval instead of pinned to an independent fixed constant.
// This is deliberate: an unreachable target's observed_at is refreshed roughly
// once per sync cycle, so the reaper's per-step gap is one sync cycle wide. A
// fixed MaxBeatGap is fragile — raise the sync interval past it (an otherwise
// unrelated config change) and every ordinary step trips the clock-stop,
// pinning accrual at zero and silently disabling reaping. Deriving it as a
// multiple of the live interval means it self-adjusts and never needs
// separate operator tuning. It stays internal — not exposed as user pkl
// config; only Synchronization.Interval is user-facing.
//
//   - MaxBeatGapMultiplier (k=6): MaxBeatGap = k × syncInterval. That gives
//     ample headroom above one nominal sync cycle to absorb ordinary jitter and
//     moderately rate-limited cycles, while still bounding a genuine multi-cycle
//     reaper/agent outage.
//   - MaxBeatGapFloor: the minimum MaxBeatGap regardless of how small
//     syncInterval is configured, so a fast sync interval doesn't collapse the
//     tolerance to something a single delayed heartbeat could trip. 5 minutes
//     is chosen to match the schema's own default sync interval — comfortably
//     larger than ordinary network/scheduling jitter for any realistically fast
//     interval.
//
// MinReapDuration must stay strictly greater than MaxBeatGap so a target can
// never reap off a single (potentially near-maximal) accrual step; it must
// have survived at least two full beat-gap windows of confirmed
// unreachability. DeriveMinReapDuration (2 × MaxBeatGap) holds that
// relationship structurally for any positive MaxBeatGap, for any configured
// sync interval — it is no longer two independently-tunable constants that
// can drift out of the required relationship.
//
// The package-level MinReapDuration below is derived from DefaultSyncInterval
// (the schema default) rather than threaded from the live agent config: it
// backs target_update_generator.go's admission-time floor on a user's
// explicitly declared per-target reap-after duration, which is a global
// sanity minimum on user input rather than a live per-agent runtime guard —
// unlike the reaper's own MaxBeatGap, which is instantiated from the actual
// configured Synchronization.Interval (see TargetReaper.Init).
const (
	ReaperInterval = 5 * time.Minute

	// DefaultSyncInterval mirrors the schema default (Config.pkl
	// SynchronizationConfig.interval = 5.min).
	DefaultSyncInterval = 5 * time.Minute

	MaxBeatGapMultiplier = 6
	MaxBeatGapFloor      = 5 * time.Minute

	MinReapDurationMultiplier = 2
)

// DeriveMaxBeatGap computes the largest tolerated gap between successive
// health beats (the accrualStep clock-stop guard) from the agent's actual
// configured synchronization interval. See the package doc for the
// multiplier and floor rationale.
func DeriveMaxBeatGap(syncInterval time.Duration) time.Duration {
	derived := MaxBeatGapMultiplier * syncInterval
	if derived < MaxBeatGapFloor {
		return MaxBeatGapFloor
	}
	return derived
}

// DeriveMinReapDuration computes the floor for any target's reap-after
// duration from a MaxBeatGap value, holding MinReapDuration > MaxBeatGap
// structurally for any positive maxBeatGap (see package doc).
func DeriveMinReapDuration(maxBeatGap time.Duration) time.Duration {
	return MinReapDurationMultiplier * maxBeatGap
}

// MinReapDuration is the admission-time reap-after floor
// (target_update_generator.go), derived from the nominal/default
// synchronization interval rather than the live agent config — see the
// package doc for why.
var MinReapDuration = DeriveMinReapDuration(DeriveMaxBeatGap(DefaultSyncInterval))
