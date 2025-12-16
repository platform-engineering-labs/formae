// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests

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

// Hard reconcile means applying a forma with the `--force` flag. Any changes to resources in the stack since the last
// reconcile, either from patches or external changes that were synced in, will be overwritten without warning.

func TestApplyForma_HardReconcile(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		reads := 0
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.Resource.Label,
						ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// Properties are the same for all resources on the first read
				if reads == 0 {
					reads++
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"foo":"bar"}`,
					}, nil
				}
				// Resource 1 has been patched so we return the updated properties
				var properties string
				if request.NativeID == "test-resource1" {
					properties = `{"foo":"baz"}`
				} else if request.NativeID == "test-resource2" {
					properties = `{"foo":"bar"}`
				}
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   properties,
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						ResourceProperties: json.RawMessage(`{"foo":"baz"}`),
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		// initial apply to create resources
		initial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"bar"}`),
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
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
		assert.NoError(t, err)

		var commands []*forma_command.FormaCommand
		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "first apply command should complete")

		// patch one of the resources
		patch := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
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
		assert.NoError(t, err)

		require.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			return len(commands) == 2 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "patch command should complete before submitting reconcile")

		// reconcile the stack to a new state, forcing a hard reconcile
		reconcile := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"snarf"}`),
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target1",
					Schema: pkgmodel.Schema{
						Fields: []string{"foo"},
					},
					Properties: json.RawMessage(`{"foo":"sandwich"}`),
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
			reconcile,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false, Force: true},
			"test-client-id")
		assert.NoError(t, err)

		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)

			return len(commands) == 3 && commands[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

	})
}
