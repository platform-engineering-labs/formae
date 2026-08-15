// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunRecordAgentBoot verifies that recording a boot writes exactly one row
// carrying the supplied version, a non-empty id, and a timestamp at the moment
// it was recorded.
func RunRecordAgentBoot(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("RecordAgentBoot_WritesOneRow", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.LoadAgentBootsForTest == nil {
			t.Skip("backend does not provide LoadAgentBootsForTest")
		}

		before := time.Now().UTC().Add(-time.Second)
		require.NoError(t, ds.RecordAgentBoot(context.Background(), "0.89.0"))
		after := time.Now().UTC().Add(time.Second)

		boots, err := td.LoadAgentBootsForTest()
		require.NoError(t, err)
		require.Len(t, boots, 1, "exactly one row per recorded boot")

		assert.Equal(t, "0.89.0", boots[0].Version)
		assert.NotEmpty(t, boots[0].BootID, "boot id must be non-empty")
		assert.False(t, boots[0].BootedAt.Before(before), "booted_at must not precede the call")
		assert.False(t, boots[0].BootedAt.After(after), "booted_at must not follow the call")
	})
}

// RunAgentBootsAreAppendOnly verifies that successive boots accumulate rather
// than overwrite, and that ordering by (booted_at, boot_id) recovers the order
// they were recorded in. The feed reads the most recent row by exactly that
// ordering, so a backend that cannot reproduce it would report a stale version.
func RunAgentBootsAreAppendOnly(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AgentBoots_AppendOnlyAndOrdered", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.LoadAgentBootsForTest == nil {
			t.Skip("backend does not provide LoadAgentBootsForTest")
		}

		versions := []string{"0.88.0", "0.89.0", "0.89.1"}
		for _, v := range versions {
			require.NoError(t, ds.RecordAgentBoot(context.Background(), v))
		}

		boots, err := td.LoadAgentBootsForTest()
		require.NoError(t, err)
		require.Len(t, boots, len(versions), "each boot appends a row, none overwrite")

		seen := make(map[string]struct{}, len(boots))
		for i, b := range boots {
			assert.Equal(t, versions[i], b.Version, "rows come back in recorded order")
			_, dup := seen[b.BootID]
			assert.False(t, dup, "boot ids must be distinct")
			seen[b.BootID] = struct{}{}
		}
	})
}

// RunRecordAgentBootEmptyVersion verifies that a source build, which carries no
// stamped version, still records a boot. The row is what tells the console an
// agent is alive at all, so refusing to write one because the version string is
// empty would blank the header for exactly the local and e2e builds that report
// "0.0.0" or nothing.
func RunRecordAgentBootEmptyVersion(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("RecordAgentBoot_EmptyVersion", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.LoadAgentBootsForTest == nil {
			t.Skip("backend does not provide LoadAgentBootsForTest")
		}

		require.NoError(t, ds.RecordAgentBoot(context.Background(), ""))

		boots, err := td.LoadAgentBootsForTest()
		require.NoError(t, err)
		require.Len(t, boots, 1)
		assert.Empty(t, boots[0].Version)
		assert.NotEmpty(t, boots[0].BootID, "an unversioned boot still gets an id")
	})
}
