// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package reaping

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestDeriveReaperInterval_ScalesAndFloors pins that the reaper cadence tracks
// the sync interval (so a fast demo interval reaps responsively) but never
// ticks faster than the floor.
func TestDeriveReaperInterval_ScalesAndFloors(t *testing.T) {
	assert.Equal(t, ReaperIntervalFloor, DeriveReaperInterval(1*time.Second), "a tiny interval is floored")
	assert.Equal(t, ReaperIntervalFloor, DeriveReaperInterval(0), "zero interval is floored")
	assert.Equal(t, ReaperIntervalFloor, DeriveReaperInterval(ReaperIntervalFloor), "at the floor, returns the floor")
	assert.Equal(t, 2*time.Minute, DeriveReaperInterval(2*time.Minute), "above the floor, passes through")
}

// TestDeriveReaperInterval_DemoInterval pins the demo case: a 20s sync interval
// floors the reaper at ReaperIntervalFloor rather than pinning a fixed 5m.
func TestDeriveReaperInterval_DemoInterval(t *testing.T) {
	// 20s is below the 30s floor, so it floors; a 90s interval passes through.
	assert.Equal(t, ReaperIntervalFloor, DeriveReaperInterval(20*time.Second))
	assert.Equal(t, 90*time.Second, DeriveReaperInterval(90*time.Second))
	assert.Equal(t, 5*time.Minute, DeriveReaperInterval(5*time.Minute))
}

// TestDeriveMaxBeatGap_ScalesWithSyncInterval pins that MaxBeatGap scales with
// whatever interval the agent is actually configured with (never a fixed
// constant coupled to a nominal interval).
func TestDeriveMaxBeatGap_ScalesWithSyncInterval(t *testing.T) {
	interval := 3 * time.Hour
	got := DeriveMaxBeatGap(interval)
	assert.Equal(t, MaxBeatGapMultiplier*interval, got, "for large intervals the sync term dominates")
	assert.Greater(t, got, interval, "MaxBeatGap must exceed the sync interval it was derived from")
}

// TestDeriveMaxBeatGap_FloorAppliesForTinyIntervals verifies a very fast sync
// interval floors MaxBeatGap on the reaper-interval term rather than collapsing
// to something a single delayed heartbeat could trip.
func TestDeriveMaxBeatGap_FloorAppliesForTinyIntervals(t *testing.T) {
	got := DeriveMaxBeatGap(1 * time.Second)
	assert.Equal(t, 2*ReaperIntervalFloor, got, "a tiny sync interval floors on 2×ReaperInterval")
	assert.Greater(t, got, 1*time.Second)
}

// TestDeriveMaxBeatGap_ZeroInterval verifies a disabled/unset sync interval (0)
// still yields a sane, floored MaxBeatGap rather than 0.
func TestDeriveMaxBeatGap_ZeroInterval(t *testing.T) {
	assert.Equal(t, 2*ReaperIntervalFloor, DeriveMaxBeatGap(0))
}

// TestMaxBeatGapExceedsReaperInterval pins the load-bearing invariant that the
// clock-stop threshold is always strictly wider than the reaper's own sampling
// cadence, for ANY interval — otherwise every ordinary step sits on the
// clock-stop boundary and accrual stalls.
func TestMaxBeatGapExceedsReaperInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, 1 * time.Second, 10 * time.Second, 20 * time.Second, 50 * time.Second, 5 * time.Minute, 3 * time.Hour} {
		assert.Greater(t, DeriveMaxBeatGap(interval), DeriveReaperInterval(interval),
			"DeriveMaxBeatGap(%s) must exceed DeriveReaperInterval(%s)", interval, interval)
	}
}

// TestDerivations_NoNegativeOverflowAtHugeIntervals guards against int64
// overflow flipping a derived duration negative for an absurdly large configured
// sync interval — a negative MaxBeatGap would read as "every step trips the
// clock-stop" and silently disable reaping. Saturating keeps them positive.
func TestDerivations_NoNegativeOverflowAtHugeIntervals(t *testing.T) {
	for _, interval := range []time.Duration{math.MaxInt64 / 12, math.MaxInt64 / 2, math.MaxInt64} {
		mbg := DeriveMaxBeatGap(interval)
		assert.Positive(t, mbg, "MaxBeatGap must never overflow negative for interval %d", int64(interval))
		minReap := DeriveMinReapDuration(mbg)
		assert.Positive(t, minReap, "MinReapDuration must never overflow negative for interval %d", int64(interval))
		assert.GreaterOrEqual(t, minReap, mbg, "MinReapDuration must not fall below MaxBeatGap even when saturated")
	}
}

// TestDeriveMinReapDuration_ExceedsMaxBeatGap pins that the reap-duration floor
// is strictly greater than the MaxBeatGap it was derived from, for any sync
// interval — a target must never be reapable before at least one full beat gap
// could have confirmed it unreachable.
func TestDeriveMinReapDuration_ExceedsMaxBeatGap(t *testing.T) {
	for _, interval := range []time.Duration{0, 1 * time.Second, 20 * time.Second, 5 * time.Minute, 3 * time.Hour} {
		maxBeatGap := DeriveMaxBeatGap(interval)
		assert.Greater(t, DeriveMinReapDuration(maxBeatGap), maxBeatGap,
			"MinReapDuration must exceed MaxBeatGap for sync interval %s", interval)
	}
}

// TestMinReapDuration_DefaultExceedsMaxBeatGap pins the package-level admission
// default against its own derived MaxBeatGap.
func TestMinReapDuration_DefaultExceedsMaxBeatGap(t *testing.T) {
	assert.Greater(t, MinReapDuration, DeriveMaxBeatGap(NominalSyncInterval))
}

// TestGlobalDefaultReapAfterExceedsMinReapDuration pins that the global default
// reap-after duration (24h) comfortably exceeds the default admission floor, so
// an operator relying on defaults end-to-end gets a sane configuration.
func TestGlobalDefaultReapAfterExceedsMinReapDuration(t *testing.T) {
	const defaultReapMaxUnreachable = 24 * time.Hour // pkgmodel.DefaultReapMaxUnreachableSeconds
	assert.Greater(t, defaultReapMaxUnreachable, MinReapDuration)
}
