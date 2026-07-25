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
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestTargetReap_EmptiedStackIsCleanedUp proves that when a reap tombstones the
// last live resource in a stack, the now-empty stack is cleaned up too — it must
// disappear from the inventory stack listing (ListAllStacks), exactly as a
// normal resource delete that empties a stack does.
//
// Regression guard for the gap where PersistTargetReap tombstoned resources but
// never triggered cleanupEmptyStacks, so a reap-emptied stack lingered forever.
func TestTargetReap_EmptiedStackIsCleanedUp(t *testing.T) {
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

		// Apply: the stack exists and holds its one resource.
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("reap-cleanup-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

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

		// ...and the now-empty stack must be cleaned up: gone from the inventory listing.
		r.Eventually(func() bool {
			return !stackListed(t, m.Datastore, "reap-cleanup-stack")
		}, 10*time.Second, 100*time.Millisecond, "a stack emptied by a reap must be cleaned up (absent from inventory stacks)")
	})
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
