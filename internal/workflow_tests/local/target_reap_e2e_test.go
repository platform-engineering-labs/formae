// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestTargetReap_FullLifecycleAndRecovery drives the complete reap lifecycle
// end to end through the real actor system, asserting every stage in
// sequence:
//
//  1. A target with a resource is healthy (reachable, zero accrual).
//  2. It goes unreachable and accrues past its own reap-after threshold
//     (seeded deterministically — see seedOverThresholdUnreachableTarget).
//  3. The real reaper (driven via ForceReap, not a direct PersistTargetReap
//     call) reaps it: the target transitions to 'reaped', its resource is
//     tombstoned (invisible to the live view), and an audit row exists.
//  4. Neither an explicit sync cycle nor an apply that touches the target
//     without re-declaring it can resurrect the resource.
//  5. Re-applying the SAME forma (which re-declares the target) is admitted,
//     mints a fresh incarnation, re-adopts the resource under it, and the
//     target ends up reachable again with its accrual reset to zero.
func TestTargetReap_FullLifecycleAndRecovery(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var createCount atomic.Int32

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				createCount.Add(1)
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        "native-" + request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"v1"}`,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationUpdate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        request.NativeID,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		// The stages are driven explicitly (ForceReap / ForceSync); periodic
		// background sync would just add nondeterministic noise.
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "lifecycle-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "svc", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "lifecycle-stack", Target: "lifecycle-target"},
			},
			Targets: []pkgmodel.Target{{Label: "lifecycle-target"}},
		}

		// Stage 1: initial apply creates the resource; the target is healthy.
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("lifecycle-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")
		r.Equal(int32(1), createCount.Load(), "exactly one Create from initial apply")

		healthy, err := m.Datastore.LoadTarget("lifecycle-target")
		r.NoError(err)
		r.NotNil(healthy.Health)
		oldIncarnation := healthy.Health.IncarnationID
		r.NotEmpty(oldIncarnation)
		r.Equal(pkgmodel.TargetHealthStateReachable, healthy.Health.State, "the target must be reachable right after a successful create")
		r.Equal(int64(0), healthy.Health.UnreachableAccumSeconds, "a never-unreachable target must carry zero accrual")

		// Stage 2: the target goes unreachable and accrues past its own
		// reap-after threshold (deterministic seeding — no real wall-clock wait).
		seedOverThresholdUnreachableTarget(t, m.Datastore, "lifecycle-target")

		accrued, err := m.Datastore.LoadTarget("lifecycle-target")
		r.NoError(err)
		r.Equal(pkgmodel.TargetHealthStateUnreachable, accrued.Health.State)
		r.Equal(oldIncarnation, accrued.Health.IncarnationID, "accrual must not touch the incarnation")
		r.Greater(accrued.Health.UnreachableAccumSeconds, int64(0), "accrual must have advanced past the threshold")

		// Stage 3: the real reaper (via ForceReap) reaps it.
		r.NoError(m.ForceReap())
		r.Eventually(func() bool {
			target, err := m.Datastore.LoadTarget("lifecycle-target")
			return err == nil && target != nil && target.Health != nil &&
				target.Health.State == pkgmodel.TargetHealthStateReaped
		}, 10*time.Second, 100*time.Millisecond, "ForceReap must actually reap the over-threshold target")

		resources, err := m.Datastore.LoadResourcesByStack("lifecycle-stack")
		r.NoError(err)
		r.Empty(resources, "the reaped target's resource must be invisible to the live view")

		reapedRows, err := m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Len(reapedRows, 1, "the reap must leave an audit-visible tombstone row")

		// Stage 4a: an explicit sync cycle must not resurrect it.
		createCount.Store(0)
		r.NoError(m.ForceSync())
		time.Sleep(3 * time.Second)
		r.Equal(int32(0), createCount.Load(), "sync must not resurrect a reaped target's resources")
		resources, err = m.Datastore.LoadResourcesByStack("lifecycle-stack")
		r.NoError(err)
		r.Empty(resources, "the resource must still be absent after a sync cycle")

		// Stage 4b: an apply that touches the reaped target without
		// re-declaring it is rejected outright (not silently ignored).
		resourceOnly := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "lifecycle-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "svc", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "lifecycle-stack", Target: "lifecycle-target"},
			},
		}
		_, err = m.ApplyForma(resourceOnly, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "rejected-client")
		r.Error(err, "an apply touching a reaped target without re-declaring it must be rejected")
		var reapedErr apimodel.TargetReapedError
		r.True(errors.As(err, &reapedErr), "error must be a TargetReapedError, got %T: %v", err, err)

		// Stage 5: re-applying the SAME forma re-declares the target and
		// recovers it: fresh incarnation, resource re-adopted, reachable again,
		// accrual reset to zero.
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err, "recovery re-apply must be admitted")

		r.Eventually(func() bool {
			target, err := m.Datastore.LoadTarget("lifecycle-target")
			if err != nil || target == nil || target.Health == nil {
				return false
			}
			return target.Health.State == pkgmodel.TargetHealthStateReachable &&
				target.Health.IncarnationID != oldIncarnation &&
				target.Health.UnreachableAccumSeconds == 0
		}, 15*time.Second, 200*time.Millisecond, "recovery must mint a fresh incarnation, clear reaped, become reachable again, and reset accrual")

		resources, err = m.Datastore.LoadResourcesByStack("lifecycle-stack")
		r.NoError(err)
		r.Len(resources, 1, "the resource must be re-adopted (live again) under the fresh incarnation")

		reapedRows, err = m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Empty(reapedRows, "no reaped tombstone should remain after recovery")

		recovered, err := m.Datastore.LoadTarget("lifecycle-target")
		r.NoError(err)
		r.NotEqual(oldIncarnation, recovered.Health.IncarnationID)
		r.Equal(pkgmodel.TargetHealthStateReachable, recovered.Health.State)
		r.Equal(int64(0), recovered.Health.UnreachableAccumSeconds)
	})
}

// TestTargetReap_SyncRaceDoesNotResurrect is the sync-race integration test:
// it holds a sync Read in flight on a reap candidate, fires ForceReap while
// that Read is blocked, and proves two things:
//
//  1. The reap proceeds anyway — the reaper never blocks on (or defers to)
//     an in-flight sync. Both the reaper's own front-door check
//     (targetHasIncompleteCommand, which treats Read operations as
//     non-blocking) and the datastore's PersistTargetReap CAS (whose
//     active-command assertion explicitly excludes the 'sync' command)
//     allow the reap to commit while the sync command is still open.
//  2. When the blocked Read finally completes and its result tries to land,
//     the write is rejected by the resource-write reaped/incarnation guard —
//     the resource is NOT resurrected.
//
// This is a real race in the sense that the reap and the in-flight Read
// genuinely overlap (the Read is blocked on a channel until after the reap
// has been asserted committed); it does not, however, prove anything about
// timing windows narrower than "an entire goroutine scheduling quantum" — see
// the task report for what this test does and does not establish.
func TestTargetReap_SyncRaceDoesNotResurrect(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		readStarted := make(chan struct{})
		readCanComplete := make(chan struct{})

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        "native-" + request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				select {
				case readStarted <- struct{}{}:
				default:
				}
				<-readCanComplete
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"v1"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		// Background sync stays off; the race is driven explicitly via ForceSync.
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "sync-race-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "raced", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "sync-race-stack", Target: "sync-race-target"},
			},
			Targets: []pkgmodel.Target{{Label: "sync-race-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("sync-race-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		// Push the target over its reap-after threshold, but stop short of
		// reaping it directly — the real reaper (ForceReap) must do that.
		seedOverThresholdUnreachableTarget(t, m.Datastore, "sync-race-target")

		// Kick off a sync cycle; it reads the (still-live, over-threshold)
		// resource and blocks mid-Read.
		go func() { _ = m.ForceSync() }()
		<-readStarted

		// Fire the reap while the sync Read is still in flight. It must
		// proceed and commit despite the open sync command.
		r.NoError(m.ForceReap())
		r.Eventually(func() bool {
			target, err := m.Datastore.LoadTarget("sync-race-target")
			return err == nil && target != nil && target.Health != nil &&
				target.Health.State == pkgmodel.TargetHealthStateReaped
		}, 10*time.Second, 100*time.Millisecond, "the reap must commit even while a sync read is in flight against the target")

		reapedRows, err := m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Len(reapedRows, 1, "the resource must be tombstoned by the reap, in-flight sync read notwithstanding")

		// Release the blocked read. Its result now tries to land against an
		// already-reaped resource row.
		close(readCanComplete)

		// Give the late write every chance to (wrongly) resurrect the resource.
		time.Sleep(3 * time.Second)

		resources, err := m.Datastore.LoadResourcesByStack("sync-race-stack")
		r.NoError(err)
		r.Empty(resources, "the sync read's late write must be rejected by the resource-write guard, not resurrect the resource")

		reapedRows, err = m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Len(reapedRows, 1, "the reaped tombstone must remain intact after the late write is rejected")
	})
}
