// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunAdvanceTargetAccrual exercises datastore.AdvanceTargetAccrual: an
// incarnation-guarded, max-version-pinned, in-place update to a target's
// unreachable_accum_seconds and last_sample_at columns.
func RunAdvanceTargetAccrual(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AdvanceTargetAccrual_Applies", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("accrual-applies-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		observedAt := time.Now().UTC().Truncate(time.Second)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   "accrual-applies-test",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    observedAt,
			LastErrorCode: "NetworkFailure",
		})
		require.NoError(t, err)

		loaded, err := ds.LoadTarget("accrual-applies-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health)
		incarnationID := loaded.Health.IncarnationID
		require.NotEmpty(t, incarnationID)

		newLastSampleAt := observedAt
		applied, err := ds.AdvanceTargetAccrual("accrual-applies-test", incarnationID, newLastSampleAt, 42)
		require.NoError(t, err)
		assert.True(t, applied, "a matching incarnation must apply the accrual advance")

		reloaded, err := ds.LoadTarget("accrual-applies-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded)
		require.NotNil(t, reloaded.Health)
		assert.Equal(t, int64(42), reloaded.Health.UnreachableAccumSeconds,
			"unreachable_accum_seconds must be advanced by the delta")
		require.NotNil(t, reloaded.Health.LastSampleAt)
		assert.WithinDuration(t, newLastSampleAt, *reloaded.Health.LastSampleAt, time.Second)

		// A second advance accumulates rather than overwrites.
		secondLastSampleAt := newLastSampleAt.Add(30 * time.Second)
		applied, err = ds.AdvanceTargetAccrual("accrual-applies-test", incarnationID, secondLastSampleAt, 8)
		require.NoError(t, err)
		assert.True(t, applied)

		final, err := ds.LoadTarget("accrual-applies-test")
		require.NoError(t, err)
		require.NotNil(t, final.Health)
		assert.Equal(t, int64(50), final.Health.UnreachableAccumSeconds,
			"successive advances must accumulate")
		require.NotNil(t, final.Health.LastSampleAt)
		assert.WithinDuration(t, secondLastSampleAt, *final.Health.LastSampleAt, time.Second)
	})

	t.Run("AdvanceTargetAccrual_IncarnationMismatch", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("accrual-mismatch-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		observedAt := time.Now().UTC().Truncate(time.Second)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   "accrual-mismatch-test",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    observedAt,
			LastErrorCode: "NetworkFailure",
		})
		require.NoError(t, err)

		applied, err := ds.AdvanceTargetAccrual("accrual-mismatch-test", "bogus-incarnation-id-does-not-exist", observedAt, 99)
		require.NoError(t, err)
		assert.False(t, applied, "a mismatched incarnation id must be rejected")

		reloaded, err := ds.LoadTarget("accrual-mismatch-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded)
		require.NotNil(t, reloaded.Health)
		assert.Equal(t, int64(0), reloaded.Health.UnreachableAccumSeconds,
			"a rejected advance must not mutate unreachable_accum_seconds")
		assert.Nil(t, reloaded.Health.LastSampleAt,
			"a rejected advance must not mutate last_sample_at")
	})

	// AdvanceTargetAccrual_PinnedToCurrentVersion verifies the max-version pin:
	// once a target has been re-declared (a new version row, health carried
	// forward since the prior row was not reaped), an advance carrying the
	// still-valid incarnation id lands on the new current row rather than being
	// silently lost against a stale version.
	t.Run("AdvanceTargetAccrual_PinnedToCurrentVersion", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("accrual-version-pin-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		observedAt := time.Now().UTC().Truncate(time.Second)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   "accrual-version-pin-test",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    observedAt,
			LastErrorCode: "NetworkFailure",
		})
		require.NoError(t, err)

		v1, err := ds.LoadTarget("accrual-version-pin-test")
		require.NoError(t, err)
		require.NotNil(t, v1.Health)
		incarnationID := v1.Health.IncarnationID
		require.NotEmpty(t, incarnationID)

		// Re-declare the target: bumps the version, carries health forward
		// (unreachable, same incarnation) since the current row is not reaped.
		_, err = ds.UpdateTarget(target)
		require.NoError(t, err)

		v2, err := ds.LoadTarget("accrual-version-pin-test")
		require.NoError(t, err)
		require.NotNil(t, v2.Health)
		require.Equal(t, incarnationID, v2.Health.IncarnationID,
			"incarnation must be carried forward across a non-reaped re-declare")
		require.Equal(t, pkgmodel.TargetHealthStateUnreachable, v2.Health.State)

		applied, err := ds.AdvanceTargetAccrual("accrual-version-pin-test", incarnationID, observedAt, 15)
		require.NoError(t, err)
		assert.True(t, applied, "advance must apply to the current (post-bump) max-version row")

		reloaded, err := ds.LoadTarget("accrual-version-pin-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded.Health)
		assert.Equal(t, int64(15), reloaded.Health.UnreachableAccumSeconds,
			"advance must have landed on the current max-version row")
	})

	// AdvanceTargetAccrual_ReachableGuard verifies that an advance computed
	// against a stale 'unreachable' snapshot is rejected (rowcount 0) if the
	// target has since raced back to 'reachable' — the write must never
	// resurrect accrual on a target that recovered before the write landed.
	t.Run("AdvanceTargetAccrual_ReachableGuard", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := newHealthTestTarget("accrual-reachable-guard-test")
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		observedAt := time.Now().UTC().Truncate(time.Second)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   "accrual-reachable-guard-test",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    observedAt,
			LastErrorCode: "NetworkFailure",
		})
		require.NoError(t, err)

		loaded, err := ds.LoadTarget("accrual-reachable-guard-test")
		require.NoError(t, err)
		require.NotNil(t, loaded.Health)
		incarnationID := loaded.Health.IncarnationID

		// Target recovers before the accrual write lands.
		recoveredAt := observedAt.Add(time.Second)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel: "accrual-reachable-guard-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  recoveredAt,
			LastSeenAt:  &recoveredAt,
		})
		require.NoError(t, err)

		applied, err := ds.AdvanceTargetAccrual("accrual-reachable-guard-test", incarnationID, observedAt, 60)
		require.NoError(t, err)
		assert.False(t, applied, "an advance must not apply once the target is no longer unreachable")

		reloaded, err := ds.LoadTarget("accrual-reachable-guard-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReachable, reloaded.Health.State)
		assert.Equal(t, int64(0), reloaded.Health.UnreachableAccumSeconds,
			"a rejected advance must not mutate unreachable_accum_seconds")
	})
}
