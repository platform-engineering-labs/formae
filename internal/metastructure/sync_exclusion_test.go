// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

// The sync exclusion set is claimed by every changeset that writes a resource
// and released when that changeset finishes. A plain set cannot express two
// writers: whichever finished first would release a claim it did not solely
// hold, and the other changeset would carry on writing with the synchronizer
// free to read the resource mid-write, which is the race the exclusion exists
// to close.
//
// Concurrent writers on one resource are refused at admission today, so this is
// a property held by construction rather than one exercised in production. It
// is counted anyway because the counting is smaller than the argument for why
// it is unnecessary, and the admission guard is not this package's to rely on.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSyncExclusion_LastWriterReleases(t *testing.T) {
	excluded := map[string]int{}

	excludeFromSync(excluded, "formae://a#")
	excludeFromSync(excluded, "formae://a#")

	releaseFromSync(excluded, "formae://a#")
	_, stillExcluded := excluded["formae://a#"]
	assert.True(t, stillExcluded,
		"one writer finishing must not expose a resource another is still writing")

	releaseFromSync(excluded, "formae://a#")
	_, nowExcluded := excluded["formae://a#"]
	assert.False(t, nowExcluded, "the last writer's release must lift the exclusion")
}

func TestSyncExclusion_SingleWriterRoundTrip(t *testing.T) {
	excluded := map[string]int{}

	excludeFromSync(excluded, "formae://a#")
	_, isExcluded := excluded["formae://a#"]
	assert.True(t, isExcluded)

	releaseFromSync(excluded, "formae://a#")
	_, stillThere := excluded["formae://a#"]
	assert.False(t, stillThere)
}

// A release with no matching claim is not an error worth failing a command
// over, but it must not leave a negative count that a later claim would have
// to climb out of before the resource is protected again.
func TestSyncExclusion_ReleaseWithoutClaimIsInert(t *testing.T) {
	excluded := map[string]int{}

	releaseFromSync(excluded, "formae://ghost#")
	assert.Empty(t, excluded)

	excludeFromSync(excluded, "formae://ghost#")
	_, isExcluded := excluded["formae://ghost#"]
	assert.True(t, isExcluded, "a claim after a stray release must still protect")
}

func TestSyncExclusion_ResourcesAreIndependent(t *testing.T) {
	excluded := map[string]int{}

	excludeFromSync(excluded, "formae://a#")
	excludeFromSync(excluded, "formae://b#")
	releaseFromSync(excluded, "formae://a#")

	_, aExcluded := excluded["formae://a#"]
	_, bExcluded := excluded["formae://b#"]
	assert.False(t, aExcluded)
	assert.True(t, bExcluded, "releasing one resource must not release another")
}
