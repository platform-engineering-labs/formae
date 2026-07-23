// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/logging"
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

// defaultSyncInterval mirrors the schema default (Config.pkl
// SynchronizationConfig.interval = 5.min).
const defaultSyncInterval = 5 * time.Minute

// testMaxBeatGap is the MaxBeatGap derived from defaultSyncInterval, used
// throughout this file wherever a test needs the reaper's actual clock-stop
// threshold rather than the raw derivation function.
var testMaxBeatGap = config.DeriveMaxBeatGap(defaultSyncInterval)

func TestPlanAccrual_SeedsNullLastSampleAt(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	newUnreachableTestTarget(t, ds, "seed-test", 3600, observedAt)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt), testMaxBeatGap)
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

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(t2), testMaxBeatGap)
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
	t2 := t1.Add(testMaxBeatGap + time.Minute)
	_, err = ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   "clock-stop-test",
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    t2,
		LastErrorCode: "NetworkFailure",
	})
	require.NoError(t, err)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(t2), testMaxBeatGap)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	plan := plans[0]
	assert.Equal(t, int64(0), plan.DeltaSeconds, "a gap larger than MaxBeatGap must reset delta to 0")
	assert.True(t, plan.LastSampleAt.Equal(t2), "last_sample_at must still advance despite the clock-stop")
	assert.Equal(t, int64(0), plan.NewAccumSeconds, "accrual must not advance across the clock-stop")
}

// TestAccrualStep_SyncIntervalGapDoesNotTripClockStop is the regression guard
// for the calibration bug that made reaping inert: an unreachable target's
// observed_at is refreshed only about once per synchronization cycle, so the
// reaper's per-step gap is one sync interval wide. Because MaxBeatGap is now
// derived from the sync interval itself (config.DeriveMaxBeatGap), a
// sync-interval-sized gap can never sit at or above it — this holds by
// construction, not by calibration, for any sync interval. This test pins the
// equivalent assertion for the default interval.
func TestAccrualStep_SyncIntervalGapDoesNotTripClockStop(t *testing.T) {
	require.Greater(t, testMaxBeatGap, defaultSyncInterval,
		"a sync-interval-sized gap must be smaller than the derived MaxBeatGap, else reaping is inert")

	lastSampleAt := time.Now().UTC().Truncate(time.Second)
	observedAt := lastSampleAt.Add(defaultSyncInterval)
	health := &pkgmodel.TargetHealth{
		ObservedAt:              &observedAt,
		LastSampleAt:            &lastSampleAt,
		UnreachableAccumSeconds: 0,
	}

	delta, newLastSampleAt := accrualStep(health, testMaxBeatGap)

	assert.Equal(t, int64(defaultSyncInterval.Seconds()), delta,
		"a sync-interval-sized gap must accrue the full delta, not be zeroed by the clock-stop")
	assert.True(t, newLastSampleAt.Equal(observedAt), "last_sample_at must advance to observed_at")
}

// TestDeriveMaxBeatGap_ScalesWithConfiguredSyncInterval verifies MaxBeatGap
// scales with whatever synchronization interval the reaper is actually
// configured with — not a fixed constant — and that the derivation floor
// protects a very fast interval from collapsing MaxBeatGap to something a
// single delayed heartbeat could trip.
func TestDeriveMaxBeatGap_ScalesWithConfiguredSyncInterval(t *testing.T) {
	longInterval := 3 * time.Hour
	got := config.DeriveMaxBeatGap(longInterval)
	assert.Equal(t, 18*time.Hour, got, "MaxBeatGap must scale with the configured sync interval (k=6)")
	assert.Greater(t, got, longInterval, "a scaled-up sync interval must still sit below the derived MaxBeatGap")

	tinyInterval := 10 * time.Second
	got = config.DeriveMaxBeatGap(tinyInterval)
	assert.Equal(t, config.MaxBeatGapFloor, got, "a very fast sync interval must be floored, not collapse MaxBeatGap")
}

func TestReapCandidates_DetectsCandidateWithoutBlockingCommand(t *testing.T) {
	ds := newReaperTestDatastore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	// MaxUnreachableSeconds=0 so any tick's accrual (even the seed) already qualifies.
	newUnreachableTestTarget(t, ds, "candidate-test", 0, observedAt)

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt), testMaxBeatGap)
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
		forma_command.SourceUser,
	)
	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt), testMaxBeatGap)
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

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt), testMaxBeatGap)
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

	plans, err := planAccrualForUnreachableTargets(ds, fixedClock(observedAt), testMaxBeatGap)
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

// TestPlanReapExecution_CapsCandidatesAndLogsDeferred is the rate-cap unit
// test: a tick with more candidates than the configured cap must only reap up
// to the cap, and the remainder must be logged (not silently dropped) so an
// operator can see what was deferred to the next tick.
func TestPlanReapExecution_CapsCandidatesAndLogsDeferred(t *testing.T) {
	capture := logging.NewTestLogCaptureQuiet()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	candidates := []ReapCandidate{
		{TargetLabel: "target-a", IncarnationID: "inc-a", UnreachableAccumSeconds: 100},
		{TargetLabel: "target-b", IncarnationID: "inc-b", UnreachableAccumSeconds: 200},
		{TargetLabel: "target-c", IncarnationID: "inc-c", UnreachableAccumSeconds: 300},
	}

	plan := planReapExecution(candidates, 2)

	require.Len(t, plan.ToReap, 2, "only the cap's worth of candidates may be reaped this tick")
	assert.Equal(t, "target-a", plan.ToReap[0].TargetLabel)
	assert.Equal(t, "target-b", plan.ToReap[1].TargetLabel)

	require.Len(t, plan.Deferred, 1, "the remainder must be deferred, not dropped")
	assert.Equal(t, "target-c", plan.Deferred[0].TargetLabel)

	assert.True(t, capture.ContainsAll("target-c"),
		"the deferred candidate must be logged so nothing is silently dropped, got: %v", capture.GetEntries())
	assert.True(t, capture.ContainsAll("rate cap"),
		"the log must explain why the candidate was deferred, got: %v", capture.GetEntries())
}

// TestPlanReapExecution_UnboundedWhenCapNotPositive verifies the documented
// fallback: a MaxReapsPerTick of zero or negative means unbounded, so every
// candidate is reaped this tick (e.g. when the value was never configured).
func TestPlanReapExecution_UnboundedWhenCapNotPositive(t *testing.T) {
	candidates := []ReapCandidate{
		{TargetLabel: "target-a"},
		{TargetLabel: "target-b"},
	}

	for _, maxPerTick := range []int{0, -1} {
		plan := planReapExecution(candidates, maxPerTick)
		assert.Len(t, plan.ToReap, 2, "maxPerTick=%d must mean unbounded", maxPerTick)
		assert.Empty(t, plan.Deferred, "maxPerTick=%d must defer nothing", maxPerTick)
	}
}

// TestPlanReapExecution_UnderCapReapsAllAndDefersNone verifies the cap is a
// ceiling, not a floor or a fixed batch size: fewer candidates than the cap
// are all reaped, deferring nothing.
func TestPlanReapExecution_UnderCapReapsAllAndDefersNone(t *testing.T) {
	candidates := []ReapCandidate{{TargetLabel: "target-a"}}
	plan := planReapExecution(candidates, 5)
	assert.Len(t, plan.ToReap, 1)
	assert.Empty(t, plan.Deferred)
}

// TestTargetReapStatus_PendingBeforeTombstoneThenReapedAfter is the
// reap-pending visibility unit test: an unreachable target that has already
// crossed its own reap-after threshold must surface as "reap-pending" while
// its persisted health_state is still 'unreachable' — i.e. before any
// tombstone — and only becomes "reaped" once PersistTargetReap actually
// commits.
func TestTargetReapStatus_PendingBeforeTombstoneThenReapedAfter(t *testing.T) {
	ds := newReaperTestDatastore(t)
	label := "reap-status-test"

	reaping, err := pkgmodel.MarshalReaping(&pkgmodel.ReapAfter{Kind: "after", MaxUnreachableSeconds: 0})
	require.NoError(t, err)
	_, err = ds.CreateTarget(&pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
		Reaping:   reaping,
	})
	require.NoError(t, err)

	seenAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	observedAt := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel: label,
		State:       pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:  observedAt,
		LastSeenAt:  &seenAt,
	})
	require.NoError(t, err)
	require.True(t, applied)

	target, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, target.Health)

	// Seed last_sample_at (a prior tick's accrual advance would normally do
	// this); a zero delta is enough since the threshold is already 0.
	sampleAt := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Second)
	applied, err = ds.AdvanceTargetAccrual(label, target.Health.IncarnationID, sampleAt, 0)
	require.NoError(t, err)
	require.True(t, applied)

	target, err = ds.LoadTarget(label)
	require.NoError(t, err)

	status, err := TargetReapStatus(target)
	require.NoError(t, err)
	assert.Equal(t, pkgmodel.TargetHealthStateReapPending, status,
		"an over-threshold unreachable target must surface as reap-pending before any tombstone")

	cutoff := time.Now().UTC()
	reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
		Label:            label,
		IncarnationID:    target.Health.IncarnationID,
		LastSeenBefore:   cutoff,
		LastSampleBefore: cutoff,
		ReapedAt:         cutoff,
	})
	require.NoError(t, err)
	require.True(t, reaped)

	target, err = ds.LoadTarget(label)
	require.NoError(t, err)

	status, err = TargetReapStatus(target)
	require.NoError(t, err)
	assert.Equal(t, pkgmodel.TargetHealthStateReaped, status, "once reaped, the status must flip to reaped")
}
