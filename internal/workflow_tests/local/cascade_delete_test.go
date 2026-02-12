// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetastructure_DestroyWithCascade_SimulateShowsCascades(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Create the parent resource (VPC)
		parentForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "parent-vpc",
					Type:       "FakeAWS::EC2::VPC",
					Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
					Stack:      "test-stack",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "test-target"},
			},
		}

		_, err = m.ApplyForma(parentForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for parent to be created
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) == 0 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Parent VPC should be created")

		// Get the parent's KSUID
		parentResources, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)
		require.Len(t, parentResources, 1)
		parentKsuid := parentResources[0].Ksuid

		// Step 2: Directly insert a child resource that references the parent via $ref
		childProperties := fmt.Sprintf(`{"VpcId":{"$ref":"formae://%s#/VpcId","$value":"vpc-123"},"CidrBlock":"10.0.1.0/24"}`, parentKsuid)
		childResource := &pkgmodel.Resource{
			NativeID:   "subnet-child-native",
			Label:      "child-subnet",
			Type:       "FakeAWS::EC2::Subnet",
			Properties: json.RawMessage(childProperties),
			Stack:      "test-stack",
			Target:     "test-target",
			Managed:    true,
		}
		_, err = m.Datastore.StoreResource(childResource, "test-cmd")
		require.NoError(t, err)

		// Verify both resources exist
		allResources, err := m.Datastore.LoadResourcesByStack("test-stack")
		require.NoError(t, err)
		require.Len(t, allResources, 2, "Should have both VPC and subnet")

		// Step 3: Simulate destroying ONLY the parent - check the simulation response
		destroyParentOnlyForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "parent-vpc",
					Type:       "FakeAWS::EC2::VPC",
					Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
					Stack:      "test-stack",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "test-target"},
			},
		}

		// Call with Simulate: true to get the simulation response
		resp, err := m.DestroyForma(destroyParentOnlyForma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test")
		require.NoError(t, err)

		// Step 4: Verify cascade detection in the simulation response
		assert.True(t, resp.Simulation.ChangesRequired, "Simulation should indicate changes required")
		assert.Len(t, resp.Simulation.Command.ResourceUpdates, 2, "Should have 2 resource updates (parent + cascade child)")

		// Find the cascade delete in the response
		var parentUpdate, childUpdate *apimodel.ResourceUpdate
		for i := range resp.Simulation.Command.ResourceUpdates {
			ru := &resp.Simulation.Command.ResourceUpdates[i]
			if ru.ResourceLabel == "parent-vpc" {
				parentUpdate = ru
			} else if ru.ResourceLabel == "child-subnet" {
				childUpdate = ru
			}
		}

		require.NotNil(t, parentUpdate, "Should have parent update")
		require.NotNil(t, childUpdate, "Should have child update")

		// Parent should NOT be a cascade
		assert.False(t, parentUpdate.IsCascade, "Parent should not be marked as cascade")
		assert.Empty(t, parentUpdate.CascadeSource, "Parent should not have cascade source")

		// Child should be a cascade with parent as source
		assert.True(t, childUpdate.IsCascade, "Child should be marked as cascade")
		assert.Equal(t, parentKsuid, childUpdate.CascadeSource, "Child cascade source should be parent KSUID")

		// Both should be delete operations
		assert.Equal(t, apimodel.OperationDelete, parentUpdate.Operation)
		assert.Equal(t, apimodel.OperationDelete, childUpdate.Operation)
	})
}
