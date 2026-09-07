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

// RunGetUnreachableTargets verifies that GetUnreachableTargets returns only
// current (max-version) targets whose health_state is 'unreachable', with
// Health fully populated, and excludes reachable/unknown targets.
func RunGetUnreachableTargets(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetUnreachableTargets", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		unreachable := newHealthTestTarget("gut-unreachable-test")
		_, err := ds.CreateTarget(unreachable)
		require.NoError(t, err)
		observedAt := time.Now().UTC().Truncate(time.Second)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   "gut-unreachable-test",
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    observedAt,
			LastErrorCode: "NetworkFailure",
		})
		require.NoError(t, err)

		reachable := newHealthTestTarget("gut-reachable-test")
		_, err = ds.CreateTarget(reachable)
		require.NoError(t, err)
		_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel: "gut-reachable-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  observedAt,
			LastSeenAt:  &observedAt,
		})
		require.NoError(t, err)

		unknown := newHealthTestTarget("gut-unknown-test")
		_, err = ds.CreateTarget(unknown)
		require.NoError(t, err)

		targets, err := ds.GetUnreachableTargets()
		require.NoError(t, err)

		var found *pkgmodel.Target
		for _, tgt := range targets {
			assert.NotEqual(t, "gut-reachable-test", tgt.Label, "reachable targets must be excluded")
			assert.NotEqual(t, "gut-unknown-test", tgt.Label, "unknown-health targets must be excluded")
			if tgt.Label == "gut-unreachable-test" {
				found = tgt
			}
		}

		require.NotNil(t, found, "the unreachable target must be present in the result")
		require.NotNil(t, found.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateUnreachable, found.Health.State)
		assert.NotEmpty(t, found.Health.IncarnationID)
		require.NotNil(t, found.Health.ObservedAt)
		assert.WithinDuration(t, observedAt, *found.Health.ObservedAt, time.Second)
	})
}
