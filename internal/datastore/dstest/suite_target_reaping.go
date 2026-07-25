// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reapingTestTarget(label string, reaping json.RawMessage) *pkgmodel.Target {
	return &pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		Reaping:   reaping,
	}
}

// RunTargetReapingPersists verifies that the reaping behaviour is written on
// CreateTarget/UpdateTarget and reconstructed on load: an explicit reap-after,
// an explicit never, an absent reaping (global default), and an update that
// changes the behaviour.
func RunTargetReapingPersists(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("TargetReaping_Persists", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Explicit reap-after.
		_, err := ds.CreateTarget(reapingTestTarget("reap-after-test",
			json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":3600}`)))
		require.NoError(t, err)

		loaded, err := ds.LoadTarget("reap-after-test")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		behaviour, err := pkgmodel.ParseReaping(loaded.Reaping)
		require.NoError(t, err)
		after, ok := behaviour.(*pkgmodel.ReapAfter)
		require.True(t, ok, "expected ReapAfter, got %T", behaviour)
		assert.Equal(t, int64(3600), after.MaxUnreachableSeconds)

		// Explicit never.
		_, err = ds.CreateTarget(reapingTestTarget("reap-never-test",
			json.RawMessage(`{"Kind":"never"}`)))
		require.NoError(t, err)

		loadedNever, err := ds.LoadTarget("reap-never-test")
		require.NoError(t, err)
		require.NotNil(t, loadedNever)
		neverBehaviour, err := pkgmodel.ParseReaping(loadedNever.Reaping)
		require.NoError(t, err)
		_, ok = neverBehaviour.(*pkgmodel.NeverReap)
		assert.True(t, ok, "expected NeverReap, got %T", neverBehaviour)

		// Absent reaping falls back to the global default (after / 86400s).
		_, err = ds.CreateTarget(reapingTestTarget("reap-default-test", nil))
		require.NoError(t, err)

		loadedDefault, err := ds.LoadTarget("reap-default-test")
		require.NoError(t, err)
		require.NotNil(t, loadedDefault)
		defaultBehaviour, err := pkgmodel.ParseReaping(loadedDefault.Reaping)
		require.NoError(t, err)
		defaultAfter, ok := defaultBehaviour.(*pkgmodel.ReapAfter)
		require.True(t, ok, "expected ReapAfter default, got %T", defaultBehaviour)
		assert.Equal(t, pkgmodel.DefaultReapMaxUnreachableSeconds, defaultAfter.MaxUnreachableSeconds)

		// Update changes the behaviour.
		updated := reapingTestTarget("reap-after-test",
			json.RawMessage(`{"Kind":"never"}`))
		_, err = ds.UpdateTarget(updated)
		require.NoError(t, err)

		reloaded, err := ds.LoadTarget("reap-after-test")
		require.NoError(t, err)
		require.NotNil(t, reloaded)
		reloadedBehaviour, err := pkgmodel.ParseReaping(reloaded.Reaping)
		require.NoError(t, err)
		_, ok = reloadedBehaviour.(*pkgmodel.NeverReap)
		assert.True(t, ok, "update must persist the new reaping behaviour, got %T", reloadedBehaviour)

		// Reaping is also populated by the bulk load path.
		all, err := ds.LoadAllTargets()
		require.NoError(t, err)
		for _, tgt := range all {
			assert.NotEmpty(t, tgt.Reaping, "LoadAllTargets must populate Reaping for %s", tgt.Label)
		}
	})
}

// RunTargetReapingPopulatedByDependencyLoad verifies FindTargetsDependingOnMany
// populates the reaping behaviour on the targets it returns.
func RunTargetReapingPopulatedByDependencyLoad(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("TargetReaping_PopulatedByDependencyLoad", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Target whose config references a resource, so it shows up as a dependent.
		ksuid := "reapdep000000000000000000000"
		target := &pkgmodel.Target{
			Label:     "reap-dep-test",
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Endpoint":{"$ref":"formae://` + ksuid + `#/endpoint"}}`),
			Reaping:   json.RawMessage(`{"Kind":"never"}`),
		}
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		byKsuid, err := ds.FindTargetsDependingOnMany([]string{ksuid})
		require.NoError(t, err)
		deps := byKsuid[ksuid]
		require.NotEmpty(t, deps, "expected target to depend on %s", ksuid)
		found := false
		for _, tgt := range deps {
			if tgt.Label == "reap-dep-test" {
				found = true
				require.NotEmpty(t, tgt.Reaping, "FindTargetsDependingOnMany must populate Reaping")
				behaviour, perr := pkgmodel.ParseReaping(tgt.Reaping)
				require.NoError(t, perr)
				_, ok := behaviour.(*pkgmodel.NeverReap)
				assert.True(t, ok)
			}
		}
		assert.True(t, found, "reap-dep-test must be among dependents")
	})
}

// RunUpdateTargetMintsFreshIncarnationOnReaped verifies the recovery invariant:
// re-declaring a target whose current row is 'reaped' mints a fresh incarnation
// id and resets health to 'unknown' (accrual 0) rather than carrying the reaped
// state forward.
func RunUpdateTargetMintsFreshIncarnationOnReaped(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTarget_MintsFreshIncarnationOnReaped", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetTargetHealthStateForTest == nil {
			t.Skip("backend does not expose SetTargetHealthStateForTest")
		}

		target := reapingTestTarget("reap-recover-test", json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":3600}`))
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		v1, err := ds.LoadTarget("reap-recover-test")
		require.NoError(t, err)
		require.NotNil(t, v1)
		require.NotNil(t, v1.Health)
		oldIncarnation := v1.Health.IncarnationID
		require.NotEmpty(t, oldIncarnation)

		// Force the current row into the reaped state.
		require.NoError(t, td.SetTargetHealthStateForTest("reap-recover-test", pkgmodel.TargetHealthStateReaped))

		// Re-declare the target (recovery).
		_, err = ds.UpdateTarget(target)
		require.NoError(t, err)

		v2, err := ds.LoadTarget("reap-recover-test")
		require.NoError(t, err)
		require.NotNil(t, v2)
		require.NotNil(t, v2.Health)

		assert.NotEqual(t, oldIncarnation, v2.Health.IncarnationID,
			"recovery must mint a fresh incarnation id")
		assert.NotEmpty(t, v2.Health.IncarnationID)
		assert.Equal(t, pkgmodel.TargetHealthStateUnknown, v2.Health.State,
			"recovery must reset health to 'unknown'")
		assert.Equal(t, int64(0), v2.Health.UnreachableAccumSeconds,
			"recovery must reset accrual to 0")
		assert.Nil(t, v2.Health.FirstUnreachableAt,
			"recovery must clear first_unreachable_at")
	})
}

// RunUpdateTargetCarriesHealthForwardWhenNotReaped guards the recovery branch:
// a non-reaped UpdateTarget must NOT mint a new incarnation.
func RunUpdateTargetCarriesHealthForwardWhenNotReaped(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateTarget_CarriesHealthForwardWhenNotReaped", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		target := reapingTestTarget("reap-noRecover-test", json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":3600}`))
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		v1, err := ds.LoadTarget("reap-noRecover-test")
		require.NoError(t, err)
		oldIncarnation := v1.Health.IncarnationID

		_, err = ds.UpdateTarget(target)
		require.NoError(t, err)

		v2, err := ds.LoadTarget("reap-noRecover-test")
		require.NoError(t, err)
		assert.Equal(t, oldIncarnation, v2.Health.IncarnationID,
			"a non-reaped update must carry the incarnation id forward")
	})
}

// RunSuccessObservationZeroesAccrual verifies that a reachable ("success")
// observation clears any accrued unreachability (first_unreachable_at and
// unreachable_accum_seconds) on the target's current row.
func RunSuccessObservationZeroesAccrual(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("SuccessObservation_ZeroesAccrual", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetTargetAccrualForTest == nil {
			t.Skip("backend does not expose SetTargetAccrualForTest")
		}

		target := reapingTestTarget("accrual-reset-test", nil)
		_, err := ds.CreateTarget(target)
		require.NoError(t, err)

		// Seed a non-pristine accrual state.
		firstUnreachable := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		require.NoError(t, td.SetTargetAccrualForTest("accrual-reset-test", firstUnreachable, 3600))

		// Sanity: accrual is set.
		seeded, err := ds.LoadTarget("accrual-reset-test")
		require.NoError(t, err)
		require.NotNil(t, seeded.Health)
		require.Equal(t, int64(3600), seeded.Health.UnreachableAccumSeconds)
		require.NotNil(t, seeded.Health.FirstUnreachableAt)

		// Apply a reachable observation.
		now := time.Now().UTC().Truncate(time.Second)
		applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel: "accrual-reset-test",
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  now,
			LastSeenAt:  &now,
		})
		require.NoError(t, err)
		require.True(t, applied, "reachable observation must be applied")

		loaded, err := ds.LoadTarget("accrual-reset-test")
		require.NoError(t, err)
		require.NotNil(t, loaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReachable, loaded.Health.State)
		assert.Equal(t, int64(0), loaded.Health.UnreachableAccumSeconds,
			"a success observation must zero unreachable_accum_seconds")
		assert.Nil(t, loaded.Health.FirstUnreachableAt,
			"a success observation must clear first_unreachable_at")
	})
}
