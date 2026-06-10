// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestAutoReconciler_ReconcileAfterSyncChange tests that the auto-reconciler reverts
// changes detected via synchronization to the state at last reconcile.
func TestAutoReconciler_ReconcileAfterSyncChange(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Track the current state of resource properties
		currentProps := `{"foo":"original"}`

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "create-1",
						NativeID:        "native-1",
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   currentProps,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				// Update restores the original properties
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationUpdate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "update-1",
						NativeID:        request.NativeID,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		// Enable synchronization so it detects the OOB change and updates the datastore
		cfg.Agent.Synchronization.Enabled = true
		cfg.Agent.Synchronization.Interval = 1 * time.Second // Run sync every second
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		// Create a forma with a stack that has an auto-reconcile policy (3 second interval)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:       "auto-reconcile-stack",
					Description: "Stack with auto-reconcile policy",
					Policies: []json.RawMessage{
						json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":3}`),
					},
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::Resource",
					Properties: json.RawMessage(`{"foo":"original"}`),
					Stack:      "auto-reconcile-stack",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "test-target"},
			},
		}

		// Apply the forma (initial reconcile)
		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")

		// Wait for the initial apply to complete
		r := require.New(t)
		r.Eventually(
			func() bool {
				resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
				if err != nil {
					return false
				}
				resources := resourcesByStack["auto-reconcile-stack"]
				return len(resources) == 1 && resources[0].Label == "test-resource"
			},
			10*time.Second,
			200*time.Millisecond,
			"Initial apply should complete",
		)

		// Simulate an out-of-band change by modifying the resource properties in the plugin
		// The synchronizer (enabled with 1s interval) will detect this and update the datastore
		currentProps = `{"foo":"changed-by-oob"}`

		// Wait for sync to detect the OOB change (sync runs every 1s, give it some time)
		time.Sleep(3 * time.Second)

		// Wait for the auto-reconcile to trigger (interval is 3 seconds, give it some buffer)
		// The auto-reconciler should detect the change and create a new command to revert it
		r.Eventually(
			func() bool {
				// Look for a forma command with the auto-reconcile source
				query := &datastore.StatusQuery{
					N: 10,
				}
				commands, err := m.Datastore.QueryFormaCommands(query)
				if err != nil {
					return false
				}

				for _, cmd := range commands {
					// Check if any resource update has the auto-reconcile source
					updates, err := m.Datastore.LoadResourceUpdates(cmd.ID)
					if err != nil {
						continue
					}
					for _, ru := range updates {
						if ru.Source == resource_update.FormaCommandSourcePolicyAutoReconcile {
							// Found an auto-reconcile command
							return true
						}
					}
				}
				return false
			},
			15*time.Second,
			500*time.Millisecond,
			"Auto-reconciler should trigger and create a reconcile command",
		)
	})
}

// TestForceReconcile_RejectedWithoutPolicy verifies that ForceAutoReconcile
// returns a ReconcilePolicyRequiredError when the stack does not have an
// auto-reconcile policy attached. This prevents accidental destructive
// reconciles on stacks that were not explicitly configured for it.
func TestForceReconcile_RejectedWithoutPolicy(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, cleanup, err := test_helpers.NewTestMetastructure(t, nil)
		defer cleanup()
		require.NoError(t, err)

		// Apply a forma with a stack but NO auto-reconcile policy
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "no-policy-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      "no-policy-stack",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "test-target"},
			},
		}

		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")

		// Wait for the apply to complete
		r := require.New(t)
		r.Eventually(
			func() bool {
				resources, err := m.Datastore.LoadResourcesByStack("no-policy-stack")
				return err == nil && len(resources) == 1
			},
			5*time.Second,
			100*time.Millisecond,
			"Initial apply should complete",
		)

		// Attempt force-reconcile — should be rejected
		resp, err := m.ForceAutoReconcile("no-policy-stack")
		r.Error(err, "ForceAutoReconcile should return an error when no auto-reconcile policy is attached")
		r.Nil(resp)

		var policyErr apimodel.ReconcilePolicyRequiredError
		r.ErrorAs(err, &policyErr, "Error should be ReconcilePolicyRequiredError")
		r.Equal("no-policy-stack", policyErr.StackLabel)
	})
}

// TestAutoReconciler_RetriesFailedUpdate documents whether the auto-reconciler
// can re-attempt a resource update that failed on the previous reconcile.
//
// Scenario: an apply with three resources is issued against a stack with an
// auto-reconcile policy. One resource's Create fails transiently. Once the
// transient failure clears, the auto-reconciler should eventually issue a
// follow-up reconcile that brings the failed resource into the desired state
// — i.e. the fleet should converge over multiple auto-reconcile ticks, even
// across intermittent failures.
//
// Today, the resources persisted by the previous reconcile are the only basis
// for the next desired-state forma (via GetResourcesAtLastReconcile), so a
// resource whose Create failed never appears in any subsequent reconcile and
// the system cannot self-heal.
func TestAutoReconciler_RetriesFailedUpdate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var failBCreate atomic.Bool
		failBCreate.Store(true)

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Label == "resource-b" && failBCreate.Load() {
					// Use a non-recoverable error code so the PluginOperator's
					// built-in retries don't paper over the failure — the
					// update should end up in Failed state, matching the
					// "transient outage that exceeded local retries" scenario.
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
							ErrorCode:       resource.OperationErrorCodeUnforeseenError,
							StatusMessage:   "simulated outage (non-recoverable)",
							NativeID:        "native-" + request.Label,
						},
					}, nil
				}
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
					Properties:   `{"foo":"original"}`,
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
		// Disable sync so the only path that can recover the failed resource
		// is auto-reconcile — keeps this test focused on convergence.
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		resourceProps := json.RawMessage(`{"foo":"original"}`)
		schema := pkgmodel.Schema{Fields: []string{"foo"}}

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "convergence-stack",
					Policies: []json.RawMessage{
						json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":2}`),
					},
				},
			},
			Resources: []pkgmodel.Resource{
				{Label: "resource-a", Type: "FakeAWS::Resource", Properties: resourceProps, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
				{Label: "resource-b", Type: "FakeAWS::Resource", Properties: resourceProps, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
				{Label: "resource-c", Type: "FakeAWS::Resource", Properties: resourceProps, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
			},
			Targets: []pkgmodel.Target{{Label: "test-target"}},
		}

		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")

		r := require.New(t)

		// Wait for the initial apply to reach a final state. Only A and C
		// should have been persisted; B's Create failed.
		r.Eventually(
			func() bool {
				resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
				if err != nil {
					return false
				}
				labels := map[string]bool{}
				for _, res := range resources {
					labels[res.Label] = true
				}
				return labels["resource-a"] && labels["resource-c"] && !labels["resource-b"]
			},
			15*time.Second,
			200*time.Millisecond,
			"initial apply: A and C should be persisted, B should not (Create failed)",
		)

		// Clear the transient failure so the next attempt at B succeeds.
		failBCreate.Store(false)

		// The auto-reconciler should fire (interval=2s) and produce a follow-up
		// reconcile that includes resource-b. Eventually all three resources
		// should be present.
		r.Eventually(
			func() bool {
				resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
				if err != nil {
					return false
				}
				labels := map[string]bool{}
				for _, res := range resources {
					labels[res.Label] = true
				}
				return labels["resource-a"] && labels["resource-b"] && labels["resource-c"]
			},
			30*time.Second,
			500*time.Millisecond,
			"auto-reconciler should retry failed update and converge to all 3 resources present",
		)
	})
}

// TestAutoReconciler_RetriesFailureAndRevertsOOBDriftSimultaneously covers
// the fleet-convergence scenario this work is motivated by: one node is
// offline during a fleet-wide update (its update fails), and meanwhile a
// healthy node has its config drifted out-of-band. Auto-reconcile must
// revert the OOB drift on the healthy node in the same cycle that retries
// the offline node — the offline node's repeated failure must not block
// drift correction on the others.
func TestAutoReconciler_RetriesFailureAndRevertsOOBDriftSimultaneously(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var failBUpdate atomic.Bool

		// observedProps simulates the plugin-side ("cloud") view of each
		// resource's properties, keyed by NativeID (the field Read sees).
		// Read returns whatever is stored here; successful Updates mutate
		// it; we mutate it directly to simulate out-of-band drift.
		var mu sync.Mutex
		observedProps := map[string]string{}
		nativeIDFor := func(label string) string { return "native-" + label }
		getPropsByNativeID := func(nativeID string) string {
			mu.Lock()
			defer mu.Unlock()
			return observedProps[nativeID]
		}
		setPropsByNativeID := func(nativeID, props string) {
			mu.Lock()
			defer mu.Unlock()
			observedProps[nativeID] = props
		}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				nativeID := nativeIDFor(request.Label)
				setPropsByNativeID(nativeID, string(request.Properties))
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        nativeID,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   getPropsByNativeID(request.NativeID),
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				if request.Label == "resource-b" && failBUpdate.Load() {
					return &resource.UpdateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationUpdate,
							OperationStatus: resource.OperationStatusFailure,
							ErrorCode:       resource.OperationErrorCodeUnforeseenError,
							StatusMessage:   "simulated outage (node offline)",
							NativeID:        request.NativeID,
						},
					}, nil
				}
				setPropsByNativeID(request.NativeID, string(request.DesiredProperties))
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
		// Enable sync so OOB drift propagates from the plugin-side view
		// into the resources table — the auto-reconciler reads from the
		// resources table for the existing state side of its diff.
		cfg.Agent.Synchronization.Enabled = true
		cfg.Agent.Synchronization.Interval = 1 * time.Second
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		v2 := json.RawMessage(`{"foo":"v2"}`)
		stack := pkgmodel.Stack{
			Label: "convergence-stack",
			Policies: []json.RawMessage{
				json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":2}`),
			},
		}
		targets := []pkgmodel.Target{{Label: "test-target"}}

		// Phase 1: initial apply at v1 — all three succeed.
		f1 := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{stack},
			Resources: []pkgmodel.Resource{
				{Label: "resource-a", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
				{Label: "resource-b", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
				{Label: "resource-c", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
			},
			Targets: targets,
		}
		m.ApplyForma(f1, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")

		r := require.New(t)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
			return err == nil && len(resources) == 3
		}, 15*time.Second, 200*time.Millisecond, "initial apply: all 3 resources should be created")

		// Phase 2: arm B's update failure, then apply v2 to all three.
		failBUpdate.Store(true)
		f2 := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{stack},
			Resources: []pkgmodel.Resource{
				{Label: "resource-a", Type: "FakeAWS::Resource", Properties: v2, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
				{Label: "resource-b", Type: "FakeAWS::Resource", Properties: v2, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
				{Label: "resource-c", Type: "FakeAWS::Resource", Properties: v2, Schema: schema, Stack: "convergence-stack", Target: "test-target"},
			},
			Targets: targets,
		}
		m.ApplyForma(f2, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")

		// Wait for the second reconcile to settle: A and C at v2, B still at v1.
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
			if err != nil {
				return false
			}
			byLabel := map[string]string{}
			for _, res := range resources {
				byLabel[res.Label] = string(res.Properties)
			}
			return byLabel["resource-a"] == `{"foo":"v2"}` &&
				byLabel["resource-b"] == `{"foo":"v1"}` &&
				byLabel["resource-c"] == `{"foo":"v2"}`
		}, 15*time.Second, 200*time.Millisecond, "after second reconcile: A=v2, B=v1 (failed), C=v2")

		// Phase 3: simulate OOB drift on A — change observed properties.
		// Sync (1s interval) will detect this and update the resources table.
		setPropsByNativeID(nativeIDFor("resource-a"), `{"foo":"v_oob"}`)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
			if err != nil {
				return false
			}
			for _, res := range resources {
				if res.Label == "resource-a" && string(res.Properties) == `{"foo":"v_oob"}` {
					return true
				}
			}
			return false
		}, 10*time.Second, 200*time.Millisecond, "sync should ingest OOB drift on A into the resources table")

		// Phase 4: auto-reconcile should revert A's drift while B's update
		// still fails. We assert by waiting for A's stored properties to
		// flip back to v2, while B stays at v1.
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
			if err != nil {
				return false
			}
			byLabel := map[string]string{}
			for _, res := range resources {
				byLabel[res.Label] = string(res.Properties)
			}
			return byLabel["resource-a"] == `{"foo":"v2"}` &&
				byLabel["resource-b"] == `{"foo":"v1"}` &&
				byLabel["resource-c"] == `{"foo":"v2"}`
		}, 30*time.Second, 500*time.Millisecond,
			"auto-reconcile should revert OOB drift on A while B's update continues to fail")

		// Phase 5: clear B's failure; auto-reconcile should drive B to v2
		// on its next tick without disturbing A or C.
		failBUpdate.Store(false)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("convergence-stack")
			if err != nil || len(resources) != 3 {
				return false
			}
			for _, res := range resources {
				if string(res.Properties) != `{"foo":"v2"}` {
					return false
				}
			}
			return true
		}, 30*time.Second, 500*time.Millisecond,
			"after clearing B's failure: all 3 resources converge to v2")
	})
}

// TestAutoReconciler_RevertsInterveningPatch verifies that a patch-mode
// apply between two reconcile cycles does not shift the auto-reconcile
// baseline — auto-reconcile drives the resource back to the state declared
// by the last reconcile, undoing the patch.
//
// This is the patch-side counterpart to TestAutoReconciler_ReconcileAfterSyncChange
// (which exercises the same property for sync-detected OOB drift): the
// auto-reconcile baseline is the user's last reconcile-mode apply, and
// intervening sync or patch operations are treated as drift that the next
// reconcile cycle overwrites.
func TestAutoReconciler_RevertsInterveningPatch(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		observedProps := map[string]string{}
		getPropsByNativeID := func(nativeID string) string {
			mu.Lock()
			defer mu.Unlock()
			return observedProps[nativeID]
		}
		setPropsByNativeID := func(nativeID, props string) {
			mu.Lock()
			defer mu.Unlock()
			observedProps[nativeID] = props
		}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				nativeID := "native-" + request.Label
				setPropsByNativeID(nativeID, string(request.Properties))
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        nativeID,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   getPropsByNativeID(request.NativeID),
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				setPropsByNativeID(request.NativeID, string(request.DesiredProperties))
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
		// Sync stays off here so the reverted property doesn't get echoed
		// back from the plugin between reconcile cycles — we want a clean
		// signal that the auto-reconciler is the one driving the property
		// back to its declared value.
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		vPatched := json.RawMessage(`{"foo":"patched"}`)
		stack := pkgmodel.Stack{
			Label: "patch-undo-stack",
			Policies: []json.RawMessage{
				json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":2}`),
			},
		}
		targets := []pkgmodel.Target{{Label: "test-target"}}
		resourceTemplate := func(props json.RawMessage) pkgmodel.Resource {
			return pkgmodel.Resource{
				Label: "patched-resource", Type: "FakeAWS::Resource",
				Properties: props, Schema: schema,
				Stack: "patch-undo-stack", Target: "test-target",
			}
		}

		// Phase 1: initial reconcile at v1.
		f1 := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{stack},
			Resources: []pkgmodel.Resource{resourceTemplate(v1)},
			Targets:   targets,
		}
		m.ApplyForma(f1, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")

		r := require.New(t)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("patch-undo-stack")
			if err != nil || len(resources) != 1 {
				return false
			}
			return string(resources[0].Properties) == `{"foo":"v1"}`
		}, 15*time.Second, 200*time.Millisecond, "initial reconcile should land the resource at v1")

		// Phase 2: patch the resource to a new value. This succeeds and
		// updates the resources table, but is mode=patch so it must not
		// become the auto-reconcile baseline.
		fPatch := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{stack},
			Resources: []pkgmodel.Resource{resourceTemplate(vPatched)},
			Targets:   targets,
		}
		m.ApplyForma(fPatch, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "test-client")

		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("patch-undo-stack")
			if err != nil || len(resources) != 1 {
				return false
			}
			return string(resources[0].Properties) == `{"foo":"patched"}`
		}, 15*time.Second, 200*time.Millisecond, "patch should land the resource at patched")

		// Phase 3: auto-reconcile should pick the initial reconcile as the
		// baseline (the patch's config_mode='patch' excludes it from the
		// candidate pool) and drive the resource back to v1.
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("patch-undo-stack")
			if err != nil || len(resources) != 1 {
				return false
			}
			return string(resources[0].Properties) == `{"foo":"v1"}`
		}, 30*time.Second, 500*time.Millisecond,
			"auto-reconcile should revert the intervening patch back to the reconcile baseline (v1)")
	})
}
