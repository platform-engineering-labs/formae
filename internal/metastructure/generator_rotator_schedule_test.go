// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
)

func rotationInfo(generatorID string, intervalSeconds int, lastRotationAt time.Time) datastore.GeneratorRotationInfo {
	return datastore.GeneratorRotationInfo{
		GeneratorID:     generatorID,
		Label:           "db-password",
		StackLabel:      "secrets",
		IntervalSeconds: intervalSeconds,
		LastRotationAt:  lastRotationAt,
	}
}

// A generator with no committed rotation on record is due now. Attaching
// rotation to a credential nobody has rotated is a request to rotate it, not a
// request to wait one interval first.
func TestNextRotationDue_NeverRotatedIsDueImmediately(t *testing.T) {
	due := nextRotationDue(rotationInfo("gen-a", 3600, time.Time{}))
	assert.True(t, due.IsZero(), "a generator that has never rotated must be due now, got %s", due)

	now := time.Now().UTC()
	assert.True(t, rotationIsDue(rotationInfo("gen-a", 3600, time.Time{}), now))
}

// The cadence is a fixed delay measured from the last committed rotation.
func TestNextRotationDue_FixedDelayFromTheLastCommittedRotation(t *testing.T) {
	last := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	info := rotationInfo("gen-a", 3600, last)

	due := nextRotationDue(info)
	assert.False(t, due.Before(last.Add(time.Hour)),
		"the next rotation must be at least one interval after the last one")

	assert.False(t, rotationIsDue(info, last.Add(30*time.Minute)),
		"a generator inside its interval is not due")
	assert.True(t, rotationIsDue(info, last.Add(2*time.Hour)),
		"a generator past its interval and its jitter is due")
}

// Jitter is what stops a fleet rotating in lockstep. Every generator in a
// fleet created by one apply shares a last-rotation instant to the second, so
// without an offset derived from the generator itself they would all come due
// together on every cadence, forever.
//
// The offset is derived from the generator's identity rather than drawn at
// random, so a restart does not reshuffle the fleet's schedule and this
// assertion cannot fail by chance.
func TestRotationJitter_SpreadsAFleetAcrossTheCadence(t *testing.T) {
	const fleet = 500
	interval := time.Hour

	offsets := make(map[time.Duration]bool, fleet)
	bound := rotationJitterBound(interval)
	require.Positive(t, bound)

	for i := 0; i < fleet; i++ {
		offset := rotationJitter(fmt.Sprintf("2generator%019d", i), interval)
		assert.GreaterOrEqual(t, offset, time.Duration(0))
		assert.Less(t, offset, bound, "jitter must stay inside its bound")
		offsets[offset] = true
	}

	assert.Greater(t, len(offsets), fleet/2,
		"a fleet of %d generators must land on more than %d distinct offsets, got %d",
		fleet, fleet/2, len(offsets))
}

// The same generator always gets the same offset: the schedule is stable
// across sweeps and across restarts.
func TestRotationJitter_StableForOneGenerator(t *testing.T) {
	first := rotationJitter("2generatoraaaaaaaaaaaaaaaaaa", time.Hour)
	second := rotationJitter("2generatoraaaaaaaaaaaaaaaaaa", time.Hour)
	assert.Equal(t, first, second)
}

// The jitter bound is a fraction of the cadence, capped so an annual rotation
// does not drift by weeks.
func TestRotationJitterBound_FractionOfTheCadenceUpToACap(t *testing.T) {
	assert.Equal(t, 6*time.Minute, rotationJitterBound(time.Hour))
	assert.Equal(t, maxRotationJitter, rotationJitterBound(365*24*time.Hour))
	assert.Equal(t, time.Duration(0), rotationJitterBound(0))
}

// A missed window collapses to one run. A generator whose cadence elapsed ten
// times over is due once, not ten times: the next successful rotation moves
// the anchor to now, and nothing counts up the intervals that were missed.
func TestRotationIsDue_MissedIntervalsCollapseToOne(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	info := rotationInfo("gen-a", 3600, last)
	now := last.Add(10 * time.Hour)

	require.True(t, rotationIsDue(info, now))

	// Having rotated once, the generator is not due again until a further
	// interval has passed — the nine missed windows are gone, not queued.
	rotated := rotationInfo("gen-a", 3600, now)
	assert.False(t, rotationIsDue(rotated, now.Add(30*time.Minute)))
	assert.True(t, rotationIsDue(rotated, now.Add(2*time.Hour)))
}

// Backoff grows with the attempt count and saturates at one cadence, so a
// generator whose rotation keeps failing is retried at its own interval rather
// than either hammering the provider or giving up on the credential for good.
func TestRotationBackoff_GrowsThenSaturatesAtOneCadence(t *testing.T) {
	interval := time.Hour

	assert.Equal(t, time.Duration(0), rotationBackoff(0, interval))
	first := rotationBackoff(1, interval)
	second := rotationBackoff(2, interval)
	assert.Equal(t, rotationRetryBaseDelay, first)
	assert.Greater(t, second, first)

	for attempts := 1; attempts <= maxRotationRetryAttempts+3; attempts++ {
		assert.LessOrEqual(t, rotationBackoff(attempts, interval), interval,
			"backoff must never exceed one cadence")
	}
	assert.Equal(t, interval, rotationBackoff(maxRotationRetryAttempts, interval))
	assert.Equal(t, interval, rotationBackoff(maxRotationRetryAttempts+5, interval))
}

// A cadence shorter than the base retry delay must not be retried more slowly
// than it rotates.
func TestRotationBackoff_NeverExceedsAShortCadence(t *testing.T) {
	assert.Equal(t, time.Second, rotationBackoff(1, time.Second))
	assert.Equal(t, time.Second, rotationBackoff(4, time.Second))
}

// A tick that lands while a rotation is already in flight for a generator
// skips it. A rotation runs for as long as the provider writes take, which is
// many sweeps, and a second command for the same generator would draw a second
// value and leave one credential's destinations split across two generations.
func TestRotationsDueNow_SkipsAGeneratorWithAnAttemptInFlight(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := last.Add(10 * time.Hour)
	infos := []datastore.GeneratorRotationInfo{rotationInfo("gen-a", 3600, last)}

	require.Len(t, rotationsDueNow(infos, nil, nil, now), 1,
		"precondition: the generator is due")

	inFlight := map[string]rotationAttempt{"gen-a": {commandID: "command-1", interval: time.Hour}}
	assert.Empty(t, rotationsDueNow(infos, inFlight, nil, now),
		"a generator with an attempt in flight must be skipped")

	// Repeated ticks keep skipping it for as long as the attempt is running.
	assert.Empty(t, rotationsDueNow(infos, inFlight, nil, now.Add(time.Hour)))
	assert.Empty(t, rotationsDueNow(infos, inFlight, nil, now.Add(5*time.Hour)))

	// Once the attempt clears, the generator is due again.
	assert.Len(t, rotationsDueNow(infos, map[string]rotationAttempt{}, nil, now.Add(5*time.Hour)), 1)
}

// An in-flight attempt for one generator says nothing about another's.
func TestRotationsDueNow_InFlightGuardIsPerGenerator(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := last.Add(10 * time.Hour)
	infos := []datastore.GeneratorRotationInfo{
		rotationInfo("gen-a", 3600, last),
		rotationInfo("gen-b", 3600, last),
	}

	due := rotationsDueNow(infos, map[string]rotationAttempt{"gen-a": {commandID: "command-1", interval: time.Hour}}, nil, now)
	require.Len(t, due, 1)
	assert.Equal(t, "gen-b", due[0].GeneratorID)
}

// A generator inside its retry backoff is skipped, and comes back once the
// delay has elapsed.
func TestRotationsDueNow_SkipsAGeneratorInsideItsBackoff(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := last.Add(10 * time.Hour)
	infos := []datastore.GeneratorRotationInfo{rotationInfo("gen-a", 3600, last)}
	nextAttemptAt := map[string]time.Time{"gen-a": now.Add(5 * time.Minute)}

	assert.Empty(t, rotationsDueNow(infos, nil, nextAttemptAt, now))
	assert.Len(t, rotationsDueNow(infos, nil, nextAttemptAt, now.Add(10*time.Minute)), 1)
}

// A sweep over a cadence that elapsed many times produces exactly one
// rotation for that generator, not one per missed window.
func TestRotationsDueNow_MissedWindowsProduceOneRotation(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	infos := []datastore.GeneratorRotationInfo{rotationInfo("gen-a", 3600, last)}

	due := rotationsDueNow(infos, nil, nil, last.Add(240*time.Hour))
	assert.Len(t, due, 1, "240 missed hourly windows must collapse to one rotation")
}

// An agent that restarts reads the cadence back out of the datastore, so a
// generator that has just rotated is not rotated again. The rotator's own
// memory holds nothing that would have told it so: a fresh instance has an
// empty in-flight map and an empty backoff table, and the only thing standing
// between a restart and a second rotation is the derived last-rotation
// instant.
func TestRotationsDueNow_ARestartDoesNotRerotate(t *testing.T) {
	rotatedAt := time.Now().UTC().Add(-time.Minute)
	infos := []datastore.GeneratorRotationInfo{rotationInfo("gen-a", 3600, rotatedAt)}

	// A fresh rotator: no in-flight attempts, no backoff, nothing remembered.
	assert.Empty(t, rotationsDueNow(infos, map[string]rotationAttempt{}, map[string]time.Time{}, time.Now().UTC()),
		"a generator that rotated a minute ago must not rotate again after a restart")
}
