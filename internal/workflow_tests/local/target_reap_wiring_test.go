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

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// seedOverThresholdUnreachableTarget drives targetLabel to just past its own
// configured reap-after threshold using only the public Datastore API, but
// deliberately stops short of the final PersistTargetReap CAS: the tombstone
// itself must come from the real TargetReaper being exercised (via
// ForceReap), not directly from the test.
//
// Unlike reapTargetForTest (target_reap_test.go), which stamps its synthetic
// observation comfortably ahead of "now" because it drives PersistTargetReap
// itself with a matching far-future cutoff, this helper feeds a REAL
// TargetReaper tick, whose own cutoff is the actual wall-clock "now" at tick
// time. So observed_at/last_sample_at here must stay at-or-before real "now"
// at every subsequent instant, while still being strictly after whatever real
// "reachable" observation the initial apply may have already stamped (every
// successful plugin operation emits one) — plain time.Now() calls, made
// after that apply has already completed, satisfy both.
func seedOverThresholdUnreachableTarget(t *testing.T, ds datastore.Datastore, label string) {
	t.Helper()

	loaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, loaded, "target %s must exist", label)
	require.NotNil(t, loaded.Health, "target %s must carry health", label)
	inc := loaded.Health.IncarnationID
	require.NotEmpty(t, inc)

	behaviour, err := pkgmodel.ParseReaping(loaded.Reaping)
	require.NoError(t, err)
	after, ok := pkgmodel.ResolveReaping(behaviour, nil).(*pkgmodel.ReapAfter)
	require.True(t, ok, "target %s must resolve to a reap-after behaviour for this test", label)

	seenAt := time.Now().UTC()
	observedAt := time.Now().UTC()
	applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   label,
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    observedAt,
		LastSeenAt:    &seenAt,
		IncarnationID: inc,
	})
	require.NoError(t, err)
	require.True(t, applied, "unreachable observation must apply for target %s", label)

	sampleAt := time.Now().UTC()
	applied, err = ds.AdvanceTargetAccrual(label, inc, sampleAt, after.MaxUnreachableSeconds)
	require.NoError(t, err)
	require.True(t, applied, "accrual advance must apply for target %s", label)
}

// TestTargetReaper_DryRun_TombstonesNothing verifies the dry-run/audit-only
// safety valve: with DryRun enabled, an over-threshold candidate is detected
// (the reaper's tick runs to completion without error) but PersistTargetReap
// is never actually called — the target's health state and its resources
// must be completely unchanged.
func TestTargetReaper_DryRun_TombstonesNothing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
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
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		cfg.Agent.TargetReaping.DryRun = true
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "dry-run-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "res1", Type: "FakeAWS::Resource", Properties: json.RawMessage(`{"foo":"v1"}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}}, Stack: "dry-run-stack", Target: "dry-run-target"},
			},
			Targets: []pkgmodel.Target{{Label: "dry-run-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("dry-run-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		seedOverThresholdUnreachableTarget(t, m.Datastore, "dry-run-target")

		r.NoError(m.ForceReap())

		// Give the reaper every chance to (wrongly) reap for real.
		time.Sleep(3 * time.Second)

		target, err := m.Datastore.LoadTarget("dry-run-target")
		r.NoError(err)
		r.NotNil(target.Health)
		r.Equal(pkgmodel.TargetHealthStateUnreachable, target.Health.State,
			"dry-run must never tombstone the target, even when it is an over-threshold candidate")

		resources, err := m.Datastore.LoadResourcesByStack("dry-run-stack")
		r.NoError(err)
		r.Len(resources, 1, "dry-run must never tombstone the target's resources")
	})
}

// TestTargetReaper_ForceReap_PostReapNoResurrection is the post-real-reap
// no-resurrection end-to-end test: drives an actual reap through the wired
// reaper (ForceReap, not a direct PersistTargetReap call), then proves both a
// user apply and an auto-reconcile beat still fail to resurrect the reaped
// target's resources — i.e. the no-resurrection guards hold when the
// reap happens via the real reaper path, not just the direct-datastore-call
// path already covered elsewhere.
func TestTargetReaper_ForceReap_PostReapNoResurrection(t *testing.T) {
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
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		stack := pkgmodel.Stack{
			Label: "force-reap-stack",
			Policies: []json.RawMessage{
				json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":2}`),
			},
		}
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{stack},
			Resources: []pkgmodel.Resource{
				{Label: "doomed", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "force-reap-stack", Target: "force-reap-target"},
			},
			Targets: []pkgmodel.Target{{Label: "force-reap-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("force-reap-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")
		r.Equal(int32(1), createCount.Load(), "exactly one Create from initial apply")

		seedOverThresholdUnreachableTarget(t, m.Datastore, "force-reap-target")

		r.NoError(m.ForceReap())

		r.Eventually(func() bool {
			target, err := m.Datastore.LoadTarget("force-reap-target")
			return err == nil && target != nil && target.Health != nil &&
				target.Health.State == pkgmodel.TargetHealthStateReaped
		}, 10*time.Second, 100*time.Millisecond, "ForceReap must actually reap the over-threshold target")

		resources, err := m.Datastore.LoadResourcesByStack("force-reap-stack")
		r.NoError(err)
		r.Empty(resources, "the really-reaped target's resources must be invisible to the live view")

		// (a) A user apply that touches the reaped target without re-declaring
		// it must be rejected by the command-admission reaped-check.
		resourceOnly := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "force-reap-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "doomed", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "force-reap-stack", Target: "force-reap-target"},
			},
		}
		_, err = m.ApplyForma(resourceOnly, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "rejected-client")
		r.Error(err, "an apply touching a really-reaped target without re-declaring it must be rejected")
		var reapedErr apimodel.TargetReapedError
		r.True(errors.As(err, &reapedErr), "error must be a TargetReapedError, got %T: %v", err, err)

		// (b) Auto-reconcile must not resurrect it either; reconcile skips reaped targets.
		createCount.Store(0)
		time.Sleep(8 * time.Second)
		r.Equal(int32(0), createCount.Load(),
			"auto-reconcile must not resurrect a really-reaped target's resources (got %d unexpected Creates)",
			createCount.Load())

		resources, err = m.Datastore.LoadResourcesByStack("force-reap-stack")
		r.NoError(err)
		r.Empty(resources, "stack should remain empty after auto-reconcile beats")
	})
}
