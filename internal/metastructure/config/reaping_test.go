// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestValidateReapingConfig(t *testing.T) {
	require.NoError(t, ValidateReapingConfig(),
		"the built-in reaping constants must satisfy their invariants")
}
