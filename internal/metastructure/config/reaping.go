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
//     before a target is treated as unobserved.
//   - MinReapDuration is the floor for any target's reap-after duration. It must
//     be strictly greater than MaxBeatGap so a target can never become reapable
//     before at least one full beat gap could have confirmed it unreachable.
const (
	ReaperInterval  = 1 * time.Minute
	MaxBeatGap      = 3 * time.Minute
	MinReapDuration = 5 * time.Minute
)

// ValidateReapingConfig asserts the invariants the reaping constants must hold.
// It is called at agent startup so a bad edit to the constants fails fast.
func ValidateReapingConfig() error {
	if MinReapDuration <= MaxBeatGap {
		return fmt.Errorf("reaping config: MinReapDuration (%s) must be greater than MaxBeatGap (%s)",
			MinReapDuration, MaxBeatGap)
	}
	if ReaperInterval <= 0 {
		return fmt.Errorf("reaping config: ReaperInterval (%s) must be positive", ReaperInterval)
	}
	return nil
}
