// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package workflow_tests

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

func TestMetastructure_ApplyWhileAnotherFormaIsModifyingTheStack_ReturnsConflictingResourcesError(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, stop, err := test_helpers.NewTestMetastructure(t, nil)
		defer stop()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		executingFormaResourceUpdates := []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStatePending, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource3",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateCanceled, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource4",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateInProgress, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource5",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateSuccess, // not conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource6",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateFailed, // not conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource7",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateInProgress,
				Operation: resource_update.OperationRead, // not conflicting
			},
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
			"")
		executingForma.State = forma_command.CommandStateInProgress

		newFormaResourceUpdates := []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource8",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
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
		newFormaCommand := forma_command.NewFormaCommand(newForma, &config.FormaCommandConfig{}, pkgmodel.CommandApply, newFormaResourceUpdates, "")
		err = m.Datastore.StoreFormaCommand(executingForma, "1")
		if err != nil {
			t.Fatalf("Failed to store forma command: %v", err)
			return
		}

		cfg := config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}

		_, err = m.ApplyForma(&newFormaCommand.Forma, &cfg, "test")
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
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStatePending, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource3",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateInProgress, // conflicting
				Operation: resource_update.OperationCreate,
			},
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
			"")
		executingForma.State = forma_command.CommandStateInProgress
		err = m.Datastore.StoreFormaCommand(executingForma, "1")
		if err != nil {
			t.Fatalf("Failed to store forma command: %v", err)
			return
		}

		anotherExecutingFormaResourceUpdates := []resource_update.ResourceUpdate{
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource4",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateInProgress, // conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource5",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateSuccess, // not conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource6",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateFailed, // not conflicting
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource7",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateInProgress,
				Operation: resource_update.OperationRead, // not conflicting
			},
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
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource3",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource4",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource5",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource6",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
			{
				Resource: pkgmodel.Resource{
					Label:  "test-resource7",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
				ResourceTarget: pkgmodel.Target{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
				StartTs:   util.TimeNow(),
				State:     resource_update.ResourceUpdateStateNotStarted,
				Operation: resource_update.OperationCreate,
			},
		}

		newFormaCommand := forma_command.NewFormaCommand(newForma, &config.FormaCommandConfig{}, pkgmodel.CommandApply, newFormaResourceUpdates, "")

		cfg := config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}

		_, err = m.ApplyForma(&newFormaCommand.Forma, &cfg, "test")
		assert.Error(t, err)

		var conflictErr apimodel.FormaConflictingCommandsError
		if errors.As(err, &conflictErr) {
			assert.Equal(t, 2, len(conflictErr.ConflictingCommands))
			assert.Equal(t, 3, len(conflictErr.ConflictingCommands[0].ResourceUpdates))
			assert.Equal(t, 1, len(conflictErr.ConflictingCommands[1].ResourceUpdates))
		} else {
			t.Errorf("Expected a ConflctingResourcesError, got: %T", err)
		}
	})
}
