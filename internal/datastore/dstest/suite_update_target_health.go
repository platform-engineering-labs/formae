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
