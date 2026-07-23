// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reapSuiteResource builds a managed resource on the given target and stack,
// pinned to a known ksuid so tests can address its resources-table row through
// resource.URI().
func reapSuiteResource(ksuid, label, stack, target string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Ksuid:      ksuid,
		NativeID:   "native-" + ksuid,
		Stack:      stack,
		Type:       "AWS::S3::Bucket",
		Label:      label,
		Target:     target,
		Managed:    true,
		Properties: json.RawMessage(`{"key":"value"}`),
	}
}

// seedReapReadyTarget creates a target and drives its current row into a state
// that satisfies the reap CAS: health_state='unreachable', reap_kind='after',
// unreachable_accum_seconds >= threshold, with last_seen_at and last_sample_at
// both set to instants comfortably in the past. It uses only the public
// Datastore API. Returns the target's incarnation id and the grace cutoff the
// caller should use for LastSeenBefore/LastSampleBefore.
func seedReapReadyTarget(t *testing.T, ds datastore.Datastore, label string, thresholdSeconds int64) (incarnation string, cutoff time.Time) {
	t.Helper()

	_, err := ds.CreateTarget(&pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		Reaping:   json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":` + itoa(thresholdSeconds) + `}`),
	})
	require.NoError(t, err)

	loaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, loaded.Health)
	inc := loaded.Health.IncarnationID
	require.NotEmpty(t, inc)

	// Drive to unreachable, seeding an old last_seen_at.
	seenAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	observedAt := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   label,
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    observedAt,
		LastSeenAt:    &seenAt,
		IncarnationID: inc,
	})
	require.NoError(t, err)
	require.True(t, applied, "unreachable observation must apply")

	// Accrue at or past the threshold, seeding an old last_sample_at.
	sampleAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	applied, err = ds.AdvanceTargetAccrual(label, inc, sampleAt, thresholdSeconds)
	require.NoError(t, err)
	require.True(t, applied, "accrual advance must apply")

	return inc, time.Now().UTC()
}

// itoa avoids importing strconv just for the reaping JSON literal.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// RunPersistTargetReapHappyPath (S1) verifies that an over-threshold unreachable
// target with N resources reaps: reaped=true, target health 'reaped', all N
// resources reaped (invisible to live queries, retained as tombstones), one
// audit row.
func RunPersistTargetReapHappyPath(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("PersistTargetReap_HappyPath", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawResourceOperationForTest == nil {
			t.Skip("backend does not expose RawResourceOperationForTest")
		}

		label := "reap-happy-target"
		stack := "reap-happy-stack"
		inc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		const n = 3
		uris := make([]string, 0, n)
		for i := 0; i < n; i++ {
			res := reapSuiteResource(util.NewID(), "bucket", stack, label)
			_, err := ds.StoreResource(res, "cmd-create")
			require.NoError(t, err)
			uris = append(uris, string(res.URI()))
		}

		// Sanity: resources are live before the reap.
		live, err := ds.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, live, n)

		reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		assert.True(t, reaped, "an over-threshold unreachable target must reap")

		// Target transitioned to reaped.
		loaded, err := ds.LoadTarget(label)
		require.NoError(t, err)
		require.NotNil(t, loaded.Health)
		assert.Equal(t, pkgmodel.TargetHealthStateReaped, loaded.Health.State)

		// Every resource is now a reaped tombstone: invisible to live queries…
		live, err = ds.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, live, "reaped resources must be invisible to live queries")

		// …but retained in the table with the reaped marker.
		for _, uri := range uris {
			op, opErr := td.RawResourceOperationForTest(uri)
			require.NoError(t, opErr)
			assert.Equal(t, string(resource_update.OperationReaped), op,
				"resource %s must carry the reaped marker", uri)
		}

		// Exactly one audit row.
		if td.CountReapAuditRowsForTest != nil {
			count, cErr := td.CountReapAuditRowsForTest(label)
			require.NoError(t, cErr)
			assert.Equal(t, 1, count, "reap must write exactly one audit row")
		}
	})
}

// RunPersistTargetReapGuards (S2) covers the guard rejections. Each subtest sets
// up a reap-ready target and then perturbs one precondition, asserting the reap
// is a no-op (reaped=false, target and resources untouched, no audit row).
func RunPersistTargetReapGuards(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	// (a) The current row was reset to reachable (committed) before the reap.
	t.Run("PersistTargetReap_Guard_ResetToReachable", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		label := "reap-guard-reachable"
		stack := "reap-guard-reachable-stack"
		inc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		res := reapSuiteResource(util.NewID(), "bucket", stack, label)
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		// A reachable observation flips the row back and zeroes accrual.
		now := time.Now().UTC().Truncate(time.Second)
		applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   label,
			State:         pkgmodel.TargetHealthStateReachable,
			ObservedAt:    now,
			LastSeenAt:    &now,
			IncarnationID: inc,
		})
		require.NoError(t, err)
		require.True(t, applied)

		reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		assert.False(t, reaped, "a reachable target must not reap")

		assertNotReaped(t, td, ds, label, stack, string(res.URI()))
	})

	// (b) reap_kind flipped to never / threshold raised out of reach.
	t.Run("PersistTargetReap_Guard_NeverOrThresholdRaised", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// never
		labelNever := "reap-guard-never"
		stackNever := "reap-guard-never-stack"
		incNever, cutoffNever := seedReapReadyTarget(t, ds, labelNever, 100)
		resNever := reapSuiteResource(util.NewID(), "bucket", stackNever, labelNever)
		_, err := ds.StoreResource(resNever, "cmd-create")
		require.NoError(t, err)
		_, err = ds.UpdateTarget(&pkgmodel.Target{
			Label:     labelNever,
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1"}`),
			Reaping:   json.RawMessage(`{"Kind":"never"}`),
		})
		require.NoError(t, err)

		reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            labelNever,
			IncarnationID:    incNever,
			LastSeenBefore:   cutoffNever,
			LastSampleBefore: cutoffNever,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		assert.False(t, reaped, "a reap_kind=never target must not reap")
		assertNotReaped(t, td, ds, labelNever, stackNever, string(resNever.URI()))

		// threshold raised beyond the accrued time
		labelHigh := "reap-guard-threshold"
		stackHigh := "reap-guard-threshold-stack"
		incHigh, cutoffHigh := seedReapReadyTarget(t, ds, labelHigh, 100)
		resHigh := reapSuiteResource(util.NewID(), "bucket", stackHigh, labelHigh)
		_, err = ds.StoreResource(resHigh, "cmd-create")
		require.NoError(t, err)
		_, err = ds.UpdateTarget(&pkgmodel.Target{
			Label:     labelHigh,
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1"}`),
			Reaping:   json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":100000}`),
		})
		require.NoError(t, err)

		reaped, _, err = ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            labelHigh,
			IncarnationID:    incHigh,
			LastSeenBefore:   cutoffHigh,
			LastSampleBefore: cutoffHigh,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		assert.False(t, reaped, "a target below its (raised) threshold must not reap")
		assertNotReaped(t, td, ds, labelHigh, stackHigh, string(resHigh.URI()))
	})

	// (c) An active incomplete command references the target label.
	t.Run("PersistTargetReap_Guard_ActiveCommand", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		label := "reap-guard-active"
		stack := "reap-guard-active-stack"
		inc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		res := reapSuiteResource(util.NewID(), "bucket", stack, label)
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		// An incomplete forma_command whose resource_update targets this label.
		storeActiveCommandTouchingTarget(t, ds, "active-cmd-1", stack, label)

		reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		assert.False(t, reaped, "a target touched by an active command must not reap")
		assertNotReaped(t, td, ds, label, stack, string(res.URI()))
	})

	// (d) Stale reaper: the target was reaped, then recovered to a fresh
	// incarnation; a reap carrying the OLD incarnation reaps nothing and leaves
	// the new incarnation's resources untouched.
	t.Run("PersistTargetReap_Guard_StaleReaper", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawResourceOperationForTest == nil {
			t.Skip("backend does not expose RawResourceOperationForTest")
		}

		label := "reap-guard-stale"
		stack := "reap-guard-stale-stack"
		oldInc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		oldRes := reapSuiteResource(util.NewID(), "bucket-old", stack, label)
		_, err := ds.StoreResource(oldRes, "cmd-create-old")
		require.NoError(t, err)

		// First reap succeeds.
		reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    oldInc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		require.True(t, reaped)

		// Recover: re-declaring the reaped target mints a fresh incarnation.
		_, err = ds.UpdateTarget(&pkgmodel.Target{
			Label:     label,
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1"}`),
			Reaping:   json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":100}`),
		})
		require.NoError(t, err)
		recovered, err := ds.LoadTarget(label)
		require.NoError(t, err)
		require.NotNil(t, recovered.Health)
		newInc := recovered.Health.IncarnationID
		require.NotEqual(t, oldInc, newInc, "recovery must mint a fresh incarnation")

		// Drive the recovered target reap-ready again (fresh incarnation).
		seenAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		observedAt := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
		applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
			TargetLabel:   label,
			State:         pkgmodel.TargetHealthStateUnreachable,
			ObservedAt:    observedAt,
			LastSeenAt:    &seenAt,
			IncarnationID: newInc,
		})
		require.NoError(t, err)
		require.True(t, applied)
		sampleAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		applied, err = ds.AdvanceTargetAccrual(label, newInc, sampleAt, 100)
		require.NoError(t, err)
		require.True(t, applied)

		// A live resource on the fresh incarnation. Recovery already un-reaped the
		// original resource and re-stamped it with the fresh incarnation, so the
		// stack now holds two live resources on that incarnation.
		newRes := reapSuiteResource(util.NewID(), "bucket-new", stack, label)
		_, err = ds.StoreResource(newRes, "cmd-create-new", newInc)
		require.NoError(t, err)

		// A stale reaper carrying the OLD incarnation reaps nothing.
		reaped, _, err = ds.PersistTargetReap(datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    oldInc,
			LastSeenBefore:   time.Now().UTC(),
			LastSampleBefore: time.Now().UTC(),
			ReapedAt:         time.Now().UTC(),
		})
		require.NoError(t, err)
		assert.False(t, reaped, "a stale-incarnation reap must be a no-op")

		// The fresh incarnation's resource is untouched (still live).
		op, err := td.RawResourceOperationForTest(string(newRes.URI()))
		require.NoError(t, err)
		assert.NotEqual(t, string(resource_update.OperationReaped), op,
			"the new incarnation's resource must not be tombstoned by a stale reap")
		live, err := ds.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, live, 2, "the recovered and the fresh-incarnation resource must both remain live")

		// Still exactly one audit row (from the first, real reap).
		if td.CountReapAuditRowsForTest != nil {
			count, cErr := td.CountReapAuditRowsForTest(label)
			require.NoError(t, cErr)
			assert.Equal(t, 1, count, "a stale reap must not add an audit row")
		}
	})

	// (e) Double call: a second reap of an already-reaped incarnation is a no-op
	// and does not add a second audit row.
	t.Run("PersistTargetReap_Guard_DoubleCall", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		label := "reap-guard-double"
		stack := "reap-guard-double-stack"
		inc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		res := reapSuiteResource(util.NewID(), "bucket", stack, label)
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		req := datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		}
		reaped, _, err := ds.PersistTargetReap(req)
		require.NoError(t, err)
		require.True(t, reaped, "first reap must succeed")

		reaped, _, err = ds.PersistTargetReap(req)
		require.NoError(t, err)
		assert.False(t, reaped, "a second reap of the same incarnation must be a no-op")

		if td.CountReapAuditRowsForTest != nil {
			count, cErr := td.CountReapAuditRowsForTest(label)
			require.NoError(t, cErr)
			assert.Equal(t, 1, count, "a double reap must leave exactly one audit row")
		}
	})
}

// RunPersistTargetReapConcurrent (S3) fires two reaps at one incarnation and
// asserts exactly one wins (the conditional UPDATE is the CAS), with no
// duplicate tombstones or audit rows.
func RunPersistTargetReapConcurrent(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("PersistTargetReap_Concurrent", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		label := "reap-concurrent-target"
		stack := "reap-concurrent-stack"
		inc, cutoff := seedReapReadyTarget(t, ds, label, 100)

		res := reapSuiteResource(util.NewID(), "bucket", stack, label)
		_, err := ds.StoreResource(res, "cmd-create")
		require.NoError(t, err)

		req := datastore.PersistTargetReapRequest{
			Label:            label,
			IncarnationID:    inc,
			LastSeenBefore:   cutoff,
			LastSampleBefore: cutoff,
			ReapedAt:         time.Now().UTC(),
		}

		var wg sync.WaitGroup
		results := make([]bool, 2)
		errs := make([]error, 2)
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func(idx int) {
				defer wg.Done()
				results[idx], _, errs[idx] = ds.PersistTargetReap(req)
			}(i)
		}
		wg.Wait()

		for i := 0; i < 2; i++ {
			require.NoError(t, errs[i])
		}
		wins := 0
		for _, r := range results {
			if r {
				wins++
			}
		}
		assert.Equal(t, 1, wins, "exactly one concurrent reap must win the CAS")

		if td.CountReapAuditRowsForTest != nil {
			count, cErr := td.CountReapAuditRowsForTest(label)
			require.NoError(t, cErr)
			assert.Equal(t, 1, count, "concurrent reaps must write exactly one audit row")
		}
	})
}

// assertNotReaped asserts a target and its resource were left untouched by a
// rejected reap: the target is not in the reaped state, the resource is still
// live, and no audit row was written.
func assertNotReaped(t *testing.T, td TestDatastore, ds datastore.Datastore, label, stack, uri string) {
	t.Helper()

	loaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, loaded.Health)
	assert.NotEqual(t, pkgmodel.TargetHealthStateReaped, loaded.Health.State,
		"a rejected reap must not transition the target")

	live, err := ds.LoadResourcesByStack(stack)
	require.NoError(t, err)
	assert.Len(t, live, 1, "a rejected reap must leave resources live")

	if td.RawResourceOperationForTest != nil {
		op, opErr := td.RawResourceOperationForTest(uri)
		require.NoError(t, opErr)
		assert.NotEqual(t, string(resource_update.OperationReaped), op,
			"a rejected reap must not tombstone resources")
	}

	if td.CountReapAuditRowsForTest != nil {
		count, cErr := td.CountReapAuditRowsForTest(label)
		require.NoError(t, cErr)
		assert.Equal(t, 0, count, "a rejected reap must not write an audit row")
	}
}

// storeActiveCommandTouchingTarget persists an incomplete (NotStarted)
// forma_command whose single resource_update targets the given label, so the
// active-command assertion in PersistTargetReap sees a live reference.
func storeActiveCommandTouchingTarget(t *testing.T, ds datastore.Datastore, commandID, stack, target string) {
	t.Helper()

	res := reapSuiteResource(util.NewID(), "active-bucket", stack, target)
	fc := &forma_command.FormaCommand{
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateNotStarted,
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState: *res,
				Operation:    resource_update.OperationCreate,
				State:        resource_update.ResourceUpdateStateNotStarted,
				StackLabel:   stack,
			},
		},
	}
	require.NoError(t, ds.StoreFormaCommand(fc, commandID))
}
