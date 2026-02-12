// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestMetastructure_ApplyThenDestroyForma(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource-destroy",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}
		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			if len(fas) != 1 || len(fas[0].ResourceUpdates) != 1 {
				return false
			}

			return fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

		applyResources, err := m.Datastore.LoadResourcesByStack("test-stack1")
		assert.NoError(t, err)
		assert.Len(t, applyResources, 1)

		m.DestroyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		// Wait for destroy to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			if len(fas) != 2 {
				return false
			}

			// Find the destroy command by type (don't rely on order)
			var destroyForma *forma_command.FormaCommand
			for _, fc := range fas {
				if fc.Command == pkgmodel.CommandDestroy {
					destroyForma = fc
					break
				}
			}
			if destroyForma == nil {
				return false
			}

			if len(destroyForma.ResourceUpdates) != 1 {
				return false
			}

			return destroyForma.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				destroyForma.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

		fas, err := m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Equal(t, 2, len(fas))

		// Find apply and destroy commands by type (don't rely on order)
		var applyForma, destroyForma *forma_command.FormaCommand
		for _, fc := range fas {
			switch fc.Command {
			case pkgmodel.CommandApply:
				applyForma = fc
			case pkgmodel.CommandDestroy:
				destroyForma = fc
			}
		}

		assert.NotNil(t, applyForma, "should have an apply command")
		assert.NotNil(t, destroyForma, "should have a destroy command")
		assert.Equal(t, 1, len(destroyForma.ResourceUpdates))

		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, destroyForma.ResourceUpdates[0].State)

		assert.Equal(t, forma_command.CommandStateSuccess, destroyForma.State)
		destroyResources, err := m.Datastore.LoadResourcesByStack("test-stack1")
		assert.Nil(t, err)
		assert.Empty(t, destroyResources)

		// Verify the stack was also deleted
		stack, err := m.Datastore.GetStackByLabel("test-stack1")
		assert.NoError(t, err)
		assert.Nil(t, stack, "Stack should be deleted when all resources are destroyed")
	})
}

// TestMetastructure_DestroyPolicyOnlyForma tests that destroying a forma with only
// standalone policies (no resources) completes successfully without hanging.
// This is a regression test for a bug where policy-only destroy commands would hang
// indefinitely because the policy updates were never processed/persisted.
func TestMetastructure_DestroyPolicyOnlyForma(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// Create a forma with only a standalone TTL policy (no resources, no stacks)
		policyJSON := json.RawMessage(`{
			"Type": "ttl",
			"Label": "ephemeral-1h",
			"TTLSeconds": 3600,
			"OnDependents": "abort"
		}`)

		f := &pkgmodel.Forma{
			Policies: []json.RawMessage{policyJSON},
		}

		// Apply the policy first
		applyResp, err := m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModePatch,
		}, "test")
		assert.NoError(t, err)
		assert.NotNil(t, applyResp)

		// Wait for apply to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Apply should complete successfully")

		// Verify the policy was created
		policies, err := m.Datastore.ListAllStandalonePolicies()
		assert.NoError(t, err)
		assert.Len(t, policies, 1, "Should have one standalone policy after apply")

		// Now destroy the policy-only forma
		destroyResp, err := m.DestroyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		assert.NoError(t, err)
		assert.NotNil(t, destroyResp)

		// This is the critical test: destroy should complete (not hang)
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			// Find the destroy command
			var destroyCmd *forma_command.FormaCommand
			for _, fc := range fas {
				if fc.Command == pkgmodel.CommandDestroy {
					destroyCmd = fc
					break
				}
			}
			if destroyCmd == nil {
				return false
			}

			// The destroy command should complete
			return destroyCmd.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Destroy should complete (not hang)")

		// Verify the command structure
		fas, err := m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Equal(t, 2, len(fas), "Should have apply and destroy commands")

		var destroyCmd *forma_command.FormaCommand
		for _, fc := range fas {
			if fc.Command == pkgmodel.CommandDestroy {
				destroyCmd = fc
				break
			}
		}
		assert.NotNil(t, destroyCmd, "Should have a destroy command")
		assert.Len(t, destroyCmd.PolicyUpdates, 1, "Should have one policy update")
		assert.Equal(t, policy_update.PolicyOperationDelete, destroyCmd.PolicyUpdates[0].Operation)
		assert.Equal(t, policy_update.PolicyUpdateStateSuccess, destroyCmd.PolicyUpdates[0].State)

		// Verify the policy was actually deleted
		policies, err = m.Datastore.ListAllStandalonePolicies()
		assert.NoError(t, err)
		assert.Len(t, policies, 0, "Policy should be deleted after destroy")
	})
}
