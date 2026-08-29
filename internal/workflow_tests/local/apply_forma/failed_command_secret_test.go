// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
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

// failingConsumerOverrides mocks a consumer whose Create succeeds and whose
// Update always fails, so a command that plans a consumer update reaches a
// Failed terminal state after resolution has already substituted the live
// secret value into the consumer's desired state.
func failingConsumerOverrides(updateAttempts *atomic.Int32) *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			if req.ResourceType != "FakeAWS::S3::Bucket" {
				return nil, nil
			}
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "consumer-create-1",
					NativeID:        "bucket-native-1",
				},
			}, nil
		},
		Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
			if req.ResourceType != "FakeAWS::S3::Bucket" {
				return nil, nil
			}
			updateAttempts.Add(1)
			return nil, fmt.Errorf("consumer update rejected by the provider")
		},
	}
}

// A command that fails after resolution substituted a secret's live value into
// a consumer's desired state must leave no plaintext at rest. Terminal-state
// hashing runs for every final state, Failed included, so the failed command's
// stored rows must hold digests rather than the value the consumer was about
// to be given.
func TestApplyForma_FailedCommand_LeavesNoConsumerPlaintextAtRest(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secretV1 = "failed-command-secret-v1"
		const secretV2 = "failed-command-secret-v2"

		logCapture := test_helpers.SetupTestLogger()

		var updateAttempts atomic.Int32
		overrides := failingConsumerOverrides(&updateAttempts)

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		createForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), secretConsumer(stack, "my-secret")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(createForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		// Change only the secret's value. The consumer is planned because its
		// reference resolves from a moved source, its plugin is handed the new
		// value, and the plugin refuses, failing the command.
		rotateForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV2), secretConsumer(stack, "my-secret")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(rotateForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		var failedCmd *forma_command.FormaCommand
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply && c.ID != createCmd.ID {
				failedCmd = c
			}
		}
		require.NotNil(t, failedCmd, "the second apply command must exist")
		require.Greater(t, updateAttempts.Load(), int32(0),
			"precondition: the consumer's plugin must have been called with the resolved value")
		require.Equal(t, forma_command.CommandStateFailed, failedCmd.State,
			"precondition: the consumer's refusal must fail the command")

		// The failed command's stored rows carry no plaintext, from either
		// generation of the secret's value.
		assertNoPlaintextInResourceUpdates(t, m, failedCmd.ID, secretV2)
		assertNoPlaintextInResourceUpdates(t, m, failedCmd.ID, secretV1)

		// Nor does the command blob itself.
		blob, err := json.Marshal(failedCmd)
		require.NoError(t, err)
		assert.NotContains(t, string(blob), secretV2, "the failed command blob leaked the resolved secret")
		assert.NotContains(t, string(blob), secretV1, "the failed command blob leaked the prior secret")

		// Nor the resource rows.
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		for i := range resources {
			assert.NotContains(t, string(resources[i].Properties), secretV2,
				"resources.properties leaked plaintext for %s", resources[i].Label)
			assert.NotContains(t, string(resources[i].Properties), secretV1,
				"resources.properties leaked prior plaintext for %s", resources[i].Label)
		}

		// Nor the logs.
		for _, entry := range logCapture.GetEntries() {
			assert.NotContains(t, entry, secretV1, "log entry leaked the prior secret")
			assert.NotContains(t, entry, secretV2, "log entry leaked the resolved secret")
		}
	})
}

// Destroying a secret that a consumer still references fails the consumer, and
// the failed command must still leave no plaintext at rest: the consumer's
// stored desired state holds a digest, never the value it last resolved.
func TestApplyForma_DestroyedReferencedSecret_LeavesNoConsumerPlaintextAtRest(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const secretV1 = "destroyed-secret-v1"

		logCapture := test_helpers.SetupTestLogger()

		var calls atomic.Int32
		var props atomic.Value
		overrides := secretConsumerOverrides(&calls, &props)

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}

		createForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretResource(stack, "my-secret", secretV1), secretConsumer(stack, "my-secret")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(createForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		// Reconcile with the secret dropped from the forma. It is destroyed
		// while the consumer still references it.
		dropForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{secretConsumer(stack, "my-secret")},
			Targets:   targets,
		}
		_, err = m.ApplyForma(dropForma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		var dropCmd *forma_command.FormaCommand
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply && c.ID != createCmd.ID {
				dropCmd = c
			}
		}
		require.NotNil(t, dropCmd, "the second apply command must exist")

		assertNoPlaintextInResourceUpdates(t, m, dropCmd.ID, secretV1)

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		for i := range resources {
			assert.NotContains(t, string(resources[i].Properties), secretV1,
				"resources.properties leaked plaintext for %s", resources[i].Label)
		}

		for _, entry := range logCapture.GetEntries() {
			assert.NotContains(t, entry, secretV1, "log entry leaked the secret")
		}
	})
}
