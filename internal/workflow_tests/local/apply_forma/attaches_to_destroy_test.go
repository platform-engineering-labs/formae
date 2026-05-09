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

// TestAttachesToInvertDestroyOrder verifies that when Schema.Hints["VpcId"].AttachesTo=true
// on FakeAWS::EC2::VPCCidrBlock, destroying a stack containing a VPC and a CIDR block
// (where CIDR's VpcId references the VPC) produces the inverted delete order:
// VPC deletes BEFORE CIDR block (instead of usual reverse-construction "child first").
//
// Without AttachesTo, the destroy order is: cidr.delete → vpc.delete (reverse construction).
// With AttachesTo on VpcId, the destroy order is: vpc.delete → cidr.delete (inverted).
func TestAttachesToInvertDestroyOrder(t *testing.T) {
	// Deterministic KSUID for the VPC so the CIDR block can $ref it.
	// Without a $ref, the resolver sees no edge between the resources
	// and the DAG builder has no ref on which to apply the AttachesTo hint.
	const vpcKsuid = "2MiD2rA1SJbLMGZgTL0hCxjkjjr"

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
				// Return the same Properties that were created; this lets the
				// resolver find the VPC's Id when the CIDR block $refs it.
				switch request.ResourceType {
				case "FakeAWS::EC2::VPC":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"Id":"vpc-1","CidrBlock":"10.0.0.0/16"}`,
					}, nil
				case "FakeAWS::EC2::VPCCidrBlock":
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   `{"Id":"cidr-1","CidrBlock":"10.0.1.0/24","VpcId":"vpc-1"}`,
					}, nil
				}
				return &resource.ReadResult{ResourceType: request.ResourceType, Properties: "{}"}, nil
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
					Ksuid:   vpcKsuid,
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
						// AttachesTo semantically inverts destroy-edge direction:
						// cidr.delete waits for vpc.delete instead of the default
						// reverse-construction order (vpc.delete waits for cidr.delete).
						// The plugin schema in fake_aws.go carries the same hint.
						Hints: map[string]pkgmodel.FieldHint{
							"VpcId": {AttachesTo: true},
						},
					},
					// VpcId is a $ref so the resolver produces an edge the DAG
					// builder can apply the AttachesTo hint to. A plain string here
					// would produce no edge and the test would assert nothing useful.
					Properties: json.RawMessage(fmt.Sprintf(`{
						"Id": "cidr-1",
						"CidrBlock": "10.0.1.0/24",
						"VpcId": {"$ref":"formae://%s#/Id","$value":"vpc-1"}
					}`, vpcKsuid)),
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

		// The key assertion: with AttachesTo on VpcId, vpc.delete should complete BEFORE cidr.delete
		// (inverted from the normal reverse-construction order).
		assert.True(t,
			vpcModifiedTs.Before(cidrModifiedTs),
			"vpc.delete must complete before cidr.delete under AttachesTo annotation (vpc=%v cidr=%v)",
			vpcModifiedTs, cidrModifiedTs)

		// Also verify that the state is Success (not Failed or other terminal states)
		for _, ru := range destroyCmd.ResourceUpdates {
			assert.Equal(t, resource_update.ResourceUpdateStateSuccess, ru.State,
				"ResourceUpdate for %q should be successful", ru.DesiredState.Label)
		}
	})
}
