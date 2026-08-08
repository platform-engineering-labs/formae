// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunStatsNamespaces_TypelessLiveResourceReportedUnderEmptyNamespace verifies
// the namespace gauges tolerate a live row whose type carries no namespace
// separator. The namespace is the part of the type before "::", so a typeless
// row has none and is reported under the empty key rather than aborting the
// whole Stats call. Both gauges are covered because they derive the namespace
// the same way over the managed and the unmanaged stack respectively.
func RunStatsNamespaces_TypelessLiveResourceReportedUnderEmptyNamespace(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsNamespaces_TypelessLiveResourceReportedUnderEmptyNamespace", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "thing-1", ""), "cmd-create-thing-1")

		unmanaged := liveResource(constants.UnmanagedStack, "ksuid-2", "thing-2", "")
		unmanaged.Managed = false
		storeLiveResource(t, td, unmanaged, "cmd-discover-thing-2")

		s, err := td.Stats()
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"": 1}, s.ManagedResources)
		assert.Equal(t, map[string]int{"": 1}, s.UnmanagedResources)
	})
}
