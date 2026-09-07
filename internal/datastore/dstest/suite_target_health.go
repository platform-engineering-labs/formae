// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunTargetHealthDefaults verifies that a freshly created target has default
// health fields: health_state "unknown", zero accumulated seconds, a non-empty
// incarnation id, and nil timestamps.
func RunTargetHealthDefaults(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("TargetHealth_DefaultsOnCreate", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{
			Label:     "health-defaults-test",
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		}
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		loaded, err := ds.LoadTarget("health-defaults-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		require.NotNil(t, loaded.Health, "Health must be populated on load")

		assert.Equal(t, "unknown", loaded.Health.State, "initial health_state must be 'unknown'")
		assert.Equal(t, int64(0), loaded.Health.UnreachableAccumSeconds, "initial unreachable_accum_seconds must be 0")
		assert.NotEmpty(t, loaded.Health.IncarnationID, "incarnation id must be non-empty")
		assert.Nil(t, loaded.Health.LastSeenAt, "last_seen_at must be nil initially")
		assert.Nil(t, loaded.Health.ObservedAt, "observed_at must be nil initially")
		assert.Nil(t, loaded.Health.FirstUnreachableAt, "first_unreachable_at must be nil initially")
		assert.Nil(t, loaded.Health.LastSampleAt, "last_sample_at must be nil initially")
		assert.Empty(t, loaded.Health.LastErrorCode, "last_error_code must be empty initially")
	})
}

// RunTargetHealthStableAcrossUpdate verifies that UpdateTarget carries the
// incarnation id and current health forward onto the new version row; an update
// must not mint a new incarnation or reset health.
func RunTargetHealthStableAcrossUpdate(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("TargetHealth_StableAcrossUpdate", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := &pkgmodel.Target{
			Label:     "health-stable-test",
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		}
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		v1, err := ds.LoadTarget("health-stable-test")
		require.NoError(t, err)
		require.NotNil(t, v1)
		require.NotNil(t, v1.Health)
		incarnationID := v1.Health.IncarnationID
		require.NotEmpty(t, incarnationID)

		// Update the target (new version row).
		target.Config = json.RawMessage(`{"Region":"us-west-2"}`)
		_, err = ds.UpdateTarget(target)
		require.NoError(t, err)

		v2, err := ds.LoadTarget("health-stable-test")
		require.NoError(t, err)
		require.NotNil(t, v2)
		require.NotNil(t, v2.Health)

		assert.Equal(t, incarnationID, v2.Health.IncarnationID,
			"incarnation id must be unchanged across UpdateTarget")
		assert.Equal(t, "unknown", v2.Health.State,
			"health_state must be carried forward (still 'unknown')")
		assert.Equal(t, int64(0), v2.Health.UnreachableAccumSeconds,
			"unreachable_accum_seconds must be carried forward")
	})
}
