// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunUpdateTargetUnreapsResourcesOnRecovery verifies that recovering a reaped
// target (re-declaring it via UpdateTarget) brings its reaped resource rows back
// to life: they become visible to live queries again and no longer carry the
// reaped marker. This is what lets a recovery command's re-adopt writes land
// instead of being rejected as reaped tombstones.
func RunUpdateTargetUnreapsResourcesOnRecovery(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTarget_UnreapsResourcesOnRecovery", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawResourceOperationForTest == nil {
			t.Skip("backend does not expose RawResourceOperationForTest")
		}

		label := "recover-unreap-target"
		stack := "recover-unreap-stack"
		inc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		res := reapSuiteResource(util.NewID(), "bucket", stack, label)
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)
		uri := string(res.URI())

		reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		require.True(t, reaped, "setup: target must reap")

		// Sanity: the resource is reaped and invisible.
		live, err := ds.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Empty(t, live, "resource must be reaped before recovery")

		// Recover by re-declaring the target.
		recoverTarget, err := ds.LoadTarget(label)
		require.NoError(t, err)
		require.NotNil(t, recoverTarget)
		_, err = ds.UpdateTarget(recoverTarget)
		require.NoError(t, err)

		// The resource is live again and no longer carries the reaped marker.
		live, err = ds.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Len(t, live, 1, "recovery must un-reap the target's resources")

		op, err := td.RawResourceOperationForTest(uri)
		require.NoError(t, err)
		assert.NotEqual(t, string(resource_update.OperationReaped), op,
			"recovery must clear the reaped marker on the resource row")

		remaining, err := ds.LoadReapedResources()
		require.NoError(t, err)
		assert.Empty(t, remaining, "no reaped tombstone should remain on the recovered target")
	})
}
