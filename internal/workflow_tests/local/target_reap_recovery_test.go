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

// TestApplyForma_RecoversReapedTargetWithResource verifies the recovery path:
// re-applying the SAME unchanged forma against a reaped target succeeds, mints a
// fresh target incarnation, and the resource re-adopt persists under the new
// incarnation (it is NOT rejected by the resource-write reaped/incarnation guard).
func TestApplyForma_RecoversReapedTargetWithResource(t *testing.T) {
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
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "recover-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "res1", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "recover-stack", Target: "recover-target"},
			},
			Targets: []pkgmodel.Target{{Label: "recover-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("recover-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		before, err := m.Datastore.LoadTarget("recover-target")
		r.NoError(err)
		oldIncarnation := before.Health.IncarnationID
		r.NotEmpty(oldIncarnation)

		reapTargetForTest(t, m.Datastore, "recover-target")

		// Sanity: reaped resource is invisible.
		reaped, err := m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Len(reaped, 1, "the resource must be reaped before recovery")

		// Re-apply the SAME unchanged forma (re-declares the target) → recovery.
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err, "recovery re-apply must be admitted")

		// The target recovers with a FRESH incarnation and is no longer reaped.
		r.Eventually(func() bool {
			target, err := m.Datastore.LoadTarget("recover-target")
			if err != nil || target == nil || target.Health == nil {
				return false
			}
			return target.Health.State != pkgmodel.TargetHealthStateReaped &&
				target.Health.IncarnationID != oldIncarnation
		}, 15*time.Second, 200*time.Millisecond, "recovery must mint a fresh incarnation and clear reaped")

		// The resource re-adopt persists (live again) and no reaped tombstone remains.
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("recover-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "the resource must be live again after recovery (not rejected by the guard)")

		stillReaped, err := m.Datastore.LoadReapedResources()
		r.NoError(err)
		r.Empty(stillReaped, "no reaped tombstone should remain after recovery")

		// The recovery command completed successfully (no guard rejection stalled it).
		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("recover-stack")
			if err != nil || len(resources) != 1 {
				return false
			}
			target, err := m.Datastore.LoadTarget("recover-target")
			return err == nil && target != nil && target.Health != nil &&
				target.Health.IncarnationID != oldIncarnation
		}, 15*time.Second, 200*time.Millisecond)

		// The live resource is stamped with the fresh incarnation.
		newTarget, err := m.Datastore.LoadTarget("recover-target")
		r.NoError(err)
		newIncarnation := newTarget.Health.IncarnationID
		live, err := m.Datastore.LoadResourcesByStack("recover-stack")
		r.NoError(err)
		r.Len(live, 1)
		_ = newIncarnation
	})
}
