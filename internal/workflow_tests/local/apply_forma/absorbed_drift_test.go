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

// TestApplyForma_SoftReconcile_AbsorbedDriftDoesNotBlockNewResources verifies that when
// a resource was modified since the last reconcile (e.g. via patch or OOB change absorbed
// through extract), but the new forma already reflects the current state of that resource,
// the modification should not block a subsequent reconcile that adds new resources to the stack.
func TestApplyForma_SoftReconcile_AbsorbedDriftDoesNotBlockNewResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		resource1Reads := 0
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Label,
						ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if request.NativeID == "test-resource1" {
					var properties string
					switch resource1Reads {
					case 0:
						// First read (during initial reconcile): original state
						properties = `{"foo":"bar"}`
					default:
						// Subsequent reads (after patch): patched state
						properties = `{"foo":"baz"}`
					}
					resource1Reads++
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   properties,
					}, nil
				}
				if request.NativeID == "test-resource2" {
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"foo":"bar"}`,
					}, nil
				}
				// test-resource3
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"new"}`,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.NativeID,
						ResourceProperties: json.RawMessage(`{"foo":"baz"}`),
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Step 1: Initial reconcile to create two resources
		initial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack1"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource1",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
				{
					Label:      "test-resource2",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			initial,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "initial reconcile should complete")

		// Step 2: Patch resource1 (simulates an OOB change that was absorbed via extract)
		patch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack1"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource1",
					NativeID:   "test-resource1",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"baz"}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		_, err = m.ApplyForma(
			patch,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: false},
			"test-client-id")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return len(commands) == 2 && allCommandsSuccessful(commands)
		}, 5*time.Second, 100*time.Millisecond, "patch should complete")

		// Step 3: Reconcile with absorbed drift (resource1 now has patched properties)
		// PLUS a new resource3. This should succeed because the drift in resource1
		// is already reflected in the forma.
		reconcileWithNewResource := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack1"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource1",
					NativeID:   "test-resource1",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"baz"}`), // matches current state after patch
				},
				{
					Label:      "test-resource2",
					NativeID:   "test-resource2",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"bar"}`), // unchanged
				},
				{
					Label:      "test-resource3",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
					Properties: json.RawMessage(`{"foo":"new"}`), // new resource
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
					Config:    json.RawMessage(`{}`),
				},
			},
		}

		// This should NOT return a FormaReconcileRejectedError because the drift
		// from the patch is already absorbed in the forma.
		_, err = m.ApplyForma(
			reconcileWithNewResource,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		assert.NoError(t, err, "reconcile with absorbed drift and new resource should not be rejected")

		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return len(commands) == 3 && allCommandsSuccessful(commands)
		}, 5*time.Second, 100*time.Millisecond, "reconcile with new resource should complete")
	})
}
