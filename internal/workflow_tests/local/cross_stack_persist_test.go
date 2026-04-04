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
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetastructure_SuccessfulCreatePersistedInFailedCommand verifies that when
// a command fails (some resources fail to create), other successfully-created
// resources are still persisted to inventory.
//
// This reproduces an issue found by property tests where the command response
// shows creates as Success, but the resources don't appear in inventory.
func TestMetastructure_SuccessfulCreatePersistedInFailedCommand(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Fail creates for "res-b" to make the command fail while
		// "res-a" succeeds.
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Label == "res-b" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
							ErrorCode:       resource.OperationErrorCodeAccessDenied,
							StatusMessage:   "injected failure",
						},
					}, nil
				}
				return nil, nil // default behavior
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "res-a",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"BucketName":"res-a"}`),
					Stack:      "test-stack",
					Target:     "test-target1",
				},
				{
					Label:      "res-b",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"BucketName":"res-b"}`),
					Stack:      "test-stack",
					Target:     "test-target1",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "test-target1", Namespace: "test-namespace1"},
			},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for command to complete (should fail due to res-b)
		var finalCmd *forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			cmds, _ := m.Datastore.LoadFormaCommands()
			for _, cmd := range cmds {
				if cmd.State == forma_command.CommandStateFailed {
					finalCmd = cmd
					return true
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond, "Command should fail due to res-b")

		require.NotNil(t, finalCmd, "Should have a failed command")

		// Verify command response
		var resAState, resBState string
		for _, ru := range finalCmd.ResourceUpdates {
			t.Logf("  ru: label=%s op=%s state=%s nativeID=%s",
				ru.DesiredState.Label, ru.Operation, ru.State, ru.DesiredState.NativeID)
			switch ru.DesiredState.Label {
			case "res-a":
				resAState = string(ru.State)
			case "res-b":
				resBState = string(ru.State)
			}
		}
		assert.Equal(t, "Success", resAState, "res-a should have succeeded")
		assert.Equal(t, "Failed", resBState, "res-b should have failed")

		// KEY CHECK: Is res-a persisted in inventory despite the command failing?
		// Wait a moment for async persistence to complete.
		time.Sleep(500 * time.Millisecond)

		resources, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)

		var foundResA bool
		for _, r := range resources {
			t.Logf("  inventory: label=%s type=%s nativeID=%s", r.Label, r.Type, r.NativeID)
			if r.Label == "res-a" {
				foundResA = true
			}
		}

		assert.True(t, foundResA,
			"res-a should be in inventory even though command failed (persistence is per-resource, not per-command)")
	})
}
