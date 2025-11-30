// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestMetastructure_Stats(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.Resource.Label == "test-resource-fail" {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusFailure,
						RequestID:       "1234",
						NativeID:        "5678",
						ResourceType:    request.Resource.Type,
						StatusMessage:   "Simulated failure",
					}}, nil
				} else {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "1234",
						NativeID:        "5678",
						ResourceType:    request.Resource.Type,
					}}, nil
				}
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
					ResourceType:    request.ResourceType,
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		formaInitial := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Managed:    true,
				},
				{
					Label:      "test-resource-fail",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Target:     "test-target",
					Managed:    true,
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		_, err = m.ApplyForma(formaInitial, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client-id")

		assert.NoError(t, err)

		time.Sleep(2 * time.Second)

		stats, err := m.Stats()
		assert.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, stats.Version, formae.Version)
		assert.Equal(t, stats.AgentID, m.AgentID)
		assert.Equal(t, stats.Clients, 1)

		// There should be 2 commands: 1 apply and 1 destroy
		assert.Equal(t, 1, len(stats.Commands))
		assert.Equal(t, 1, stats.Commands[string(pkgmodel.CommandApply)])
		assert.Equal(t, 0, stats.Commands[string(pkgmodel.CommandDestroy)])

		assert.Equal(t, 1, len(stats.States))
		assert.Equal(t, 0, stats.States[string(forma_command.CommandStateSuccess)])
		assert.Equal(t, 1, stats.States[string(forma_command.CommandStateFailed)])

		assert.Equal(t, 1, stats.Stacks)
		assert.Equal(t, 1, stats.ManagedResources)
		assert.Equal(t, 0, stats.UnmanagedResources)

		assert.Equal(t, 1, stats.Targets)

		assert.Equal(t, 1, len(stats.ResourceTypes))

		_, err = m.DestroyForma(formaInitial, &config.FormaCommandConfig{}, "test-client-id2")

		assert.NoError(t, err)

		time.Sleep(2 * time.Second)

		stats, err = m.Stats()
		assert.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, stats.Version, formae.Version)
		assert.Equal(t, stats.AgentID, m.AgentID)
		assert.Equal(t, stats.Clients, 2)

		// There should be 2 commands: 1 apply and 1 destroy
		assert.Equal(t, 2, len(stats.Commands))
		assert.Equal(t, 1, stats.Commands[string(pkgmodel.CommandApply)])
		assert.Equal(t, 1, stats.Commands[string(pkgmodel.CommandDestroy)])

		assert.Equal(t, 2, len(stats.States))
		assert.Equal(t, 1, stats.States[string(forma_command.CommandStateSuccess)])
		assert.Equal(t, 1, stats.States[string(forma_command.CommandStateFailed)])

		assert.Equal(t, 0, stats.Stacks)
		assert.Equal(t, 0, stats.ManagedResources)
		assert.Equal(t, 0, stats.UnmanagedResources)

		assert.Equal(t, 1, stats.Targets)

		assert.Equal(t, 1, len(stats.ResourceErrors))
		assert.Contains(t, stats.ResourceErrors, "Simulated failure")
	})
}
