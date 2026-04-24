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

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestHostsOnInvertDestroyOrder verifies that when Schema.Hints["VpcId"].HostsOn=true
// on FakeAWS::EC2::VPCCidrBlock, destroying a stack containing a VPC and a CIDR block
// (where CIDR's VpcId references the VPC) produces the inverted delete order:
// VPC deletes BEFORE CIDR block (instead of usual reverse-construction "child first").
//
// Without HostsOn, the destroy order is: cidr.delete → vpc.delete (reverse construction).
// With HostsOn on VpcId, the destroy order is: vpc.delete → cidr.delete (inverted).
func TestHostsOnInvertDestroyOrder(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Create resource overrides for successful Create and Delete operations.
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "native-" + request.Label,
					ResourceProperties: request.Properties,
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   "{}",
				}, nil
			},
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "delete-123",
					NativeID:        request.NativeID,
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Build the forma with VPC and CIDR block.
		// The CIDR block's VpcId is a plain string matching the VPC's Id.
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{Label: "test"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:   "vpc",
					Type:    "FakeAWS::EC2::VPC",
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Schema:  pkgmodel.Schema{Identifier: "Id", Fields: []string{"Id", "CidrBlock"}},
					Properties: json.RawMessage(`{
						"Id": "vpc-1",
						"CidrBlock": "10.0.0.0/16"
					}`),
				},
				{
					Label:   "cidr",
					Type:    "FakeAWS::EC2::VPCCidrBlock",
					Stack:   "test",
					Target:  "provider",
					Managed: true,
					Schema: pkgmodel.Schema{
						Identifier: "Id",
						Fields:     []string{"Id", "CidrBlock", "VpcId"},
						// This hint indicates that VpcId "hosts" a dependency on the VPC resource,
						// which should invert the delete order during destroy.
						// The plugin schema (in fake_aws.go) has the same hint.
						Hints: map[string]pkgmodel.FieldHint{
							"VpcId": {HostsOn: true},
						},
					},
					// Simple properties with VpcId matching the VPC's Id
					Properties: json.RawMessage(`{
						"Id": "cidr-1",
						"CidrBlock": "10.0.1.0/24",
						"VpcId": "vpc-1"
					}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "provider",
					Namespace: "FakeAWS",
					Config:    json.RawMessage(`{"region":"us-east-1"}`),
				},
			},
		}

		// Step 1: Apply the forma
		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id")
		require.NoError(t, err, "ApplyForma should not error")

		// Wait for apply to complete
		assert.Eventually(t, func() bool {
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "apply should complete")

		// Verify both resources exist in the datastore
		resources, err := m.Datastore.LoadResourcesByStack("test")
		require.NoError(t, err)
		require.Len(t, resources, 2, "should have vpc and cidr resources")

		// Step 2: Destroy the forma (all resources in the stack are removed)
		_, err = m.DestroyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client-id")
		require.NoError(t, err, "DestroyForma should not error")

		// Wait for destroy to complete
		assert.Eventually(t, func() bool {
			cmds, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			// Should have exactly 2 commands: apply and destroy
			return len(cmds) == 2 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond, "destroy should complete")

		// Step 3: Load the destroy command and verify the resource update order
		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)

		var destroyCmd *forma_command.FormaCommand
		for _, cmd := range cmds {
			if cmd.Command == pkgmodel.CommandDestroy {
				destroyCmd = cmd
				break
			}
		}
		require.NotNil(t, destroyCmd, "should have a destroy command")
		require.Len(t, destroyCmd.ResourceUpdates, 2, "destroy command should have 2 resource updates")

		// Find ModifiedTs for each resource
		modifiedTsOfResource := func(label string) time.Time {
			for _, ru := range destroyCmd.ResourceUpdates {
				if ru.DesiredState.Label == label {
					return ru.ModifiedTs
				}
			}
			t.Fatalf("no ResourceUpdate for %q", label)
			return time.Time{}
		}

		vpcModifiedTs := modifiedTsOfResource("vpc")
		cidrModifiedTs := modifiedTsOfResource("cidr")

		// The key assertion: with HostsOn on VpcId, vpc.delete should complete BEFORE cidr.delete
		// (inverted from the normal reverse-construction order).
		assert.True(t,
			vpcModifiedTs.Before(cidrModifiedTs),
			"vpc.delete must complete before cidr.delete under HostsOn annotation (vpc=%v cidr=%v)",
			vpcModifiedTs, cidrModifiedTs)

		// Also verify that the state is Success (not Failed or other terminal states)
		for _, ru := range destroyCmd.ResourceUpdates {
			assert.Equal(t, resource_update.ResourceUpdateStateSuccess, ru.State,
				"ResourceUpdate for %q should be successful", ru.DesiredState.Label)
		}
	})
}
