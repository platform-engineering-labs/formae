// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunReapedTargetsInvisibleToQuery verifies that a target whose current row
// carries the reaped tombstone is invisible to the inventory query
// (QueryTargets, behind `inventory targets` / the ListTargets API), matching how
// a reaped resource is hidden from live-resource reads — yet the row is retained
// and still returned by LoadTarget/LoadAllTargets, which the recovery re-declare
// and the stats reaped-count depend on.
func RunReapedTargetsInvisibleToQuery(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("ReapedTargetsInvisibleToQuery", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetTargetHealthStateForTest == nil {
			t.Skip("backend does not expose SetTargetHealthStateForTest")
		}

		_, err := ds.CreateTarget(&pkgmodel.Target{
			Label:     "live-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		require.NoError(t, err)
		_, err = ds.CreateTarget(&pkgmodel.Target{
			Label:     "reaped-target-inv",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		// Both visible to the inventory query before reaping.
		before, err := ds.QueryTargets(&datastore.TargetQuery{})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"live-target", "reaped-target-inv"}, labelsOf(before),
			"both targets should be visible to QueryTargets before reaping")

		// Reap one.
		require.NoError(t, td.SetTargetHealthStateForTest("reaped-target-inv",
			string(pkgmodel.TargetHealthStateReaped)))

		// Invisible to the inventory query.
		after, err := ds.QueryTargets(&datastore.TargetQuery{})
		require.NoError(t, err)
		assert.Equal(t, []string{"live-target"}, labelsOf(after),
			"a reaped target must be invisible to QueryTargets")

		// Retained: still loadable for recovery re-declare and the stats
		// reaped-count (LoadTarget / LoadAllTargets must NOT hide it).
		reaped, err := ds.LoadTarget("reaped-target-inv")
		require.NoError(t, err)
		require.NotNil(t, reaped, "reaped target must remain loadable via LoadTarget (recovery path)")
		assert.Equal(t, pkgmodel.TargetHealthStateReaped, reaped.Health.State)

		all, err := ds.LoadAllTargets()
		require.NoError(t, err)
		assert.Contains(t, labelsOf(all), "reaped-target-inv",
			"reaped target must remain in LoadAllTargets (stats reaped-count path)")
	})
}

// RunStatsExcludesReapedTargets verifies that the agent-stats target count
// (Stats().Targets, per namespace) excludes reaped targets, so a reaped target
// is not double-counted against both the live total and the separate
// reaped-count breakout.
func RunStatsExcludesReapedTargets(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsExcludesReapedTargets", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetTargetHealthStateForTest == nil {
			t.Skip("backend does not expose SetTargetHealthStateForTest")
		}

		_, err := ds.CreateTarget(&pkgmodel.Target{Label: "stats-live", Namespace: "AWS", Config: json.RawMessage(`{}`)})
		require.NoError(t, err)
		_, err = ds.CreateTarget(&pkgmodel.Target{Label: "stats-reaped", Namespace: "AWS", Config: json.RawMessage(`{}`)})
		require.NoError(t, err)

		before, err := ds.Stats()
		require.NoError(t, err)
		assert.Equal(t, 2, before.Targets["AWS"], "both targets counted before reaping")

		require.NoError(t, td.SetTargetHealthStateForTest("stats-reaped",
			string(pkgmodel.TargetHealthStateReaped)))

		after, err := ds.Stats()
		require.NoError(t, err)
		assert.Equal(t, 1, after.Targets["AWS"], "a reaped target must not be counted in the stats target total")
	})
}

func labelsOf(targets []*pkgmodel.Target) []string {
	labels := make([]string, 0, len(targets))
	for _, target := range targets {
		labels = append(labels, target.Label)
	}
	return labels
}
