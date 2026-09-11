// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Reaping removes observed inventory, not accepted desired intent. A reconciled
// declaration retains its stack incarnation, while patch-only inventory still
// triggers cleanup after the last resource is reaped.
func TestTargetReap_StackCleanupRespectsDesiredIntent(t *testing.T) {
	for _, mode := range []pkgmodel.FormaApplyMode{pkgmodel.FormaApplyModeReconcile, pkgmodel.FormaApplyModePatch} {
		t.Run(string(mode), func(t *testing.T) {
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

				schema := pkgmodel.Schema{Fields: []string{"foo"}}
				v1 := json.RawMessage(`{"foo":"v1"}`)
				f := &pkgmodel.Forma{
					Stacks: []pkgmodel.Stack{{Label: "reap-cleanup-stack"}},
					Resources: []pkgmodel.Resource{
						{Label: "only", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "reap-cleanup-stack", Target: "reap-cleanup-target"},
					},
					Targets: []pkgmodel.Target{{Label: "reap-cleanup-target"}},
				}

				initialCount := 1
				// Patch requires live inventory in its stack. Seed only observed
				// inventory, without any accepted reconcile declaration, then
				// exercise the real patch create and target-reap paths.
				if mode == pkgmodel.FormaApplyModePatch {
					_, err := m.Datastore.CreateStack(&pkgmodel.Stack{Label: "reap-cleanup-stack"}, "patch-fixture")
					r.NoError(err)
					_, err = m.Datastore.CreateTarget(&f.Targets[0])
					r.NoError(err)
					seed := f.Resources[0]
					seed.Label = "seeded"
					seed.Ksuid = util.NewID()
					seed.NativeID = "native-seeded"
					seed.Managed = true
					_, err = m.Datastore.StoreResource(&seed, "patch-fixture")
					r.NoError(err)
					initialCount = 2
				}
				// Apply: the stack exists and holds its one resource.
				created, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: mode}, "test-client", "", "")
				r.NoError(err)
				r.Eventually(func() bool {
					resources, err := m.Datastore.LoadResourcesByStack("reap-cleanup-stack")
					return err == nil && len(resources) == initialCount
				}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

				r.Eventually(func() bool {
					command, err := m.Datastore.GetFormaCommandByCommandID(created.CommandID)
					return err == nil && command != nil && command.State == forma_command.CommandStateSuccess
				}, 15*time.Second, 100*time.Millisecond, "initial apply must be terminal before reap")
				originalStack, err := m.Datastore.GetStackByLabel("reap-cleanup-stack")
				r.NoError(err)
				r.NotNil(originalStack)
				r.True(stackListed(t, m.Datastore, "reap-cleanup-stack"), "the stack must be listed while it holds a resource")

				// Push the target over its reap-after threshold and let the real reaper reap it.
				seedOverThresholdUnreachableTarget(t, m.Datastore, "reap-cleanup-target")
				r.NoError(m.ForceReap())
				r.Eventually(func() bool {
					target, err := m.Datastore.LoadTarget("reap-cleanup-target")
					return err == nil && target != nil && target.Health != nil &&
						target.Health.State == pkgmodel.TargetHealthStateReaped
				}, 10*time.Second, 100*time.Millisecond, "ForceReap must reap the over-threshold target")

				// The resource is tombstoned (invisible to the live view)...
				resources, err := m.Datastore.LoadResourcesByStack("reap-cleanup-stack")
				r.NoError(err)
				r.Empty(resources, "the reaped target's resource must be invisible to the live view")

				if mode == pkgmodel.FormaApplyModeReconcile {
					retained, err := m.Datastore.GetStackByLabel("reap-cleanup-stack")
					r.NoError(err)
					r.NotNil(retained, "accepted intent must retain the stack after inventory is reaped")
					r.Equal(originalStack.ID, retained.ID, "reaping must not replace the desired state's incarnation")
					r.True(stackListed(t, m.Datastore, "reap-cleanup-stack"))
					desired, err := m.ExtractDesiredStacks("stack:reap-cleanup-stack")
					r.NoError(err)
					r.Len(desired.Resources, 1)
					r.Equal("only", desired.Resources[0].Label)
					r.JSONEq(`{"foo":"v1"}`, string(desired.Resources[0].Properties))
				} else {
					desired, err := m.Datastore.GetResourcesAtLastReconcile("reap-cleanup-stack")
					r.NoError(err)
					r.Empty(desired, "patch-only inventory has no accepted reconcile declaration")
					r.Eventually(func() bool {
						return !stackListed(t, m.Datastore, "reap-cleanup-stack")
					}, 10*time.Second, 100*time.Millisecond, "a reaped stack without accepted desired intent must retire")
				}
			})
		})
	}
}

// stackListed reports whether a stack with the given label appears in the
// inventory stack listing (the same view `formae inventory stacks` renders).
func stackListed(t *testing.T, ds interface {
	ListAllStacks() ([]*pkgmodel.Stack, error)
}, label string) bool {
	t.Helper()
	stacks, err := ds.ListAllStacks()
	require.NoError(t, err)
	for _, s := range stacks {
		if s.Label == label {
			return true
		}
	}
	return false
}
