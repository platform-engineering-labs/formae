// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func successfulCreateDelete() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusSuccess,
				RequestID:       "1234",
				NativeID:        "5678",
			}}, nil
		},
		Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
			return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusSuccess,
				RequestID:       "1234",
				NativeID:        "5678",
			}}, nil
		},
	}
}

func waitForCommands(t *testing.T, m *metastructure.Metastructure, count int) {
	t.Helper()
	assert.Eventually(t, func() bool {
		commands, err := m.Datastore.LoadFormaCommands()
		if err != nil {
			return false
		}
		incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
		if err != nil {
			return false
		}
		return len(commands) == count && len(incomplete) == 0
	}, 10*time.Second, 100*time.Millisecond, "expected %d completed commands", count)
}

func findDestroyCommand(t *testing.T, m *metastructure.Metastructure) *forma_command.FormaCommand {
	t.Helper()
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	for _, fc := range commands {
		if fc.Command == pkgmodel.CommandDestroy {
			return fc
		}
	}
	t.Fatal("no destroy command found")
	return nil
}

// TestDestroyForma_TargetOnly verifies that destroying a forma containing only
// a target (no resources in the forma) deletes the target and all resources
// deployed in that target.
func TestDestroyForma_TargetOnly(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, successfulCreateDelete())
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Apply a target with a resource
		applyForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		_, err = m.ApplyForma(applyForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 1)

		// Step 2: Destroy with ONLY the target (no resources in the forma).
		// This should cascade-delete all resources in the target, then the target itself.
		destroyForma := &pkgmodel.Forma{
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		_, err = m.DestroyForma(destroyForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 2)

		destroyCmd := findDestroyCommand(t, m)
		assert.Equal(t, forma_command.CommandStateSuccess, destroyCmd.State)

		// Verify resource was cascade-deleted
		require.Len(t, destroyCmd.ResourceUpdates, 1, "should cascade-delete the resource in the target")
		assert.Equal(t, resource_update.OperationDelete, destroyCmd.ResourceUpdates[0].Operation)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, destroyCmd.ResourceUpdates[0].State)

		// Verify target was deleted
		require.Len(t, destroyCmd.TargetUpdates, 1, "should delete the target")
		assert.Equal(t, target_update.TargetOperationDelete, destroyCmd.TargetUpdates[0].Operation)
		assert.Equal(t, target_update.TargetUpdateStateSuccess, destroyCmd.TargetUpdates[0].State)

		// Verify both are gone from the datastore
		resources, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)
		assert.Empty(t, resources, "resources should be deleted")

		target, err := m.Datastore.LoadTarget("test-target")
		require.NoError(t, err)
		assert.Nil(t, target, "target should be deleted")
	})
}

// TestDestroyForma_TargetWithResourcesInSameTarget verifies that when a forma
// contains both a target and resources in that same target, both the resources
// and the target are deleted. The target delete is ordered after the resource
// deletes in the DAG.
func TestDestroyForma_TargetWithResourcesInSameTarget(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, successfulCreateDelete())
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "test-bucket",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "test-stack",
					Target:  "test-target",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		// Step 1: Apply
		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 1)

		// Step 2: Destroy the same forma (target + resources in that target).
		// Both the resources and the target should be deleted.
		_, err = m.DestroyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 2)

		destroyCmd := findDestroyCommand(t, m)
		assert.Equal(t, forma_command.CommandStateSuccess, destroyCmd.State)

		// Verify resource was deleted
		require.Len(t, destroyCmd.ResourceUpdates, 1)
		assert.Equal(t, resource_update.OperationDelete, destroyCmd.ResourceUpdates[0].Operation)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, destroyCmd.ResourceUpdates[0].State)

		// Verify target was also deleted
		require.Len(t, destroyCmd.TargetUpdates, 1, "target should be deleted")
		assert.Equal(t, target_update.TargetOperationDelete, destroyCmd.TargetUpdates[0].Operation)
		assert.Equal(t, target_update.TargetUpdateStateSuccess, destroyCmd.TargetUpdates[0].State)

		// Verify both are gone from the datastore
		resources, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)
		assert.Empty(t, resources, "resources should be deleted")

		target, err := m.Datastore.LoadTarget("test-target")
		require.NoError(t, err)
		assert.Nil(t, target, "target should be deleted")
	})
}

// TestDestroyForma_TargetWithResourcesInDifferentTarget verifies that when a forma
// contains a target and resources in a DIFFERENT target, the target (and all its
// resources) is destroyed, the listed resources in the other target are destroyed,
// but the other target is preserved.
func TestDestroyForma_TargetWithResourcesInDifferentTarget(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, successfulCreateDelete())
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Apply — create two targets, each with a resource
		applyForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "stack-a"},
				{Label: "stack-b"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "bucket-a",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "stack-a",
					Target:  "target-a",
					Managed: true,
				},
				{
					Label:   "bucket-b",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "stack-b",
					Target:  "target-b",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "target-a",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
				{
					Label:     "target-b",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"eu-west-1"}`),
				},
			},
		}

		_, err = m.ApplyForma(applyForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 1)

		// Step 2: Destroy a forma with target-a (no resources in it) + a resource in target-b.
		// Expected: target-a cascade-deleted (target + bucket-a), bucket-b deleted, target-b stays.
		destroyForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "stack-b"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "bucket-b",
					Type:    "FakeAWS::S3::Bucket",
					Stack:   "stack-b",
					Target:  "target-b",
					Managed: true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "target-a",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		_, err = m.DestroyForma(destroyForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForCommands(t, m, 2)

		destroyCmd := findDestroyCommand(t, m)
		assert.Equal(t, forma_command.CommandStateSuccess, destroyCmd.State)

		// Verify: bucket-a (cascade from target-a) and bucket-b (explicit) both deleted
		assert.Len(t, destroyCmd.ResourceUpdates, 2, "should delete both resources")
		for _, ru := range destroyCmd.ResourceUpdates {
			assert.Equal(t, resource_update.OperationDelete, ru.Operation)
			assert.Equal(t, resource_update.ResourceUpdateStateSuccess, ru.State)
		}

		// Verify: target-a deleted, target-b preserved
		require.Len(t, destroyCmd.TargetUpdates, 1, "should only delete target-a")
		assert.Equal(t, target_update.TargetOperationDelete, destroyCmd.TargetUpdates[0].Operation)
		assert.Equal(t, target_update.TargetUpdateStateSuccess, destroyCmd.TargetUpdates[0].State)

		// Verify datastore state
		targetA, err := m.Datastore.LoadTarget("target-a")
		require.NoError(t, err)
		assert.Nil(t, targetA, "target-a should be deleted")

		targetB, err := m.Datastore.LoadTarget("target-b")
		require.NoError(t, err)
		assert.NotNil(t, targetB, "target-b should still exist")

		resourcesA, err := m.Datastore.LoadResourcesByStack("stack-a")
		require.NoError(t, err)
		assert.Empty(t, resourcesA, "resources in stack-a should be deleted")

		resourcesB, err := m.Datastore.LoadResourcesByStack("stack-b")
		require.NoError(t, err)
		assert.Empty(t, resourcesB, "resources in stack-b should be deleted")
	})
}
