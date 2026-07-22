// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func newReaperTestDatastore(t *testing.T) datastore.Datastore {
	t.Helper()

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite: pkgmodel.SqliteConfig{
			FilePath: ":memory:",
		},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	t.Cleanup(ds.Close)
	return ds
}

// newUnreachableTestTarget creates and persists a target, then drives it to
// health_state='unreachable' at observedAt with the given reap-after
// threshold. Returns the target's incarnation id.
func newUnreachableTestTarget(t *testing.T, ds datastore.Datastore, label string, maxUnreachableSeconds int64, observedAt time.Time) string {
	t.Helper()

	reaping, err := pkgmodel.MarshalReaping(&pkgmodel.ReapAfter{Kind: "after", MaxUnreachableSeconds: maxUnreachableSeconds})
	require.NoError(t, err)

	target := &pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		Reaping:   reaping,
	}
	_, err = ds.CreateTarget(target)
	require.NoError(t, err)

	_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   label,
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    observedAt,
		LastErrorCode: "NetworkFailure",
	})
	require.NoError(t, err)

	loaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, loaded.Health)
	return loaded.Health.IncarnationID
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestPlanAccrual_SeedsNullLastSampleAt(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	newUnreachableTestTarget(t, ds, "seed-test", 3600, observedAt)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt))
	require.NoError(t, err)
	require.Len(t, plans, 1)

	plan := plans[0]
	assert.Equal(t, "seed-test", plan.Target.Label)
	assert.Equal(t, int64(0), plan.DeltaSeconds, "a NULL last_sample_at must seed with zero accrual")
	assert.True(t, plan.LastSampleAt.Equal(observedAt), "last_sample_at must be seeded to observed_at")
	assert.Equal(t, int64(0), plan.NewAccumSeconds)
}

func TestPlanAccrual_NormalAccrualWithinBeatGap(t *testing.T) {
	ds := newReaperTestDatastore(t)
	t1 := time.Now().UTC().Truncate(time.Second)
	incarnationID := newUnreachableTestTarget(t, ds, "normal-accrual-test", 3600, t1)

	// Seed last_sample_at (simulating a prior tick) directly via the
	// datastore write path — this is the persistence layer under test in
	// suite_advance_target_accrual.go, not the reaper logic under test here.
	applied, err := ds.AdvanceTargetAccrual("normal-accrual-test", incarnationID, t1, 0)
	require.NoError(t, err)
	require.True(t, applied)

	// A new observation arrives 30s later — well within MaxBeatGap.
	t2 := t1.Add(30 * time.Second)
	_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   "normal-accrual-test",
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    t2,
		LastErrorCode: "NetworkFailure",
	})
	require.NoError(t, err)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(t2))
	require.NoError(t, err)
	require.Len(t, plans, 1)

	plan := plans[0]
	assert.Equal(t, int64(30), plan.DeltaSeconds, "delta must be observed_at - last_sample_at, not tied to any nominal interval")
	assert.True(t, plan.LastSampleAt.Equal(t2))
	assert.Equal(t, int64(30), plan.NewAccumSeconds)
}

func TestPlanAccrual_ClockStopBeyondMaxBeatGap(t *testing.T) {
	ds := newReaperTestDatastore(t)
	t1 := time.Now().UTC().Truncate(time.Second)
	incarnationID := newUnreachableTestTarget(t, ds, "clock-stop-test", 3600, t1)

	applied, err := ds.AdvanceTargetAccrual("clock-stop-test", incarnationID, t1, 0)
	require.NoError(t, err)
	require.True(t, applied)

	// A gap far larger than MaxBeatGap — the reaper must not have run for a while.
	t2 := t1.Add(config.MaxBeatGap + time.Minute)
	_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   "clock-stop-test",
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    t2,
		LastErrorCode: "NetworkFailure",
	})
	require.NoError(t, err)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(t2))
	require.NoError(t, err)
	require.Len(t, plans, 1)

	plan := plans[0]
	assert.Equal(t, int64(0), plan.DeltaSeconds, "a gap larger than MaxBeatGap must reset delta to 0")
	assert.True(t, plan.LastSampleAt.Equal(t2), "last_sample_at must still advance despite the clock-stop")
	assert.Equal(t, int64(0), plan.NewAccumSeconds, "accrual must not advance across the clock-stop")
}

func TestReapCandidates_DetectsCandidateWithoutBlockingCommand(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	// MaxUnreachableSeconds=0 so any tick's accrual (even the seed) already qualifies.
	newUnreachableTestTarget(t, ds, "candidate-test", 0, observedAt)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt))
	require.NoError(t, err)
	require.Len(t, plans, 1)

	candidates := reapCandidatesFromPlans(ds, plans)
	require.Len(t, candidates, 1)
	assert.Equal(t, "candidate-test", candidates[0].TargetLabel)
}

func TestReapCandidates_SkipsTargetWithIncompleteCommand(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	newUnreachableTestTarget(t, ds, "blocked-candidate-test", 0, observedAt)

	// An incomplete command touching the target via a non-terminal resource update.
	cmd := forma_command.NewFormaCommand(
		&pkgmodel.Forma{},
		&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		pkgmodel.CommandApply,
		[]resource_update.ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{
					Label:  "some-resource",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "default",
					Ksuid:  util.NewID(),
					Target: "blocked-candidate-test",
				},
				ResourceTarget: pkgmodel.Target{Label: "blocked-candidate-test"},
				Operation:      resource_update.OperationUpdate,
				State:          resource_update.ResourceUpdateStateNotStarted,
				StackLabel:     "default",
			},
		},
		nil, nil, nil,
		"test-client",
	)
	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt))
	require.NoError(t, err)
	require.Len(t, plans, 1)

	candidates := reapCandidatesFromPlans(ds, plans)
	assert.Empty(t, candidates, "a target with an incomplete command touching it must be skipped")
}

func TestPlanAccrual_ReachableTargetsUntouched(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)

	reachable := &pkgmodel.Target{Label: "reachable-untouched-test", Namespace: "AWS", Config: json.RawMessage(`{}`)}
	_, err := ds.CreateTarget(reachable)
	require.NoError(t, err)
	_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel: "reachable-untouched-test",
		State:       pkgmodel.TargetHealthStateReachable,
		ObservedAt:  observedAt,
		LastSeenAt:  &observedAt,
	})
	require.NoError(t, err)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt))
	require.NoError(t, err)
	assert.Empty(t, plans, "reachable targets must never be planned for accrual")
}

// TestNoMutation_PlanAndCandidateDetectionAreReadOnly asserts the load-bearing
// constraint of Task 3: computing the accrual plan and evaluating reap
// candidates never mutates the datastore. Only the (separately tested)
// AdvanceTargetAccrual write — dispatched asynchronously by the actor via
// ResourcePersister — mutates state; this test exercises just the pure
// planning/detection path and confirms the target row is byte-for-byte
// unchanged (and definitely never reaped/tombstoned).
func TestNoMutation_PlanAndCandidateDetectionAreReadOnly(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	// Threshold of 0 guarantees candidacy, maximizing the chance a buggy
	// implementation would be tempted to act.
	newUnreachableTestTarget(t, ds, "no-mutation-test", 0, observedAt)

	before, err := ds.LoadTarget("no-mutation-test")
	require.NoError(t, err)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt))
	require.NoError(t, err)
	candidates := reapCandidatesFromPlans(ds, plans)
	require.Len(t, candidates, 1, "target must still be detected as a candidate")

	after, err := ds.LoadTarget("no-mutation-test")
	require.NoError(t, err)

	require.NotNil(t, before.Health)
	require.NotNil(t, after.Health)
	assert.Equal(t, before.Health.State, after.Health.State, "health state must not change")
	assert.NotEqual(t, pkgmodel.TargetHealthStateReaped, after.Health.State, "target must never be reaped by this task")
	assert.Equal(t, before.Health.UnreachableAccumSeconds, after.Health.UnreachableAccumSeconds,
		"unreachable_accum_seconds must not change — only the async AdvanceTargetAccrual write may mutate it")
	assert.Equal(t, before.Health.IncarnationID, after.Health.IncarnationID, "incarnation must not change")
	assert.Equal(t, before.Version, after.Version, "target version must not change")
}
