// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHealthTestTarget(label string) *pkgmodel.Target {
	return &pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	}
}

// RunUpdateTargetHealthReachable verifies that applying a reachable observation
// updates health_state, last_seen_at, and observed_at on the target's current row,
// and that the change is visible on reload.
func RunUpdateTargetHealthReachable(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTargetHealth_Reachable", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("health-reachable-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		now := time.Now().UTC().Truncate(time.Second)
		obs := pkgmodel.TargetHealthObservation{
			TargetLabel:   "health-reachable-test",
			State:         pkgmodel.TargetHealthStateReachable,
			ObservedAt:    now,
			LastSeenAt:    &now,
			LastErrorCode: "",
		}

		applied, err := ds.UpdateTargetHealth(obs)
		require.NoError(t, err)
		assert.True(t, applied, "observation must be applied to a fresh target")

		loaded, err := ds.LoadTarget("health-reachable-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)

		assert.Equal(t, pkgmodel.TargetHealthStateReachable, loaded.Health.State)
		require.NotNil(t, loaded.Health.LastSeenAt, "last_seen_at must be set after reachable observation")
		require.NotNil(t, loaded.Health.ObservedAt, "observed_at must be set after reachable observation")
		assert.WithinDuration(t, now, *loaded.Health.LastSeenAt, time.Second)
		assert.WithinDuration(t, now, *loaded.Health.ObservedAt, time.Second)
	})
}

// RunUpdateTargetHealthMonotonicGuard verifies that an observation with an
// observedAt older than the stored observed_at is rejected (applied=false)
// and the persisted state is unchanged.
func RunUpdateTargetHealthMonotonicGuard(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTargetHealth_MonotonicGuard", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("health-monotonic-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		// Apply a reachable observation at t=now.
		now := time.Now().UTC().Truncate(time.Second)
		obs := pkgmodel.TargetHealthObservation{
			TargetLabel: "health-monotonic-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  now,
			LastSeenAt:  &now,
		}
		applied, err := ds.UpdateTargetHealth(obs)
		require.NoError(t, err)
		require.True(t, applied)

		// Attempt to apply an older observation (now - 1 minute).
		older := now.Add(-time.Minute)
		staleObs := pkgmodel.TargetHealthObservation{
			TargetLabel:   "health-monotonic-test",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    older,
			LastErrorCode: "NetworkFailure",
		}
		applied, err = ds.UpdateTargetHealth(staleObs)
		require.NoError(t, err)
		assert.False(t, applied, "stale observation must be rejected by the monotonic guard")

		// State must still be reachable.
		loaded, err := ds.LoadTarget("health-monotonic-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReachable, loaded.Health.State)
	})
}

// RunUpdateTargetHealthSubSecondMonotonic verifies that a fractional-second
// observation (nanosecond precision) is accepted when the stored timestamp is a
// whole-second value, i.e. the guard compares temporally rather than
// lexicographically.  RFC3339Nano trims trailing zeros, so "…:05Z" sorts after
// "…:05.5Z" under lexicographic ordering ('Z' > '.'), which would cause the
// guard to falsely reject a chronologically-newer fractional observation.
func RunUpdateTargetHealthSubSecondMonotonic(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTargetHealth_SubSecondMonotonic", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("health-subsecond-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		// First observation: whole-second timestamp (no fractional part).
		whole := time.Date(2024, 3, 15, 10, 30, 5, 0, time.UTC)
		obs1 := pkgmodel.TargetHealthObservation{
			TargetLabel: "health-subsecond-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  whole,
			LastSeenAt:  &whole,
		}
		applied, err := ds.UpdateTargetHealth(obs1)
		require.NoError(t, err)
		require.True(t, applied, "initial observation must be applied")

		// Second observation: same second plus 500 ms — chronologically newer.
		fractional := time.Date(2024, 3, 15, 10, 30, 5, 500_000_000, time.UTC)
		obs2 := pkgmodel.TargetHealthObservation{
			TargetLabel: "health-subsecond-test",
			State:       pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:  fractional,
		}
		applied, err = ds.UpdateTargetHealth(obs2)
		require.NoError(t, err)
		assert.True(t, applied, "fractional-second observation newer than stored whole-second must be accepted")

		loaded, err := ds.LoadTarget("health-subsecond-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateUnreachable, loaded.Health.State,
			"state must reflect the newer fractional-second observation")
	})
}

// RunUpdateTargetHealthIncarnationMatch verifies that an observation carrying
// the target's current target_incarnation_id is applied normally.
func RunUpdateTargetHealthIncarnationMatch(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTargetHealth_IncarnationMatch", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("health-incarnation-match-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		loaded, err := ds.LoadTarget("health-incarnation-match-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)
		require.NotEmpty(t, loaded.Health.IncarnationID, "target must have an incarnation id after creation")

		now := time.Now().UTC().Truncate(time.Second)
		obs := pkgmodel.TargetHealthObservation{
			TargetLabel:   "health-incarnation-match-test",
			IncarnationID: loaded.Health.IncarnationID,
			State:         pkgmodel.TargetHealthStateReachable,
			ObservedAt:    now,
			LastSeenAt:    &now,
		}
		applied, err := ds.UpdateTargetHealth(obs)
		require.NoError(t, err)
		assert.True(t, applied, "observation matching the current incarnation id must be applied")

		reloaded, err := ds.LoadTarget("health-incarnation-match-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded)
		require.NotNil(t, reloaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReachable, reloaded.Health.State)
		require.NotNil(t, reloaded.Health.ObservedAt)
		assert.WithinDuration(t, now, *reloaded.Health.ObservedAt, time.Second)
	})
}

// RunUpdateTargetHealthIncarnationMismatch verifies that an observation
// carrying a stale/foreign target_incarnation_id is rejected (applied=false)
// and the persisted health state is left unchanged, even though the
// observation is otherwise newer than the stored observed_at.
func RunUpdateTargetHealthIncarnationMismatch(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTargetHealth_IncarnationMismatch", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("health-incarnation-mismatch-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		loaded, err := ds.LoadTarget("health-incarnation-mismatch-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)

		// Establish a known-good baseline state to detect any mutation.
		baseline := time.Now().UTC().Truncate(time.Second)
		setup := pkgmodel.TargetHealthObservation{
			TargetLabel: "health-incarnation-mismatch-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  baseline,
			LastSeenAt:  &baseline,
		}
		applied, err := ds.UpdateTargetHealth(setup)
		require.NoError(t, err)
		require.True(t, applied)

		// Submit a chronologically newer observation with a bogus incarnation id.
		newer := baseline.Add(time.Minute)
		staleIncarnation := pkgmodel.TargetHealthObservation{
			TargetLabel:   "health-incarnation-mismatch-test",
			IncarnationID: "bogus-incarnation-id-does-not-exist",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    newer,
			LastErrorCode: "NetworkFailure",
		}
		applied, err = ds.UpdateTargetHealth(staleIncarnation)
		require.NoError(t, err)
		assert.False(t, applied, "observation with a mismatched incarnation id must be rejected")

		reloaded, err := ds.LoadTarget("health-incarnation-mismatch-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded)
		require.NotNil(t, reloaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReachable, reloaded.Health.State,
			"health state must be unchanged when the incarnation guard rejects the observation")
		require.NotNil(t, reloaded.Health.ObservedAt)
		assert.WithinDuration(t, baseline, *reloaded.Health.ObservedAt, time.Second,
			"observed_at must be unchanged when the incarnation guard rejects the observation")
		assert.Equal(t, loaded.Health.IncarnationID, reloaded.Health.IncarnationID,
			"incarnation id itself must be unaffected by the rejected observation")
	})
}

// RunUpdateTargetHealthReapedGuard verifies that applying any observation to a
// target whose health_state is 'reaped' is a no-op (applied=false).
func RunUpdateTargetHealthReapedGuard(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTargetHealth_ReapedGuard", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetTargetHealthStateForTest == nil {
			t.Skip("backend does not expose SetTargetHealthStateForTest")
		}

		target := newHealthTestTarget("health-reaped-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		require.NoError(t, td.SetTargetHealthStateForTest("health-reaped-test", pkgmodel.TargetHealthStateReaped))

		obs := pkgmodel.TargetHealthObservation{
			TargetLabel: "health-reaped-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  time.Now().UTC(),
			LastSeenAt:  func() *time.Time { t := time.Now().UTC(); return &t }(),
		}
		applied, err := ds.UpdateTargetHealth(obs)
		require.NoError(t, err)
		assert.False(t, applied, "observation against a reaped target must be rejected")

		loaded, err := ds.LoadTarget("health-reaped-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReaped, loaded.Health.State)
	})
}
