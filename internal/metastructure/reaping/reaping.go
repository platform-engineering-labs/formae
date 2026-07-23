// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package reaping holds the target-reaping timing derivations. It is a leaf
// package (stdlib only) so both the reaper actor (which drives the lifecycle)
// and command admission (which floors a user's declared reap-after) can import
// it without a cycle.
//
// A target is reaped once it has been continuously unreachable for its
// configured reap-after duration. Three derived durations govern that:
//
//   - ReaperInterval — how often the reaper evaluates targets. Derived from the
//     agent's sync interval (DeriveReaperInterval) so a fast interval (e.g. a
//     ~20s demo) gets proportionally responsive reaping instead of being pinned
//     to a fixed cadence, floored so we never tick faster than the reaper can
//     do real datastore work.
//   - MaxBeatGap — the largest tolerated gap between successive health beats
//     before a step is treated as unobserved (the accrual clock-stop). It is
//     the anti-fabrication guard: when the observed_at → last_sample_at gap
//     exceeds it, accrual for that step is dropped, because we cannot trust the
//     target was continuously unreachable across such a long unobserved window
//     (the reaper or agent may itself have been down). Derived from the sync
//     interval AND the reaper interval so it always strictly exceeds both — an
//     unreachable target's observed_at is refreshed ~once per sync cycle and
//     sampled ~once per reaper tick, so a fixed value would be fragile: raise
//     the interval past it and every ordinary step trips the clock-stop,
//     pinning accrual at zero and silently disabling reaping.
//   - MinReapDuration — the admission floor for any target's reap-after
//     duration. Strictly greater than MaxBeatGap so a target can never reap off
//     a single (near-maximal) accrual step; it must survive at least two full
//     beat-gap windows of confirmed unreachability.
//
// All three derive from the one user-facing knob (Synchronization.Interval),
// threaded live into both the reaper (see TargetReaper.Init) and admission (see
// FormaCommandFromForma → target_update_generator). None of them is exposed as
// user pkl config.
package reaping

import (
	"math"
	"time"
)

const (
	// ReaperIntervalFloor bounds how fast the reaper ticks regardless of how
	// small the sync interval is configured — the reaper does real datastore
	// work each tick.
	ReaperIntervalFloor = 30 * time.Second

	// MaxBeatGapMultiplier (k=6) gives MaxBeatGap ample headroom above one
	// nominal sync cycle to absorb jitter and rate-limited cycles, while still
	// bounding a genuine multi-cycle outage.
	MaxBeatGapMultiplier = 6

	// MinReapDurationMultiplier (2) holds MinReapDuration > MaxBeatGap
	// structurally for any positive MaxBeatGap.
	MinReapDurationMultiplier = 2

	// NominalSyncInterval is a DEFENSIVE fallback only: it backs the
	// package-level MinReapDuration default used when a live sync interval is
	// unavailable (a unit test constructing the generator without an explicit
	// floor). Production always threads the live Synchronization.Interval, so
	// this does NOT need to track the schema's sync-interval default.
	NominalSyncInterval = 5 * time.Minute
)

// DeriveReaperInterval computes how often the reaper evaluates targets from the
// agent's configured sync interval, floored at ReaperIntervalFloor.
func DeriveReaperInterval(syncInterval time.Duration) time.Duration {
	if syncInterval < ReaperIntervalFloor {
		return ReaperIntervalFloor
	}
	return syncInterval
}

// DeriveMaxBeatGap computes the accrual clock-stop threshold from the sync
// interval. It is the larger of k×syncInterval and 2×ReaperInterval, so it
// always strictly exceeds both the sync cycle and the reaper's own sampling
// cadence (a per-step gap is ~one reaper tick wide) for any interval.
func DeriveMaxBeatGap(syncInterval time.Duration) time.Duration {
	bySync := mulSaturating(MaxBeatGapMultiplier, syncInterval)
	byReaper := mulSaturating(2, DeriveReaperInterval(syncInterval))
	if bySync < byReaper {
		return byReaper
	}
	return bySync
}

// DeriveMinReapDuration computes the reap-after admission floor from a MaxBeatGap
// value, holding MinReapDuration > MaxBeatGap structurally for any positive
// maxBeatGap.
func DeriveMinReapDuration(maxBeatGap time.Duration) time.Duration {
	return mulSaturating(MinReapDurationMultiplier, maxBeatGap)
}

// mulSaturating returns factor×d, clamped to the maximum representable Duration
// instead of overflowing int64 into a negative value. Reaping timing derivations
// multiply the sync interval by small factors; without this guard an absurdly
// large configured interval (multiple decades) would overflow to a NEGATIVE
// MaxBeatGap — which reads as "every step trips the clock-stop", silently
// disabling reaping — or a negative admission floor. Saturating keeps the
// derivations monotonic and non-negative for every possible interval.
func mulSaturating(factor int64, d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if int64(d) > math.MaxInt64/factor {
		return math.MaxInt64
	}
	return time.Duration(factor) * d
}

// MinReapDuration is the defensive admission-floor default (used by
// target_update_generator.go when no live floor is injected). Production
// overrides it via WithMinReapDuration with a value derived from the live sync
// interval — see FormaCommandFromForma.
var MinReapDuration = DeriveMinReapDuration(DeriveMaxBeatGap(NominalSyncInterval))
