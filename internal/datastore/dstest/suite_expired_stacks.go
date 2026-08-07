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

// expiredLabels reduces GetExpiredStacks to the set of stack labels it reported,
// so a case can assert on the stacks it created without caring about ordering.
func expiredLabels(t *testing.T, ds datastore.Datastore) []string {
	t.Helper()

	expired, err := ds.GetExpiredStacks()
	require.NoError(t, err)

	labels := make([]string, 0, len(expired))
	for _, e := range expired {
		labels = append(labels, e.StackLabel)
	}
	return labels
}

// RunGetExpiredStacks_RelativeAnchoredAtCreation is the regression case for the
// anchor: a stack that gains a later version — which is what a description edit
// does — still expires on the deadline measured from its creation.
func RunGetExpiredStacks_RelativeAnchoredAtCreation(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_RelativeAnchoredAtCreation", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose SetStackValidFromForTest")
		}

		stack := &pkgmodel.Stack{Label: "anchor-stack", Description: "before"}
		_, err := ds.CreateStack(stack, "cmd-1")
		require.NoError(t, err)

		stored, err := ds.GetStackByLabel("anchor-stack")
		require.NoError(t, err)
		require.NotNil(t, stored)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type:         "ttl",
			Label:        "one-hour",
			TTLSeconds:   3600,
			OnDependents: "cascade",
			StackID:      stored.ID,
		}, "cmd-1")
		require.NoError(t, err)

		// A description edit adds a second version.
		stored.Description = "after"
		_, err = ds.UpdateStack(stored, "cmd-2")
		require.NoError(t, err)

		// The stack was created two hours ago, so its one-hour deadline passed an
		// hour ago; the description edit landed a minute ago. Anchored at
		// creation the stack is overdue, anchored at the latest version it is not.
		now := time.Now().UTC()
		require.NoError(t, td.SetStackValidFromForTest("anchor-stack", []time.Time{
			now.Add(-2 * time.Hour),
			now.Add(-1 * time.Minute),
		}))

		assert.Contains(t, expiredLabels(t, ds), "anchor-stack")
	})
}

// RunGetExpiredStacks_Absolute covers the absolute variant in both directions,
// including a stack old enough that a relative reading of the same policy would
// have fired.
func RunGetExpiredStacks_Absolute(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_Absolute", func(t *testing.T) {
		t.Run("past deadline expires", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			stack := &pkgmodel.Stack{Label: "past-stack"}
			_, err := ds.CreateStack(stack, "cmd-1")
			require.NoError(t, err)

			stored, err := ds.GetStackByLabel("past-stack")
			require.NoError(t, err)

			_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
				Type:         "ttl",
				Label:        "trial",
				ExpiresAt:    time.Now().UTC().Add(-1 * time.Hour),
				OnDependents: "abort",
				StackID:      stored.ID,
			}, "cmd-1")
			require.NoError(t, err)

			assert.Contains(t, expiredLabels(t, ds), "past-stack")
		})

		t.Run("future deadline holds even on an old stack", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			if td.SetStackValidFromForTest == nil {
				t.Skip("backend does not expose SetStackValidFromForTest")
			}

			stack := &pkgmodel.Stack{Label: "future-stack"}
			_, err := ds.CreateStack(stack, "cmd-1")
			require.NoError(t, err)

			stored, err := ds.GetStackByLabel("future-stack")
			require.NoError(t, err)

			_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
				Type:         "ttl",
				Label:        "trial",
				ExpiresAt:    time.Now().UTC().Add(1 * time.Hour),
				OnDependents: "abort",
				StackID:      stored.ID,
			}, "cmd-1")
			require.NoError(t, err)

			require.NoError(t, td.SetStackValidFromForTest("future-stack", []time.Time{
				time.Now().UTC().Add(-30 * 24 * time.Hour),
			}))

			assert.NotContains(t, expiredLabels(t, ds), "future-stack")
		})
	})
}

// RunGetExpiredStacks_MalformedExpiresAt is the reason the absolute comparison
// takes no cast. A value that cannot be read must cost only the stack that
// carries it: under a cast it would abort the statement instead, and nothing in
// the installation would ever expire again.
func RunGetExpiredStacks_MalformedExpiresAt(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_MalformedExpiresAt", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetPolicyDataForTest == nil {
			t.Skip("backend does not expose SetPolicyDataForTest")
		}

		corrupt := &pkgmodel.Stack{Label: "corrupt-stack"}
		_, err := ds.CreateStack(corrupt, "cmd-1")
		require.NoError(t, err)
		storedCorrupt, err := ds.GetStackByLabel("corrupt-stack")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type:         "ttl",
			Label:        "corrupt-ttl",
			ExpiresAt:    time.Now().UTC().Add(-1 * time.Hour),
			OnDependents: "abort",
			StackID:      storedCorrupt.ID,
		}, "cmd-1")
		require.NoError(t, err)
		require.NoError(t, td.SetPolicyDataForTest("corrupt-ttl",
			`{"ExpiresAt":"not-a-timestamp","OnDependents":"abort"}`))

		// A second stack in the same scan is genuinely overdue.
		healthy := &pkgmodel.Stack{Label: "healthy-stack"}
		_, err = ds.CreateStack(healthy, "cmd-2")
		require.NoError(t, err)
		storedHealthy, err := ds.GetStackByLabel("healthy-stack")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type:         "ttl",
			Label:        "healthy-ttl",
			ExpiresAt:    time.Now().UTC().Add(-1 * time.Hour),
			OnDependents: "abort",
			StackID:      storedHealthy.ID,
		}, "cmd-2")
		require.NoError(t, err)

		labels := expiredLabels(t, ds)
		assert.NotContains(t, labels, "corrupt-stack")
		assert.Contains(t, labels, "healthy-stack")
	})
}

// RunGetExpiredStacks_MalformedExpiresAtSortingLow is the sharp edge of
// comparing deadlines as strings: a value that is not a timestamp at all can
// still sort before now, and "sorts before now" is what triggers a destroy.
// Failing safe has to mean "not a canonical deadline", not "sorts high".
func RunGetExpiredStacks_MalformedExpiresAtSortingLow(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_MalformedExpiresAtSortingLow", func(t *testing.T) {
		// Every one of these sorts before a current canonical timestamp.
		for _, malformed := range []string{``, `0000`, `0000-00-00T00:00:00Z`, `1`, `2026-08-07`} {
			t.Run(malformed, func(t *testing.T) {
				td := newDS(t)
				ds := td.Datastore
				defer td.CleanUpFn() //nolint:errcheck

				if td.SetPolicyDataForTest == nil {
					t.Skip("backend does not expose SetPolicyDataForTest")
				}

				stack := &pkgmodel.Stack{Label: "low-sorting-stack"}
				_, err := ds.CreateStack(stack, "cmd-1")
				require.NoError(t, err)
				stored, err := ds.GetStackByLabel("low-sorting-stack")
				require.NoError(t, err)

				_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
					Type: "ttl", Label: "low-sorting-ttl",
					ExpiresAt:    time.Now().UTC().Add(time.Hour),
					OnDependents: "abort", StackID: stored.ID,
				}, "cmd-1")
				require.NoError(t, err)

				payload, err := json.Marshal(map[string]any{
					"ExpiresAt": malformed, "OnDependents": "abort",
				})
				require.NoError(t, err)
				require.NoError(t, td.SetPolicyDataForTest("low-sorting-ttl", string(payload)))

				assert.NotContains(t, expiredLabels(t, ds), "low-sorting-stack",
					"a value that is not a canonical deadline must never expire a stack")
			})
		}
	})
}

// RunGetExpiredStacks_OneRowPerStack guards the contract the expirer relies on:
// it destroys once per reported entry, so a stack carrying more than one expired
// policy must still be reported once, however the policies differ.
func RunGetExpiredStacks_OneRowPerStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_OneRowPerStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose SetStackValidFromForTest")
		}

		stack := &pkgmodel.Stack{Label: "double-policy-stack"}
		_, err := ds.CreateStack(stack, "cmd-1")
		require.NoError(t, err)
		stored, err := ds.GetStackByLabel("double-policy-stack")
		require.NoError(t, err)

		// An inline relative policy and an attached standalone absolute policy,
		// both overdue, with different deadlines.
		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "inline-ttl", TTLSeconds: 3600,
			OnDependents: "abort", StackID: stored.ID,
		}, "cmd-1")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "shared-deadline",
			ExpiresAt:    time.Now().UTC().Add(-2 * time.Hour),
			OnDependents: "abort",
		}, "cmd-1")
		require.NoError(t, err)
		require.NoError(t, ds.AttachPolicyToStack(stored.ID, "shared-deadline"))

		require.NoError(t, td.SetStackValidFromForTest("double-policy-stack", []time.Time{
			time.Now().UTC().Add(-4 * time.Hour),
		}))

		var count int
		for _, label := range expiredLabels(t, ds) {
			if label == "double-policy-stack" {
				count++
			}
		}
		assert.Equal(t, 1, count, "a stack must be reported once however many of its policies expired")
	})
}

// RunGetExpiredStacks_BothVariantsSet pins the defensive resolution of a row
// that is outside the one-of: ExpiresAt wins, and the scan stays intact. Not
// reachable through any accepted input — only through corrupt or hand-edited
// state.
func RunGetExpiredStacks_BothVariantsSet(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_BothVariantsSet", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetPolicyDataForTest == nil || td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose the raw-state test hooks")
		}

		stack := &pkgmodel.Stack{Label: "both-stack"}
		_, err := ds.CreateStack(stack, "cmd-1")
		require.NoError(t, err)
		stored, err := ds.GetStackByLabel("both-stack")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type:         "ttl",
			Label:        "both-ttl",
			TTLSeconds:   3600,
			OnDependents: "abort",
			StackID:      stored.ID,
		}, "cmd-1")
		require.NoError(t, err)

		// The stack is young, so the relative reading says "not yet"; ExpiresAt
		// says "overdue" and is the one that must decide.
		future := time.Now().UTC().Add(-1 * time.Hour).Format(pkgmodel.ExpiresAtLayout)
		require.NoError(t, td.SetPolicyDataForTest("both-ttl",
			`{"TTLSeconds":3600,"ExpiresAt":"`+future+`","OnDependents":"abort"}`))

		assert.Contains(t, expiredLabels(t, ds), "both-stack")
	})
}

// RunGetExpiredStacks_LegacyRelative covers a stored policy written before the
// absolute variant existed: TTLSeconds alone, no ExpiresAt key at all.
func RunGetExpiredStacks_LegacyRelative(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_LegacyRelative", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetPolicyDataForTest == nil || td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose the raw-state test hooks")
		}

		expiredStack := &pkgmodel.Stack{Label: "legacy-expired"}
		_, err := ds.CreateStack(expiredStack, "cmd-1")
		require.NoError(t, err)
		storedExpired, err := ds.GetStackByLabel("legacy-expired")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "legacy-expired-ttl", TTLSeconds: 0,
			OnDependents: "abort", StackID: storedExpired.ID,
		}, "cmd-1")
		require.NoError(t, err)
		require.NoError(t, td.SetPolicyDataForTest("legacy-expired-ttl",
			`{"TTLSeconds":3600,"OnDependents":"abort"}`))
		require.NoError(t, td.SetStackValidFromForTest("legacy-expired", []time.Time{
			time.Now().UTC().Add(-2 * time.Hour),
		}))

		liveStack := &pkgmodel.Stack{Label: "legacy-live"}
		_, err = ds.CreateStack(liveStack, "cmd-2")
		require.NoError(t, err)
		storedLive, err := ds.GetStackByLabel("legacy-live")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "legacy-live-ttl", TTLSeconds: 0,
			OnDependents: "abort", StackID: storedLive.ID,
		}, "cmd-2")
		require.NoError(t, err)
		require.NoError(t, td.SetPolicyDataForTest("legacy-live-ttl",
			`{"TTLSeconds":86400,"OnDependents":"abort"}`))

		labels := expiredLabels(t, ds)
		assert.Contains(t, labels, "legacy-expired")
		assert.NotContains(t, labels, "legacy-live")
	})
}

// RunGetExpiredStacks_ReportsAnchorAndDeadline checks the fields the expirer
// logs, so an expiry that destroys real resources can be explained afterwards.
func RunGetExpiredStacks_ReportsAnchorAndDeadline(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetExpiredStacks_ReportsAnchorAndDeadline", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose SetStackValidFromForTest")
		}

		relative := &pkgmodel.Stack{Label: "reported-relative"}
		_, err := ds.CreateStack(relative, "cmd-1")
		require.NoError(t, err)
		storedRelative, err := ds.GetStackByLabel("reported-relative")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "reported-relative-ttl", TTLSeconds: 3600,
			OnDependents: "abort", StackID: storedRelative.ID,
		}, "cmd-1")
		require.NoError(t, err)

		createdAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		require.NoError(t, td.SetStackValidFromForTest("reported-relative", []time.Time{createdAt}))

		absolute := &pkgmodel.Stack{Label: "reported-absolute"}
		_, err = ds.CreateStack(absolute, "cmd-2")
		require.NoError(t, err)
		storedAbsolute, err := ds.GetStackByLabel("reported-absolute")
		require.NoError(t, err)

		deadline := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
		_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
			Type: "ttl", Label: "reported-absolute-ttl", ExpiresAt: deadline,
			OnDependents: "abort", StackID: storedAbsolute.ID,
		}, "cmd-2")
		require.NoError(t, err)

		expired, err := ds.GetExpiredStacks()
		require.NoError(t, err)

		byLabel := make(map[string]datastore.ExpiredStackInfo, len(expired))
		for _, e := range expired {
			byLabel[e.StackLabel] = e
		}

		gotRelative, ok := byLabel["reported-relative"]
		require.True(t, ok, "relative stack should be reported expired")
		require.NotNil(t, gotRelative.TTLSeconds)
		assert.Equal(t, int64(3600), *gotRelative.TTLSeconds)
		assert.Empty(t, gotRelative.ExpiresAt)
		assert.WithinDuration(t, createdAt, gotRelative.StackCreatedAt, time.Second)
		assert.Equal(t, createdAt.Add(time.Hour).Format(pkgmodel.ExpiresAtLayout), gotRelative.Deadline())

		gotAbsolute, ok := byLabel["reported-absolute"]
		require.True(t, ok, "absolute stack should be reported expired")
		assert.Nil(t, gotAbsolute.TTLSeconds)
		assert.Equal(t, deadline.Format(pkgmodel.ExpiresAtLayout), gotAbsolute.ExpiresAt)
		assert.Equal(t, deadline.Format(pkgmodel.ExpiresAtLayout), gotAbsolute.Deadline())
	})
}

// RunTTLPolicyVariantRoundTrip asserts that which variant a policy carries
// survives create, read, update and re-read, for inline and standalone policies
// alike. Key presence is the invariant most easily lost in a hand-written store.
func RunTTLPolicyVariantRoundTrip(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("TTLPolicyVariantRoundTrip", func(t *testing.T) {
		t.Run("inline", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			stack := &pkgmodel.Stack{Label: "round-trip-stack"}
			_, err := ds.CreateStack(stack, "cmd-1")
			require.NoError(t, err)
			stored, err := ds.GetStackByLabel("round-trip-stack")
			require.NoError(t, err)

			deadline := time.Date(2036, 8, 7, 12, 0, 0, 0, time.UTC)
			_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{
				Type: "ttl", Label: "inline-absolute", ExpiresAt: deadline,
				OnDependents: "cascade", StackID: stored.ID,
			}, "cmd-1")
			require.NoError(t, err)

			policies, err := ds.GetPoliciesForStack(stored.ID)
			require.NoError(t, err)
			require.Len(t, policies, 1)

			ttl, ok := policies[0].(*pkgmodel.TTLPolicy)
			require.True(t, ok)
			assert.True(t, ttl.IsAbsolute())
			assert.Equal(t, deadline, ttl.ExpiresAt)
			assert.Equal(t, int64(0), ttl.TTLSeconds)
			assert.Equal(t, "cascade", ttl.OnDependents)
		})

		t.Run("standalone switched between variants", func(t *testing.T) {
			td := newDS(t)
			ds := td.Datastore
			defer td.CleanUpFn() //nolint:errcheck

			_, err := ds.CreatePolicy(&pkgmodel.TTLPolicy{
				Type: "ttl", Label: "shared-ttl", TTLSeconds: 7200, OnDependents: "abort",
			}, "cmd-1")
			require.NoError(t, err)

			got, err := ds.GetStandalonePolicy("shared-ttl")
			require.NoError(t, err)
			ttl, ok := got.(*pkgmodel.TTLPolicy)
			require.True(t, ok)
			assert.False(t, ttl.IsAbsolute())
			assert.Equal(t, int64(7200), ttl.TTLSeconds)

			// Switching a shared policy to an absolute deadline must not leave the
			// old relative value behind for the query to find.
			deadline := time.Date(2036, 8, 7, 12, 0, 0, 0, time.UTC)
			_, err = ds.UpdatePolicy(&pkgmodel.TTLPolicy{
				Type: "ttl", Label: "shared-ttl", ExpiresAt: deadline, OnDependents: "abort",
			}, "cmd-2")
			require.NoError(t, err)

			got, err = ds.GetStandalonePolicy("shared-ttl")
			require.NoError(t, err)
			ttl, ok = got.(*pkgmodel.TTLPolicy)
			require.True(t, ok)
			assert.True(t, ttl.IsAbsolute())
			assert.Equal(t, deadline, ttl.ExpiresAt)
			assert.Equal(t, int64(0), ttl.TTLSeconds)
		})
	})
}

// RunStackCreatedAtIsFirstVersion pins the read path to the same anchor as the
// expiry query: CreatedAt is when the stack was created, not when it last
// changed. The value is on the wire for API clients, and the inventory view
// computes a displayed TTL deadline from it.
func RunStackCreatedAtIsFirstVersion(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StackCreatedAtIsFirstVersion", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose SetStackValidFromForTest")
		}

		stack := &pkgmodel.Stack{Label: "created-at-stack", Description: "before"}
		_, err := ds.CreateStack(stack, "cmd-1")
		require.NoError(t, err)

		stored, err := ds.GetStackByLabel("created-at-stack")
		require.NoError(t, err)
		stored.Description = "after"
		_, err = ds.UpdateStack(stored, "cmd-2")
		require.NoError(t, err)

		createdAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
		require.NoError(t, td.SetStackValidFromForTest("created-at-stack", []time.Time{
			createdAt,
			time.Now().UTC().Add(-1 * time.Minute),
		}))

		stacks, err := ds.ListAllStacks()
		require.NoError(t, err)

		var found *pkgmodel.Stack
		for _, s := range stacks {
			if s.Label == "created-at-stack" {
				found = s
			}
		}
		require.NotNil(t, found)
		assert.Equal(t, "after", found.Description)
		assert.WithinDuration(t, createdAt, found.CreatedAt, time.Second)
	})
}

// RunStackCreatedAtSerializesOverAPI guards the wire representation the API
// hands to external clients, not just the in-process struct.
func RunStackCreatedAtSerializesOverAPI(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StackCreatedAtSerializesOverAPI", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetStackValidFromForTest == nil {
			t.Skip("backend does not expose SetStackValidFromForTest")
		}

		stack := &pkgmodel.Stack{Label: "api-stack", Description: "before"}
		_, err := ds.CreateStack(stack, "cmd-1")
		require.NoError(t, err)

		stored, err := ds.GetStackByLabel("api-stack")
		require.NoError(t, err)
		stored.Description = "after"
		_, err = ds.UpdateStack(stored, "cmd-2")
		require.NoError(t, err)

		createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		require.NoError(t, td.SetStackValidFromForTest("api-stack", []time.Time{
			createdAt,
			time.Now().UTC().Add(-1 * time.Minute),
		}))

		stacks, err := ds.ListAllStacks()
		require.NoError(t, err)

		var found *pkgmodel.Stack
		for _, s := range stacks {
			if s.Label == "api-stack" {
				found = s
			}
		}
		require.NotNil(t, found)

		encoded, err := json.Marshal(found)
		require.NoError(t, err)

		var wire struct {
			CreatedAt time.Time `json:"CreatedAt"`
		}
		require.NoError(t, json.Unmarshal(encoded, &wire))
		assert.WithinDuration(t, createdAt, wire.CreatedAt, time.Second)
	})
}
