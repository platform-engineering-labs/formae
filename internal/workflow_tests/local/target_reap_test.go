// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// reapTargetForTest drives targetLabel through the full reap sequence using
// only the public Datastore interface: an unreachable health observation,
// accrual advanced to (at least) the target's own configured reap-after
// threshold, then the PersistTargetReap CAS. The TargetReaper isn't wired to
// actually reap yet (that's a later task), so tests reap directly, mirroring
// the datastore suite's seedReapReadyTarget/PersistTargetReap pattern.
func reapTargetForTest(t *testing.T, ds datastore.Datastore, label string) {
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

	// UpdateTargetHealth enforces a monotonic guard on observed_at, and the
	// target may already carry a real "reachable" observation stamped at
	// creation/first-use time (every successful plugin operation emits one).
	// Use offsets ahead of "now" — rather than the dstest suite's past
	// offsets, which assume a target nothing has touched yet — so this
	// synthetic observation always advances the clock forward.
	seenAt := time.Now().UTC().Add(1 * time.Hour).Truncate(time.Second)
	observedAt := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   label,
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    observedAt,
		LastSeenAt:    &seenAt,
		IncarnationID: inc,
	})
	require.NoError(t, err)
	require.True(t, applied, "unreachable observation must apply for target %s", label)

	sampleAt := time.Now().UTC().Add(45 * time.Minute).Truncate(time.Second)
	applied, err = ds.AdvanceTargetAccrual(label, inc, sampleAt, after.MaxUnreachableSeconds)
	require.NoError(t, err)
	require.True(t, applied, "accrual advance must apply for target %s", label)

	cutoff := time.Now().UTC().Add(2 * time.Hour)
	reaped, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
		Label:            label,
		IncarnationID:    inc,
		LastSeenBefore:   cutoff,
		LastSampleBefore: cutoff,
		ReapedAt:         time.Now().UTC(),
	})
	require.NoError(t, err)
	require.True(t, reaped, "target %s must reap", label)
}

// TestAutoReconciler_ReapedTargetNotResurrected verifies the
// no-resurrection guard: a
// reaped target's resource is invisible to every live-resource query, so
// auto-reconcile's stale baseline (GetResourcesAtLastReconcile, which is
// unaware of reaping) must not read that absence as "missing" and re-create
// the resource on its next beat.
func TestAutoReconciler_ReapedTargetNotResurrected(t *testing.T) {
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
		// Disable sync — the reap + auto-reconcile interaction is the only
		// thing under test.
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		stack := pkgmodel.Stack{
			Label: "reap-noresurrect-stack",
			Policies: []json.RawMessage{
				json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":2}`),
			},
		}
		targets := []pkgmodel.Target{{Label: "reap-target"}}

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{stack},
			Resources: []pkgmodel.Resource{
				{Label: "doomed", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "reap-noresurrect-stack", Target: "reap-target"},
			},
			Targets: targets,
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("reap-noresurrect-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")
		r.Equal(int32(1), createCount.Load(), "exactly one Create from initial apply")

		// Reap the target directly (the reaper isn't wired to reap yet).
		reapTargetForTest(t, m.Datastore, "reap-target")

		// Sanity: the resource is now invisible to the live view.
		resources, err := m.Datastore.LoadResourcesByStack("reap-noresurrect-stack")
		r.NoError(err)
		r.Empty(resources, "reaped resource must be invisible to the live view")

		// Reset the Create counter so we can detect any resurrection-driven Create.
		createCount.Store(0)

		// Wait through several auto-reconcile beats (interval=2s). No Create
		// should happen — the reaped resource must not be resurrected.
		time.Sleep(8 * time.Second)
		r.Equal(int32(0), createCount.Load(),
			"auto-reconcile must not resurrect a reaped target's resources (got %d unexpected Creates)",
			createCount.Load())

		resources, err = m.Datastore.LoadResourcesByStack("reap-noresurrect-stack")
		r.NoError(err)
		r.Empty(resources, "stack should remain empty (reaped) after auto-reconcile beats")
	})
}

// TestDiscovery_SkipsReapedTarget verifies the discovery target sweep never
// loads a reaped target: a reaped target is skipped entirely (no List calls
// against it, no unmanaged resources discovered for it), while a sibling
// live target is scanned normally.
func TestDiscovery_SkipsReapedTarget(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var reapedTargetListCalls atomic.Int32
		var liveTargetListCalls atomic.Int32

		overrides := &plugin.ResourcePluginOverrides{
			List: func(request *resource.ListRequest) (*resource.ListResult, error) {
				if request.ResourceType != "FakeAWS::S3::Bucket" {
					return &resource.ListResult{}, nil
				}
				switch awsRegionFromTargetConfig(t, request.TargetConfig) {
				case "reaped-region":
					reapedTargetListCalls.Add(1)
					return &resource.ListResult{NativeIDs: []string{"should-not-be-discovered"}}, nil
				case "live-region":
					liveTargetListCalls.Add(1)
					return &resource.ListResult{NativeIDs: []string{"live-resource-1"}}, nil
				}
				return &resource.ListResult{}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   fmt.Sprintf(`{"Tags":{"Name":"%s"}}`, request.NativeID),
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		reapedTarget := &pkgmodel.Target{
			Label:        "reaped-discovery-target",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"reaped-region"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(reapedTarget)
		r.NoError(err)

		liveTarget := &pkgmodel.Target{
			Label:        "live-discovery-target",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"live-region"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(liveTarget)
		r.NoError(err)

		reapTargetForTest(t, m.Datastore, "reaped-discovery-target")

		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		r.NoError(err)

		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		r.NoError(err)

		// Wait for the live target to be scanned — proves discovery ran.
		r.Eventually(func() bool {
			return liveTargetListCalls.Load() > 0
		}, 10*time.Second, 100*time.Millisecond, "discovery should scan the live target")

		// Give the reaped target every chance to be (wrongly) scanned too.
		time.Sleep(2 * time.Second)
		r.Equal(int32(0), reapedTargetListCalls.Load(), "discovery must never List against a reaped target")

		unmanaged, err := m.Datastore.LoadResourcesByStack(constants.UnmanagedStack)
		r.NoError(err)
		for _, res := range unmanaged {
			r.NotEqual("reaped-discovery-target", res.Target, "no unmanaged resource should be discovered on a reaped target")
		}
	})
}

// TestDestroyForma_ReapedTargetCleansUpTombstones verifies destroy-of-reaped:
// destroying a reaped target (target-only forma — the explicit "remove this
// dead target" declaration) is permitted (not rejected), never calls the
// plugin (the target is confirmed gone), and converts the target's
// reaped-tombstoned resources into definitive delete tombstones so they no
// longer show up as reaped (LoadReapedResources), alongside the existing
// hard-delete of the target row itself.
func TestDestroyForma_ReapedTargetCleansUpTombstones(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var deleteCount atomic.Int32

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
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"v1"}`,
				}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deleteCount.Add(1)
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        request.NativeID,
					},
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
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "reap-destroy-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "condemned", Type: "FakeAWS::Resource", Properties: json.RawMessage(`{"foo":"v1"}`), Schema: schema, Stack: "reap-destroy-stack", Target: "reap-destroy-target"},
			},
			Targets: []pkgmodel.Target{{Label: "reap-destroy-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("reap-destroy-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		reapTargetForTest(t, m.Datastore, "reap-destroy-target")

		reaped, err := m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Len(reaped, 1, "the target's resource must be tombstoned as reaped before destroy")

		// Destroy-of-reaped: a target-only forma (no Resources) always
		// generates a TargetOperationDelete regardless of resources on it.
		targetOnly := &pkgmodel.Forma{
			Targets: []pkgmodel.Target{{Label: "reap-destroy-target"}},
		}
		_, err = m.DestroyForma(targetOnly, &config.FormaCommandConfig{}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			target, err := m.Datastore.LoadTarget("reap-destroy-target")
			return err == nil && target == nil
		}, 15*time.Second, 200*time.Millisecond, "destroy-of-reaped must hard-delete the target row")

		reaped, err = m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Empty(reaped, "destroy-of-reaped must clean up the reaped tombstone rows")

		r.Equal(int32(0), deleteCount.Load(), "destroy-of-reaped must never call the plugin Delete")
	})
}

// TestDanglingReapedReferences_SurfacedNotDeleted verifies the
// dangling-dependent report: a live resource on a still-reachable target that
// $refs a resource which has since been reaped is surfaced by
// FindDanglingReapedReferences, and is left completely untouched (not
// deleted, not modified) — nested/cross-target reaping never cascades.
func TestDanglingReapedReferences_SurfacedNotDeleted(t *testing.T) {
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
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		// Parent resource, on the target that will be reaped.
		parentForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "dangling-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "parent", Type: "FakeAWS::Resource", Properties: json.RawMessage(`{"foo":"v1"}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}}, Stack: "dangling-stack", Target: "dangling-parent-target"},
			},
			Targets: []pkgmodel.Target{{Label: "dangling-parent-target"}},
		}
		_, err = m.ApplyForma(parentForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("dangling-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond)

		parentResources, err := m.Datastore.LoadResourcesByStack("dangling-stack")
		r.NoError(err)
		r.Len(parentResources, 1)
		parentKsuid := parentResources[0].Ksuid

		// Dependent resource, on a DIFFERENT (still-reachable) target, $refs the parent.
		dependentProperties := json.RawMessage(fmt.Sprintf(`{"upstream":{"$ref":"formae://%s#/foo","$value":"v1"}}`, parentKsuid))
		dependent := &pkgmodel.Resource{
			NativeID:   "dependent-native",
			Label:      "dependent",
			Type:       "FakeAWS::Resource",
			Properties: dependentProperties,
			Stack:      "dangling-stack",
			Target:     "dangling-live-target",
			Managed:    true,
		}
		_, err = m.Datastore.StoreResource(dependent, "test-cmd")
		r.NoError(err)
		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{Label: "dangling-live-target"})
		r.NoError(err)

		// Reap only the parent's target — the dependent's target is untouched.
		reapTargetForTest(t, m.Datastore, "dangling-parent-target")

		findings, err := metastructure.FindDanglingReapedReferences(m.Datastore)
		r.NoError(err)
		r.Len(findings, 1, "the dependent resource's $ref to the reaped parent must be surfaced exactly once")
		r.NotNil(findings[0].DependentResource)
		r.Equal("dependent", findings[0].DependentResource.Label)
		r.Equal(parentResources[0].URI(), findings[0].ReapedResource)

		// Left alone: the dependent resource itself is untouched by the report.
		stillLive, err := m.Datastore.LoadResource(dependent.URI())
		r.NoError(err)
		r.NotNil(stillLive, "the dangling dependent must not be deleted")
		assert.JSONEq(t, string(dependentProperties), string(stillLive.Properties))
	})
}

// TestTargetReap_NestedTargetsReapIndependently verifies that reaping never
// cascades across a nested/dependent chain of targets: two independent,
// resource-bearing targets each accrue and reap purely on their own
// unreachability — reaping one has zero effect on the other, and each only
// reaps once ITS OWN threshold is met.
func TestTargetReap_NestedTargetsReapIndependently(t *testing.T) {
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
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		// "Nested" chain: child's resource $refs the parent's resource. Reaping
		// has no traversal logic, so this dependency must have zero bearing on
		// which target reaps when.
		parentForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "nested-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "nested-parent", Type: "FakeAWS::Resource", Properties: json.RawMessage(`{"foo":"v1"}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}}, Stack: "nested-stack", Target: "nested-parent-target"},
			},
			Targets: []pkgmodel.Target{{Label: "nested-parent-target"}},
		}
		_, err = m.ApplyForma(parentForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("nested-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond)

		parentResources, err := m.Datastore.LoadResourcesByStack("nested-stack")
		r.NoError(err)
		parentKsuid := parentResources[0].Ksuid

		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{Label: "nested-child-target"})
		r.NoError(err)
		childProperties := json.RawMessage(fmt.Sprintf(`{"upstream":{"$ref":"formae://%s#/foo","$value":"v1"}}`, parentKsuid))
		child := &pkgmodel.Resource{
			NativeID:   "nested-child-native",
			Label:      "nested-child",
			Type:       "FakeAWS::Resource",
			Properties: childProperties,
			Stack:      "nested-stack",
			Target:     "nested-child-target",
			Managed:    true,
		}
		_, err = m.Datastore.StoreResource(child, "test-cmd")
		r.NoError(err)

		// Reap the parent target only.
		reapTargetForTest(t, m.Datastore, "nested-parent-target")

		parentTarget, err := m.Datastore.LoadTarget("nested-parent-target")
		r.NoError(err)
		r.Equal(pkgmodel.TargetHealthStateReaped, parentTarget.Health.State)

		// The child target must be completely unaffected: still whatever health
		// it had before (never touched), and its resource still live.
		childTarget, err := m.Datastore.LoadTarget("nested-child-target")
		r.NoError(err)
		r.NotEqual(pkgmodel.TargetHealthStateReaped, childTarget.Health.State, "reaping the parent must not cascade to the child target")

		childLive, err := m.Datastore.LoadResource(child.URI())
		r.NoError(err)
		r.NotNil(childLive, "the child's resource must remain untouched by the parent's reap")

		// Now reap the child independently, on its own threshold/timeline.
		reapTargetForTest(t, m.Datastore, "nested-child-target")

		childTarget, err = m.Datastore.LoadTarget("nested-child-target")
		r.NoError(err)
		r.Equal(pkgmodel.TargetHealthStateReaped, childTarget.Health.State, "the child must reap independently once its own threshold is met")

		reaped, err := m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Len(reaped, 2, "both the parent's and the child's resources must now be reaped, independently")
	})
}
