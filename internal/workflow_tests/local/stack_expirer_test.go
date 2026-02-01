// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestStackExpirer_ExpiresStackWithTTL tests that the StackExpirer actor
// correctly destroys stacks that have exceeded their TTL.
func TestStackExpirer_ExpiresStackWithTTL(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       request.Label,
						NativeID:        request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"bar"}`,
				}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "delete-" + request.NativeID,
						NativeID:        request.NativeID,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		// Create a stack with a short TTL (2 seconds)
		stackLabel := "expiring-stack-" + util.NewID()
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:      stackLabel,
					TTLSeconds: 2, // 2 seconds TTL
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      stackLabel,
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		// Apply the forma to create the stack and resource
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for the apply to complete
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Apply should complete successfully")

		// Verify the stack was created with TTL
		stack, err := m.Datastore.GetStackByLabel(stackLabel)
		require.NoError(t, err)
		require.NotNil(t, stack, "Stack should exist")
		require.NotZero(t, stack.TTLSeconds, "Stack should have TTL")
		require.Equal(t, int64(2), stack.TTLSeconds)
		require.False(t, stack.ExpiresAt.IsZero(), "Stack should have ExpiresAt")

		// Verify resources exist
		resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
		require.NoError(t, err)
		require.Len(t, resourcesByStack, 1)
		require.Len(t, resourcesByStack[stackLabel], 1)

		// Wait for the stack to expire and be destroyed by the StackExpirer
		// The TTL is 2 seconds, and the expirer checks every 5 seconds (default interval)
		require.Eventually(t, func() bool {
			// Check if the stack has been destroyed
			resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
			if err != nil {
				return false
			}
			return len(resourcesByStack) == 0
		}, 15*time.Second, 500*time.Millisecond, "Stack should be destroyed after TTL expires")

		// Verify the stack metadata was also deleted
		stack, err = m.Datastore.GetStackByLabel(stackLabel)
		require.NoError(t, err)
		require.Nil(t, stack, "Stack metadata should be deleted")

		// Verify destroy command was created
		fas, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		var destroyCmd *forma_command.FormaCommand
		for _, fc := range fas {
			if fc.Command == pkgmodel.CommandDestroy {
				destroyCmd = fc
				break
			}
		}
		require.NotNil(t, destroyCmd, "A destroy command should have been created by the StackExpirer")
		require.Equal(t, forma_command.CommandStateSuccess, destroyCmd.State)
	})
}

// TestStackExpirer_DoesNotExpireStackWithoutTTL tests that stacks without TTL
// are not expired by the StackExpirer.
func TestStackExpirer_DoesNotExpireStackWithoutTTL(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       request.Label,
						NativeID:        request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"bar"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		// Create a stack WITHOUT TTL
		stackLabel := "no-ttl-stack-" + util.NewID()

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: stackLabel,
					// No TTLSeconds set
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      stackLabel,
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		// Apply the forma
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for the apply to complete
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Apply should complete successfully")

		// Verify the stack was created without TTL
		stack, err := m.Datastore.GetStackByLabel(stackLabel)
		require.NoError(t, err)
		require.NotNil(t, stack, "Stack should exist")
		require.Zero(t, stack.TTLSeconds, "Stack should NOT have TTL")
		require.True(t, stack.ExpiresAt.IsZero(), "Stack should NOT have ExpiresAt")

		// Wait longer than typical TTL to ensure stack is not expired
		// The expirer checks frequently, but stack should remain
		time.Sleep(8 * time.Second) // Wait 8 seconds (beyond initial 5s delay + multiple check intervals)

		// Verify stack still exists
		resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
		require.NoError(t, err)
		require.Len(t, resourcesByStack, 1, "Stack should still exist (no TTL)")
		require.Len(t, resourcesByStack[stackLabel], 1, "Resource should still exist")

		// Verify no destroy command was created
		fas, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		for _, fc := range fas {
			require.NotEqual(t, pkgmodel.CommandDestroy, fc.Command, "No destroy command should have been created")
		}
	})
}


// TestStackExpirer_ForceExpireCheck tests the ForceExpireCheck message
// for triggering immediate expiration checks.
func TestStackExpirer_ForceExpireCheck(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		deleteCalled := false

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       request.Label,
						NativeID:        request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"bar"}`,
				}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				deleteCalled = true
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "delete-" + request.NativeID,
						NativeID:        request.NativeID,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		// Create a stack with very short TTL (TTL = 1 second for near-immediate expiry)
		stackLabel := "force-expire-stack-" + util.NewID()

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:      stackLabel,
					TTLSeconds: 1, // 1 second TTL (near-immediate expiry)
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      stackLabel,
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		// Apply the forma
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for the apply to complete
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Apply should complete successfully")

		// Wait for the TTL to expire (1 second + buffer)
		time.Sleep(2 * time.Second)

		// Force an immediate expiration check
		err = m.Node.Send(gen.Atom("StackExpirer"), metastructure.CheckExpiredStacks{})
		require.NoError(t, err)

		// Wait for the stack to be destroyed
		require.Eventually(t, func() bool {
			resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
			return err == nil && len(resourcesByStack) == 0
		}, 10*time.Second, 500*time.Millisecond, "Stack should be destroyed after forced expiration check")

		assert.True(t, deleteCalled, "Delete should have been called")
	})
}

// TestStackExpirer_MultipleStacks tests that the StackExpirer correctly handles
// multiple stacks with different TTLs.
func TestStackExpirer_MultipleStacks(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       request.Label,
						NativeID:        request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"bar"}`,
				}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "delete-" + request.NativeID,
						NativeID:        request.NativeID,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		// Create two stacks: one that expires quickly, one that doesn't expire
		expiringStackLabel := "expiring-" + util.NewID()
		persistentStackLabel := "persistent-" + util.NewID()

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label:      expiringStackLabel,
					TTLSeconds: 2, // 2 seconds TTL
				},
				{
					Label: persistentStackLabel,
					// No TTL - should not expire
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "expiring-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar"}`),
					Stack:      expiringStackLabel,
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Target:     "test-target",
				},
				{
					Label:      "persistent-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"baz"}`),
					Stack:      persistentStackLabel,
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		// Apply the forma
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for the apply to complete
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Apply should complete successfully")

		// Verify both stacks were created
		resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
		require.NoError(t, err)
		require.Len(t, resourcesByStack, 2, "Both stacks should be created")

		// Wait for the expiring stack to be destroyed
		require.Eventually(t, func() bool {
			resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
			if err != nil {
				return false
			}
			// Should have exactly 1 stack remaining (the persistent one)
			return len(resourcesByStack) == 1
		}, 15*time.Second, 500*time.Millisecond, "Expiring stack should be destroyed")

		// Verify the correct stack remains
		resourcesByStack, err = m.Datastore.LoadAllResourcesByStack()
		require.NoError(t, err)
		require.Len(t, resourcesByStack, 1)
		require.Len(t, resourcesByStack[persistentStackLabel], 1)
		require.Equal(t, persistentStackLabel, resourcesByStack[persistentStackLabel][0].Stack, "Persistent stack should remain")
		require.Equal(t, "persistent-resource", resourcesByStack[persistentStackLabel][0].Label)

		// Verify the expiring stack's metadata was deleted
		expiringStack, err := m.Datastore.GetStackByLabel(expiringStackLabel)
		require.NoError(t, err)
		require.Nil(t, expiringStack, "Expiring stack metadata should be deleted")

		// Verify the persistent stack's metadata still exists
		persistentStack, err := m.Datastore.GetStackByLabel(persistentStackLabel)
		require.NoError(t, err)
		require.NotNil(t, persistentStack, "Persistent stack metadata should exist")
	})
}
