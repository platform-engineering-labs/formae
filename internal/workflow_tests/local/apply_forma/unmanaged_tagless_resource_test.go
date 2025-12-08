// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyForma_ReconcileFormaContainingUnmanagedTaglessResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// VPCGatewayAttachment resources don't support tags - they only have VpcId and InternetGatewayId
		resourceProperties := json.RawMessage(`{
			"VpcId": "vpc-12345",
			"InternetGatewayId": "igw-67890"
		}`)

		updateCallCount := 0

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				// Create should fail because the resource already exists (was discovered)
				return nil, fmt.Errorf("resource already exists")
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// Read returns the current state - identical properties
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   string(resourceProperties),
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				// Update should NOT be called for tag-less resources with no property changes
				// If this is called, the test should fail
				updateCallCount++
				t.Errorf("Update should not be called for tag-less resources with identical properties")
				return nil, fmt.Errorf("update should not be called")
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Create the unmanaged resource as it would be discovered
		unmanagedResource := pkgmodel.Resource{
			Label:  "my-vpc-gateway-attachment",
			Type:   "FakeAWS::EC2::VPCGatewayAttachment",
			Stack:  "$unmanaged",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "VpcId", // VpcId acts as the identifier
				// NOTE: No Tags field - this resource type doesn't support tags
				Fields: []string{"VpcId", "InternetGatewayId"},
			},
			Properties: resourceProperties,
			NativeID:   "vpc-12345|igw-67890", // Composite ID
			Managed:    false,
		}

		// Store the unmanaged resource in the $unmanaged stack (as discovery would)
		unmanagedStack := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "$unmanaged"}},
			Resources: []pkgmodel.Resource{unmanagedResource},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "FakeAWS"}},
		}
		_, err = m.Datastore.StoreStack(unmanagedStack, "discovery-command-1")
		require.NoError(t, err, "Failed to store unmanaged stack")

		// Now the user wants to bring this resource under management in their "infrastructure" stack
		// with IDENTICAL properties (no changes)
		managedResource := pkgmodel.Resource{
			Label:  "my-vpc-gateway-attachment",
			Type:   "FakeAWS::EC2::VPCGatewayAttachment",
			Stack:  "infrastructure", // Moving from $unmanaged to infrastructure
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "VpcId",
				Fields:     []string{"VpcId", "InternetGatewayId"},
			},
			Properties: resourceProperties, // IDENTICAL properties
			Managed:    true,
		}

		forma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
			Resources: []pkgmodel.Resource{managedResource},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "FakeAWS"}},
		}

		// Apply the forma to bring the resource under management
		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}, "test-client-id")
		require.NoError(t, err, "ApplyForma should not return an error")

		// Wait for the command to complete successfully
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				t.Logf("Error loading commands: %v", err)
				return false
			}
			if len(commands) == 0 {
				t.Logf("No commands found yet")
				return false
			}
			t.Logf("Command state: %s", commands[0].State)
			return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, 50*time.Second, 500*time.Millisecond, "Command should complete successfully")

		// Verify the command executed correctly
		commands, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Equal(t, 1, len(commands), "Should have exactly one command")
		require.Equal(t, forma_command.CommandStateSuccess, commands[0].State, "Command should be successful")
		require.Equal(t, 1, len(commands[0].ResourceUpdates), "Should have exactly one resource update")

		// Get the resource from the database
		actualKsuidURI := commands[0].ResourceUpdates[0].Resource.URI()
		fromDb, err := m.Datastore.LoadResource(actualKsuidURI)
		require.NoError(t, err, "Should be able to load resource from database")

		assert.True(t, fromDb.Managed, "Resource should be marked as managed")
		assert.Equal(t, "infrastructure", fromDb.Stack, "Resource should be in infrastructure stack")
		assert.JSONEq(t, string(resourceProperties), string(fromDb.Properties),
			"Properties should remain identical - no tags should be added")
		assert.Equal(t, 0, updateCallCount,
			"Plugin Update should not be called for tag-less resources with identical properties")
		assert.NotEmpty(t, fromDb.NativeID, "NativeID should be populated")
		assert.Equal(t, "vpc-12345|igw-67890", fromDb.NativeID,
			"NativeID should be preserved from the original unmanaged resource")
	})
}

func TestApplyForma_ReconcileFormaWithExistingStackAndUnmanagedTaglessResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		resourceProperties := json.RawMessage(`{
			"VpcId": "vpc-12345",
			"InternetGatewayId": "igw-67890"
		}`)

		otherResourceProperties := json.RawMessage(`{
			"Name": "my-other-resource"
		}`)

		updateCallCount := 0

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				// Allow the initial "other" resource to be created
				if request.Resource.Type == "FakeAWS::Other::Resource" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusSuccess,
							NativeID:        "other-resource-id",
						},
					}, nil
				}
				// VPCGatewayAttachment should fail (it already exists as unmanaged)
				return nil, fmt.Errorf("resource already exists")
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				// Return the appropriate resource based on type
				if request.ResourceType == "FakeAWS::EC2::VPCGatewayAttachment" {
					return &resource.ReadResult{
						ResourceType: request.ResourceType,
						Properties:   string(resourceProperties),
					}, nil
				}
				// For other resources
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   string(otherResourceProperties),
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updateCallCount++
				t.Errorf("Update should not be called for tag-less resources with identical properties")
				return nil, fmt.Errorf("update should not be called")
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err, "Failed to create metastructure")

		// Step 1: Create initial infrastructure stack with another resource
		otherResource := pkgmodel.Resource{
			Label:  "my-other-resource",
			Type:   "FakeAWS::Other::Resource",
			Stack:  "infrastructure",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "Name",
				Fields:     []string{"Name"},
			},
			Properties: otherResourceProperties,
			NativeID:   "other-resource-id",
			Managed:    true,
		}

		initialForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
			Resources: []pkgmodel.Resource{otherResource},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "FakeAWS"}},
		}

		_, err = m.ApplyForma(initialForma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}, "test-client-id-1")
		require.NoError(t, err, "Failed to apply initial forma")

		// Wait for initial command to complete
		eventually := func(f func() bool, msg string) {
			assert.Eventually(t, f, 50*time.Second, 500*time.Millisecond, msg)
		}

		eventually(func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			return err == nil && len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
		}, "Initial command should complete successfully")

		// Step 2: Create the unmanaged VPCGatewayAttachment as it would be discovered
		unmanagedResource := pkgmodel.Resource{
			Label:  "my-vpc-gateway-attachment",
			Type:   "FakeAWS::EC2::VPCGatewayAttachment",
			Stack:  "$unmanaged",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "VpcId",
				Fields:     []string{"VpcId", "InternetGatewayId"},
			},
			Properties: resourceProperties,
			NativeID:   "vpc-12345|igw-67890",
			Managed:    false,
		}

		unmanagedStack := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "$unmanaged"}},
			Resources: []pkgmodel.Resource{unmanagedResource},
			Targets:   []pkgmodel.Target{}, // Target already exists from first forma
		}

		_, err = m.Datastore.StoreStack(unmanagedStack, "discovery-command-1")
		require.NoError(t, err, "Failed to store unmanaged stack")

		// Step 3: Now apply a forma that brings the unmanaged resource under management
		// The infrastructure stack NOW EXISTS with other resources
		managedResource := pkgmodel.Resource{
			Label:  "my-vpc-gateway-attachment",
			Type:   "FakeAWS::EC2::VPCGatewayAttachment",
			Stack:  "infrastructure",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "VpcId",
				Fields:     []string{"VpcId", "InternetGatewayId"},
			},
			Properties: resourceProperties,
			Managed:    true,
		}

		formaWithUnmanagedResource := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "infrastructure"}},
			Resources: []pkgmodel.Resource{
				otherResource,   // Keep the existing resource
				managedResource, // Add the unmanaged resource under management
			},
			Targets: []pkgmodel.Target{}, // Target already exists from first forma
		}

		_, err = m.ApplyForma(formaWithUnmanagedResource, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: false,
		}, "test-client-id-2")
		require.NoError(t, err, "ApplyForma should not return an error")

		// Step 4: Wait for the command to complete successfully
		eventually(func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(commands) != 2 {
				return false
			}
			return commands[1].State == forma_command.CommandStateSuccess
		}, "Second command should complete successfully")

		// Step 5: Verify the resource was brought under management correctly
		commands, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Equal(t, 2, len(commands), "Should have exactly two commands")
		require.Equal(t, forma_command.CommandStateSuccess, commands[1].State, "Second command should be successful")

		// The second command should have one resource update for the VPC Gateway Attachment
		secondCommand := commands[1]
		t.Logf("Second command has %d resource updates", len(secondCommand.ResourceUpdates))

		// Find the VPC Gateway Attachment resource update
		var vpcGatewayUpdate *resource_update.ResourceUpdate
		for i, ru := range secondCommand.ResourceUpdates {
			if ru.Resource.Label == "my-vpc-gateway-attachment" {
				vpcGatewayUpdate = &secondCommand.ResourceUpdates[i]
				break
			}
		}

		require.NotNil(t, vpcGatewayUpdate, "Should have resource update for VPC Gateway Attachment")
		assert.Equal(t, resource_update.OperationUpdate, vpcGatewayUpdate.Operation,
			"Operation should be Update for bringing unmanaged resource under management")

		// Update should not have been called
		assert.Equal(t, 0, updateCallCount, "Plugin Update should not be called for tag-less resources with identical properties")
	})
}
