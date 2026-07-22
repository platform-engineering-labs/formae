// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunCheckTargetsReaped verifies that CheckTargetsReaped returns exactly the
// subset of the queried labels whose current row is 'reaped': a reaped target is
// reported, a live target and an unknown label are not, and an empty input
// yields no results.
func RunCheckTargetsReaped(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CheckTargetsReaped", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// A reaped target.
		reapedLabel := "check-reaped-target"
		inc, cutoff := seedReapReadyTarget(t, ds, reapedLabel, 100)
		reaped, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            reapedLabel,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		require.True(t, reaped, "setup: target must reap")

		// A live target that is never reaped.
		liveLabel := "check-live-target"
		_, err = ds.CreateTarget(&pkgmodel.Target{
			Label:     liveLabel,
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		})
		require.NoError(t, err)

		// Empty input yields nothing.
		got, err := ds.CheckTargetsReaped(nil)
		require.NoError(t, err)
		assert.Empty(t, got, "empty input must return no reaped labels")

		// Only the reaped label is reported, even alongside a live target and an
		// unknown label.
		got, err = ds.CheckTargetsReaped([]string{reapedLabel, liveLabel, "does-not-exist"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{reapedLabel}, got,
			"only the reaped target must be reported")

		// Querying just the live target returns nothing.
		got, err = ds.CheckTargetsReaped([]string{liveLabel})
		require.NoError(t, err)
		assert.Empty(t, got, "a live target must not be reported as reaped")
	})
}
