// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReaperIntervalPositive(t *testing.T) {
	assert.Positive(t, ReaperInterval, "ReaperInterval must be positive")
}

// TestDeriveMaxBeatGap_ScalesWithSyncInterval pins the core behaviour of this
// refactor: MaxBeatGap is no longer a fixed constant coupled to a nominal sync
// interval, it scales with whatever interval the agent is actually
// configured with, so raising the sync interval can never make MaxBeatGap sit
// at or below it (the bug that made reaping inert).
func TestDeriveMaxBeatGap_ScalesWithSyncInterval(t *testing.T) {
	interval := 3 * time.Hour
	got := DeriveMaxBeatGap(interval)

	assert.Equal(t, MaxBeatGapMultiplier*interval, got)
	assert.Greater(t, got, interval, "MaxBeatGap must exceed the sync interval it was derived from")
}

// TestDeriveMaxBeatGap_DefaultInterval pins the default-interval case (5m) so
// a change to the multiplier or floor is visible.
func TestDeriveMaxBeatGap_DefaultInterval(t *testing.T) {
	got := DeriveMaxBeatGap(DefaultSyncInterval)
	assert.Equal(t, MaxBeatGapMultiplier*DefaultSyncInterval, got)
	assert.Greater(t, got, DefaultSyncInterval)
}

// TestDeriveMaxBeatGap_FloorAppliesForTinyIntervals verifies the floor: a very
// fast sync interval must not collapse MaxBeatGap to something a single
// delayed heartbeat could trip.
func TestDeriveMaxBeatGap_FloorAppliesForTinyIntervals(t *testing.T) {
	got := DeriveMaxBeatGap(1 * time.Second)
	assert.Equal(t, MaxBeatGapFloor, got, "a tiny sync interval must be floored")
	assert.Greater(t, got, 1*time.Second)
}

// TestDeriveMaxBeatGap_ZeroInterval verifies a disabled/unset sync interval
// (0) still yields a sane, floored MaxBeatGap rather than 0.
func TestDeriveMaxBeatGap_ZeroInterval(t *testing.T) {
	assert.Equal(t, MaxBeatGapFloor, DeriveMaxBeatGap(0))
}

// TestDeriveMinReapDuration_ExceedsMaxBeatGap pins the load-bearing invariant
// that the reap-duration floor is strictly greater than the maximum
// tolerated beat gap it was derived from: a target must never be reapable
// before at least one full beat gap could have confirmed it unreachable.
// This must hold for any sync interval, not just the default, since both
// values are now derived from the same input.
func TestDeriveMinReapDuration_ExceedsMaxBeatGap(t *testing.T) {
	for _, interval := range []time.Duration{0, 1 * time.Second, 5 * time.Minute, 3 * time.Hour} {
		maxBeatGap := DeriveMaxBeatGap(interval)
		minReapDuration := DeriveMinReapDuration(maxBeatGap)
		assert.Greater(t, minReapDuration, maxBeatGap,
			"MinReapDuration must exceed MaxBeatGap for sync interval %s", interval)
	}
}

// TestMinReapDuration_DefaultExceedsMaxBeatGap pins the package-level default
// (used by target_update_generator.go's admission floor) against the
// default-interval-derived MaxBeatGap.
func TestMinReapDuration_DefaultExceedsMaxBeatGap(t *testing.T) {
	assert.Greater(t, MinReapDuration, DeriveMaxBeatGap(DefaultSyncInterval))
}

// TestGlobalDefaultReapAfterExceedsMinReapDuration pins that the global
// default reap-after duration (pkgmodel.DefaultReapMaxUnreachableSeconds,
// unchanged by this refactor) comfortably exceeds MinReapDuration under the
// default sync interval, so an operator relying on defaults end-to-end gets a
// sane, non-degenerate configuration.
func TestGlobalDefaultReapAfterExceedsMinReapDuration(t *testing.T) {
	const defaultReapMaxUnreachableSeconds = 24 * time.Hour // pkgmodel.DefaultReapMaxUnreachableSeconds
	assert.Greater(t, defaultReapMaxUnreachableSeconds, MinReapDuration)
}
