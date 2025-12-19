// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import "time"

// TimeNow returns the current time in UTC.
// All timestamps in the system should be stored in UTC for consistent ordering
// and comparison, especially important for TEXT-based timestamp storage in SQLite.
func TimeNow() time.Time {
	return time.Now().UTC()
}
