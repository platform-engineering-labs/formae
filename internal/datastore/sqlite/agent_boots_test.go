// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SQLite has no timestamp type, so booted_at is stored as text and ordered as
// text. The reader picks the newest boot with ORDER BY booted_at, so the stored
// format has to sort lexicographically in the same order it sorts
// chronologically.
//
// time.RFC3339Nano does not: it drops trailing zeros from the fractional part,
// so an earlier ".5Z" compares greater than a later ".500000001Z" ('Z' > '0')
// and the reader would report a stale version. Two boots inside one second is
// what a crash-looping agent does, so this is reachable rather than theoretical.
func TestAgentBootTimestampSortsChronologically(t *testing.T) {
	base := time.Date(2026, 8, 15, 10, 30, 45, 0, time.UTC)

	ordered := []time.Time{
		base,
		base.Add(500 * time.Millisecond),   // .5 -> trailing zeros
		base.Add(500*time.Millisecond + 1), // .500000001
		base.Add(900 * time.Millisecond),   // .9 -> trailing zeros
		base.Add(time.Second),
	}

	for i := 1; i < len(ordered); i++ {
		prev := agentBootTimestamp(ordered[i-1])
		cur := agentBootTimestamp(ordered[i])

		require.True(t, ordered[i-1].Before(ordered[i]), "fixture must be chronological")
		assert.Less(t, prev, cur,
			"text order must match time order: %q must sort before %q", prev, cur)
	}
}

func TestAgentBootTimestampRoundTrips(t *testing.T) {
	want := time.Date(2026, 8, 15, 10, 30, 45, 500000001, time.UTC)

	got, err := time.Parse(time.RFC3339Nano, agentBootTimestamp(want))
	require.NoError(t, err)
	assert.True(t, want.Equal(got), "stored form must parse back to the same instant")
}
