// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"errors"
	"testing"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"

	"github.com/stretchr/testify/assert"
)

// newTestResourceUpdate creates a ResourceUpdate with a generated KSUID for testing.
// In production, KSUIDs are assigned by the resource_update_generator, but tests that
// bypass that flow need to set KSUIDs explicitly to avoid PRIMARY KEY collisions
// in the normalized resource_updates table.
func newTestResourceUpdate(label, resourceType, stack, target string, state resource_update.ResourceUpdateState, operation resource_update.OperationType) resource_update.ResourceUpdate {
	return resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label:  label,
			Type:   resourceType,
			Stack:  stack,
			Target: target,
			Ksuid:  util.NewID(),
		},
		ResourceTarget: pkgmodel.Target{
			Label:     target,
			Namespace: "test-namespace",
		},
		StartTs:   util.TimeNow(),
		State:     state,
		Operation: operation,
	}
}

func TestMetastructure_ApplyWhileAnotherFormaIsModifyingTheStack_ReturnsConflictingResourcesError(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, stop, err := test_helpers.NewTestMetastructure(t, nil)
		defer stop()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		executingFormaResourceUpdates := []resource_update.ResourceUpdate{
			newTestResourceUpdate("test-resource1", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),  // conflicting
			newTestResourceUpdate("test-resource2", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStatePending, resource_update.OperationCreate),     // conflicting
			newTestResourceUpdate("test-resource3", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateCanceled, resource_update.OperationCreate),   // conflicting
			newTestResourceUpdate("test-resource4", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateInProgress, resource_update.OperationCreate), // conflicting
			newTestResourceUpdate("test-resource5", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateSuccess, resource_update.OperationCreate),    // not conflicting
			newTestResourceUpdate("test-resource6", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateFailed, resource_update.OperationCreate),     // not conflicting
			newTestResourceUpdate("test-resource7", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateInProgress, resource_update.OperationRead),   // not conflicting
		}

		executingForma := forma_command.NewFormaCommand(
			&pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{
						Label: "test-stack1",
					},
				},
				Targets: []pkgmodel.Target{
					{
						Label:     "test-target",
						Namespace: "test-namespace",
					},
				},
			},
			&config.FormaCommandConfig{},
			pkgmodel.CommandApply,
			executingFormaResourceUpdates,
			nil, // No target updates for test
			"")
		executingForma.State = forma_command.CommandStateInProgress

		newFormaResourceUpdates := []resource_update.ResourceUpdate{
			newTestResourceUpdate("test-resource8", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
		}

		newForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack1"}},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource8",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
		}
		_ = forma_command.NewFormaCommand(newForma, &config.FormaCommandConfig{}, pkgmodel.CommandApply, newFormaResourceUpdates, nil, "")
		err = m.Datastore.StoreFormaCommand(executingForma, "1")
		if err != nil {
			t.Fatalf("Failed to store forma command: %v", err)
			return
		}

		cfg := config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}

		_, err = m.ApplyForma(newForma, &cfg, "test")
		assert.Error(t, err)

		var conflictErr apimodel.FormaConflictingCommandsError
		if errors.As(err, &conflictErr) {
			assert.Equal(t, 1, len(conflictErr.ConflictingCommands))
			assert.Equal(t, 4, len(conflictErr.ConflictingCommands[0].ResourceUpdates))
		} else {
			t.Errorf("Expected a ConflctingResourcesError, got: %T", err)
		}
	})
}

func TestMetastructure_ApplyFormaRejectIfResourceIsUpdating(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, stop, err := test_helpers.NewTestMetastructure(t, nil)
		defer stop()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		executingFormaResourceUpdates := []resource_update.ResourceUpdate{
			newTestResourceUpdate("test-resource1", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),  // conflicting
			newTestResourceUpdate("test-resource2", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStatePending, resource_update.OperationCreate),     // conflicting
			newTestResourceUpdate("test-resource3", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateInProgress, resource_update.OperationCreate), // conflicting
		}

		executingForma := forma_command.NewFormaCommand(
			&pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{
						Label: "test-stack1",
					},
				},
				Targets: []pkgmodel.Target{
					{
						Label:     "test-target",
						Namespace: "test-namespace",
					},
				},
			},
			&config.FormaCommandConfig{},
			pkgmodel.CommandApply,
			executingFormaResourceUpdates,
			nil,
			"")
		executingForma.State = forma_command.CommandStateInProgress
		err = m.Datastore.StoreFormaCommand(executingForma, "1")
		if err != nil {
			t.Fatalf("Failed to store forma command: %v", err)
			return
		}

		anotherExecutingFormaResourceUpdates := []resource_update.ResourceUpdate{
			newTestResourceUpdate("test-resource4", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateInProgress, resource_update.OperationCreate), // conflicting
			newTestResourceUpdate("test-resource5", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateSuccess, resource_update.OperationCreate),    // not conflicting
			newTestResourceUpdate("test-resource6", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateFailed, resource_update.OperationCreate),     // not conflicting
			newTestResourceUpdate("test-resource7", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateInProgress, resource_update.OperationRead),   // not conflicting
		}

		anotherExecutingForma := forma_command.NewFormaCommand(
			&pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{
						Label: "test-stack2",
					},
				},
				Targets: []pkgmodel.Target{
					{
						Label:     "test-target",
						Namespace: "test-namespace",
					},
				},
			},
			&config.FormaCommandConfig{},
			pkgmodel.CommandApply,
			anotherExecutingFormaResourceUpdates,
			nil,
			"")
		anotherExecutingForma.State = forma_command.CommandStateInProgress
		err = m.Datastore.StoreFormaCommand(anotherExecutingForma, "2")
		if err != nil {
			t.Fatalf("Failed to store forma command: %v", err)
			return
		}

		newForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack1"}, {Label: "test-stack2"}},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				{
					Label:  "test-resource3",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				{
					Label:  "test-resource4",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				{
					Label:  "test-resource5",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				{
					Label:  "test-resource6",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				{
					Label:  "test-resource7",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
			},
		}
		newFormaResourceUpdates := []resource_update.ResourceUpdate{
			newTestResourceUpdate("test-resource1", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
			newTestResourceUpdate("test-resource2", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
			newTestResourceUpdate("test-resource3", "FakeAWS::S3::Bucket", "test-stack1", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
			newTestResourceUpdate("test-resource4", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
			newTestResourceUpdate("test-resource5", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
			newTestResourceUpdate("test-resource6", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
			newTestResourceUpdate("test-resource7", "FakeAWS::S3::Bucket", "test-stack2", "test-target", resource_update.ResourceUpdateStateNotStarted, resource_update.OperationCreate),
		}

		_ = forma_command.NewFormaCommand(newForma, &config.FormaCommandConfig{}, pkgmodel.CommandApply, newFormaResourceUpdates, nil, "")

		cfg := config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}

		_, err = m.ApplyForma(newForma, &cfg, "test")
		assert.Error(t, err)

		var conflictErr apimodel.FormaConflictingCommandsError
		if errors.As(err, &conflictErr) {
			assert.Equal(t, 2, len(conflictErr.ConflictingCommands))
			// Check that we have one command with 3 updates and one with 1, regardless of order
			counts := []int{
				len(conflictErr.ConflictingCommands[0].ResourceUpdates),
				len(conflictErr.ConflictingCommands[1].ResourceUpdates),
			}
			assert.Contains(t, counts, 3, "Expected one command with 3 conflicting resource updates")
			assert.Contains(t, counts, 1, "Expected one command with 1 conflicting resource update")
		} else {
			t.Errorf("Expected a ConflctingResourcesError, got: %T", err)
		}
	})
}
