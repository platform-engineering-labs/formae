// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"errors"
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

func reapAdmissionOverrides() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
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
			return &resource.DeleteResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					NativeID:        request.NativeID,
				},
			}, nil
		},
	}
}

// TestApplyForma_ReapedTargetResourceOnlyRejected verifies command admission:
// an apply that references a reaped target through a resource but does NOT
// re-declare the target is rejected with a typed TargetReapedError, and no
// forma command is persisted for it.
func TestApplyForma_ReapedTargetResourceOnlyRejected(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, reapAdmissionOverrides(), cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "reap-admit-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "res1", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "reap-admit-stack", Target: "reap-admit-target"},
			},
			Targets: []pkgmodel.Target{{Label: "reap-admit-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("reap-admit-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		reapTargetForTest(t, m.Datastore, "reap-admit-target")

		// Resource-only apply: references the reaped target but does NOT re-declare
		// it (no Targets block). Use a distinct client ID so we can prove nothing
		// was persisted for this rejected command.
		resourceOnly := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "reap-admit-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "res1", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "reap-admit-stack", Target: "reap-admit-target"},
			},
		}
		_, err = m.ApplyForma(resourceOnly, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "rejected-client")
		r.Error(err, "an apply that touches a reaped target without re-declaring it must be rejected")

		var reapedErr apimodel.TargetReapedError
		r.True(errors.As(err, &reapedErr), "error must be a TargetReapedError, got %T: %v", err, err)
		r.Contains(reapedErr.TargetLabels, "reap-admit-target")

		// No command persisted for the rejected client.
		cmd, err := m.Datastore.GetMostRecentFormaCommandByClientID("rejected-client")
		if err == nil {
			r.Nil(cmd, "no forma command must be persisted for a rejected apply")
		}
	})
}

// TestDestroyForma_ReapedTargetNotRejected verifies that command admission does
// NOT reject a destroy against a reaped target — destroy-of-reaped is the
// sanctioned cleanup path.
func TestDestroyForma_ReapedTargetNotRejected(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, reapAdmissionOverrides(), cfg)
		defer cleanup()
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "reap-destroy-admit-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "res1", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "reap-destroy-admit-stack", Target: "reap-destroy-admit-target"},
			},
			Targets: []pkgmodel.Target{{Label: "reap-destroy-admit-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			resources, err := m.Datastore.LoadResourcesByStack("reap-destroy-admit-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		reapTargetForTest(t, m.Datastore, "reap-destroy-admit-target")

		targetOnly := &pkgmodel.Forma{
			Targets: []pkgmodel.Target{{Label: "reap-destroy-admit-target"}},
		}
		_, err = m.DestroyForma(targetOnly, &config.FormaCommandConfig{}, "test-client")
		r.NoError(err, "destroy against a reaped target must not be rejected")

		var reapedErr apimodel.TargetReapedError
		r.False(errors.As(err, &reapedErr), "destroy must never produce a TargetReapedError")
	})
}
