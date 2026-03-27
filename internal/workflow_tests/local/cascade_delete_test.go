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
		assert.Equal(t, "parent-vpc", childUpdate.CascadeSource, "Child cascade source should be parent resource label")

		// Both should be delete operations
		assert.Equal(t, apimodel.OperationDelete, parentUpdate.Operation)
		assert.Equal(t, apimodel.OperationDelete, childUpdate.Operation)
	})
}

func TestDestroyWithCascade_SimulateShowsCascadeTargets(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Apply forma A — a "provider" target + a "cluster" resource
		providerForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "provider-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "cluster",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"BucketName":"my-cluster"}`),
					Stack:      "provider-stack",
					Target:     "provider",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "provider"},
			},
		}

		_, err = m.ApplyForma(providerForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Step 2: Wait for apply to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) == 0 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Provider forma should be applied")

		// Step 3: Get the cluster's KSUID from the DB
		clusterResources, err := m.Datastore.LoadResourcesByStack("provider-stack")
		require.NoError(t, err)
		require.Len(t, clusterResources, 1)
		clusterKsuid := clusterResources[0].Ksuid

		// Step 4: Directly insert an external "consumer" target with config containing $ref to the cluster KSUID
		consumerConfig := fmt.Sprintf(`{"endpoint":{"$ref":"formae://%s#/BucketName","$value":"my-cluster"}}`, clusterKsuid)
		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:  "consumer",
			Config: json.RawMessage(consumerConfig),
		})
		require.NoError(t, err)

		// Step 5: Simulate destroying forma A
		resp, err := m.DestroyForma(providerForma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test")
		require.NoError(t, err)

		// Step 6: Assert — simulation response includes a target update for "consumer" with IsCascade=true
		assert.True(t, resp.Simulation.ChangesRequired, "Simulation should indicate changes required")

		var consumerTargetUpdate *apimodel.TargetUpdate
		for i := range resp.Simulation.Command.TargetUpdates {
			tu := &resp.Simulation.Command.TargetUpdates[i]
			if tu.TargetLabel == "consumer" {
				consumerTargetUpdate = tu
			}
		}

		require.NotNil(t, consumerTargetUpdate, "Should have a target update for 'consumer'")
		assert.True(t, consumerTargetUpdate.IsCascade, "Consumer target should be marked as cascade")
		assert.Equal(t, "cluster", consumerTargetUpdate.CascadeSource, "Consumer cascade source should be cluster resource label")
		assert.Equal(t, apimodel.OperationDelete, consumerTargetUpdate.Operation, "Consumer target should be deleted")
	})
}

func TestDestroyWithCascade_CascadeDeletesDependentTargetAndResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Apply forma A — provider target + cluster resource
		providerForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "provider-stack"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "cluster",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"BucketName":"my-cluster"}`),
					Stack:      "provider-stack",
					Target:     "provider",
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "provider"},
			},
		}

		_, err = m.ApplyForma(providerForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Step 2: Wait for apply to complete, get cluster KSUID
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) == 0 {
				return false
			}
			return fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond, "Provider forma should be applied")

		clusterResources, err := m.Datastore.LoadResourcesByStack("provider-stack")
		require.NoError(t, err)
		require.Len(t, clusterResources, 1)
		clusterKsuid := clusterResources[0].Ksuid

		// Step 3: Directly insert external "consumer" target with $ref to cluster in config
		consumerConfig := fmt.Sprintf(`{"endpoint":{"$ref":"formae://%s#/BucketName","$value":"my-cluster"}}`, clusterKsuid)
		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:  "consumer",
			Config: json.RawMessage(consumerConfig),
		})
		require.NoError(t, err)

		// Step 4: Directly insert a "deployment" resource on the consumer target
		deploymentResource := &pkgmodel.Resource{
			NativeID:   "deployment-native-id",
			Label:      "deployment",
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"BucketName":"deployment-bucket"}`),
			Stack:      "consumer-stack",
			Target:     "consumer",
			Managed:    true,
		}
		_, err = m.Datastore.StoreResource(deploymentResource, "test-cmd")
		require.NoError(t, err)

		// Verify consumer target and deployment exist
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget)

		consumerResources, err := m.Datastore.LoadResourcesByStack("consumer-stack")
		require.NoError(t, err)
		require.Len(t, consumerResources, 1, "Should have the deployment resource")

		// Step 5: Actually destroy forma A (not simulate)
		_, err = m.DestroyForma(providerForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Step 6: Wait for destroy to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}
			for _, fa := range fas {
				if fa.Command == pkgmodel.CommandDestroy && fa.State == forma_command.CommandStateSuccess {
					return true
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond, "Destroy command should succeed")

		// Step 7: Assert — consumer target is deleted, deployment resource is deleted
		consumerTarget, err = m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		assert.Nil(t, consumerTarget, "Consumer target should be deleted")

		consumerResources, err = m.Datastore.LoadResourcesByStack("consumer-stack")
		require.NoError(t, err)
		assert.Empty(t, consumerResources, "Deployment resource should be deleted")
	})
}
