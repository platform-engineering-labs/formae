// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultSyncInterval mirrors the schema default (Config.pkl
// SynchronizationConfig.interval = 5.min). The reaping calibration is only
// valid relative to this.
const defaultSyncInterval = 5 * time.Minute

// TestMinReapDurationExceedsMaxBeatGap pins the load-bearing invariant that the
// reap-duration floor is strictly greater than the maximum tolerated beat gap:
// a target must never be reapable before at least one full beat gap could have
// confirmed it unreachable.
func TestMinReapDurationExceedsMaxBeatGap(t *testing.T) {
	assert.Greater(t, MinReapDuration, MaxBeatGap,
		"MinReapDuration must be strictly greater than MaxBeatGap")
}

func TestReaperIntervalPositive(t *testing.T) {
	assert.Positive(t, ReaperInterval, "ReaperInterval must be positive")
}

// TestMaxBeatGapExceedsSyncInterval pins the calibration that regressed and made
// reaping inert: MaxBeatGap must comfortably exceed the sync interval, because
// an unreachable target's observed_at only refreshes about once per sync cycle,
// so the reaper's per-step gap is one cycle wide. When MaxBeatGap sat below the
// interval, every step tripped the clock-stop and accrual never advanced.
func TestMaxBeatGapExceedsSyncInterval(t *testing.T) {
	assert.Greater(t, MaxBeatGap, defaultSyncInterval,
		"MaxBeatGap must exceed the default sync interval, or accrual is pinned at zero")
}

func TestValidateReapingConfig(t *testing.T) {
	require.NoError(t, ValidateReapingConfig(defaultSyncInterval),
		"the built-in reaping constants must satisfy their invariants under the default sync interval")
}

// TestValidateReapingConfig_RejectsSyncIntervalAtOrAboveMaxBeatGap is the guard
// for the exact class of misconfiguration that made reaping inert: a sync
// interval that meets or exceeds MaxBeatGap means every sync-cycle-sized beat
// gap trips the clock-stop and no target ever accrues toward reaping.
func TestValidateReapingConfig_RejectsSyncIntervalAtOrAboveMaxBeatGap(t *testing.T) {
	assert.Error(t, ValidateReapingConfig(MaxBeatGap),
		"a sync interval equal to MaxBeatGap must be rejected")
	assert.Error(t, ValidateReapingConfig(MaxBeatGap+time.Minute),
		"a sync interval greater than MaxBeatGap must be rejected")
}

// TestValidateReapingConfig_ZeroSyncIntervalSkipsCheck confirms a disabled/unset
// synchronization interval (0) does not trip the sync-interval check — there are
// no cycles to fabricate accrual against.
func TestValidateReapingConfig_ZeroSyncIntervalSkipsCheck(t *testing.T) {
	require.NoError(t, ValidateReapingConfig(0),
		"a zero sync interval must not fail validation")
}
