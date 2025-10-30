// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestGenerateResourceUpdatesForDestroy(t *testing.T) {
	tests := []struct {
		name           string
		forma          *pkgmodel.Forma
		existingStacks []*pkgmodel.Stack
		setupDatastore func(*mockDatastore)
		expectedCount  int
		expectedError  string
		validate       func(t *testing.T, updates []ResourceUpdate)
	}{
		{
			name: "destroy existing resources successfully",
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "test-stack"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:      "test-instance",
						Type:       "AWS::EC2::Instance",
						Stack:      "test-stack",
						Target:     "test-target",
						Properties: json.RawMessage(`{"InstanceType": "t2.micro"}`),
						Managed:    true,
					},
					{
						Label:      "test-vpc",
						Type:       "AWS::EC2::VPC",
						Stack:      "test-stack",
						Target:     "test-target",
						Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
						Managed:    true,
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				existingStack := &pkgmodel.Forma{
					Stacks: []pkgmodel.Stack{
						{Label: "test-stack"},
					},
					Resources: []pkgmodel.Resource{
						{
							Label:      "test-instance",
							Type:       "AWS::EC2::Instance",
							Stack:      "test-stack",
							Target:     "test-target",
							Properties: json.RawMessage(`{"InstanceType": "t2.micro"}`),
							Managed:    true,
						},
						{
							Label:      "test-vpc",
							Type:       "AWS::EC2::VPC",
							Stack:      "test-stack",
							Target:     "test-target",
							Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
							Managed:    true,
						},
					},
				}
				_, err := ds.StoreStack(existingStack, "test-command-id")
				if err != nil {
					panic(err)
				}
			},
			expectedCount: 2,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 2)

				// Check that both resources have delete operations
				for _, update := range updates {
					assert.Equal(t, OperationDelete, update.Operation)
					assert.Equal(t, ResourceUpdateStateNotStarted, update.State)
					assert.Equal(t, "test-stack", update.StackLabel)
				}

				// Check specific resources
				resourceLabels := make(map[string]bool)
				for _, update := range updates {
					resourceLabels[update.Resource.Label] = true
				}
				assert.True(t, resourceLabels["test-instance"])
				assert.True(t, resourceLabels["test-vpc"])
			},
		},
		{
			name: "stack does not exist - nothing to destroy",
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "non-existent-stack"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:  "test-instance",
						Type:   "AWS::EC2::Instance",
						Stack:  "non-existent-stack",
						Target: "test-target",
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't do anything - stack won't exist
			},
			expectedCount: 0,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Empty(t, updates)
			},
		},
		{
			name: "res stack does not exist - should return error",
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					// Note: Stack is NOT defined here, only referenced by resource
				},
				Resources: []pkgmodel.Resource{
					{
						Label:  "test-instance",
						Type:   "AWS::EC2::Instance",
						Stack:  "non-existent-res-stack",
						Target: "test-target",
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't persist anything - res stack won't exist
			},
			expectedError: "stack res provided: non-existent-res-stack does not exist",
		},
		{
			name: "res stack exists in database - should succeed",
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					// Note: Stack is NOT defined here, only referenced by resource
				},
				Resources: []pkgmodel.Resource{
					{
						Label:  "test-instance",
						Type:   "AWS::EC2::Instance",
						Stack:  "existing-res-stack",
						Target: "test-target",
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Persist the stack in database so reference is valid
				existingStack := &pkgmodel.Forma{
					Stacks: []pkgmodel.Stack{
						{Label: "existing-res-stack"},
					},
					Resources: []pkgmodel.Resource{
						{
							Label:   "test-instance",
							Type:    "AWS::EC2::Instance",
							Stack:   "existing-res-stack",
							Target:  "test-target",
							Managed: true,
						},
					},
				}
				_, err := ds.StoreStack(existingStack, "test-command-setup")
				if err != nil {
					panic(err)
				}
			},
			expectedCount: 1,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 1)
				assert.Equal(t, "test-instance", updates[0].Resource.Label)
				assert.Equal(t, OperationDelete, updates[0].Operation)
				assert.Equal(t, "existing-res-stack", updates[0].StackLabel)
			},
		},
		{
			name: "partial resource match - only matching resources destroyed",
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "test-stack"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:   "test-instance",
						Type:    "AWS::EC2::Instance",
						Stack:   "test-stack",
						Target:  "test-target",
						Managed: true,
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				existingStack := &pkgmodel.Forma{
					Stacks: []pkgmodel.Stack{
						{Label: "test-stack"},
					},
					Resources: []pkgmodel.Resource{
						{
							Label:   "test-instance",
							Type:    "AWS::EC2::Instance",
							Stack:   "test-stack",
							Target:  "test-target",
							Managed: true,
						},
						{
							Label:   "other-instance",
							Type:    "AWS::EC2::Instance",
							Stack:   "test-stack",
							Target:  "test-target",
							Managed: true,
						},
					},
				}
				_, err := ds.StoreStack(existingStack, "test-command-id")
				if err != nil {
					panic(err)
				}
			},
			expectedCount: 1,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 1)
				assert.Equal(t, "test-instance", updates[0].Resource.Label)
				assert.Equal(t, OperationDelete, updates[0].Operation)
			},
		},
		{
			name: "multiple stacks - destroy resources from each",
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "stack-1"},
					{Label: "stack-2"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:   "instance-1",
						Type:    "AWS::EC2::Instance",
						Stack:   "stack-1",
						Target:  "test-target",
						Managed: true,
					},
					{
						Label:   "instance-2",
						Type:    "AWS::EC2::Instance",
						Stack:   "stack-2",
						Target:  "test-target",
						Managed: true,
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				stack1 := &pkgmodel.Forma{
					Stacks: []pkgmodel.Stack{{Label: "stack-1"}},
					Resources: []pkgmodel.Resource{
						{
							Label:   "instance-1",
							Type:    "AWS::EC2::Instance",
							Stack:   "stack-1",
							Target:  "test-target",
							Managed: true,
						},
					},
				}
				stack2 := &pkgmodel.Forma{
					Stacks: []pkgmodel.Stack{{Label: "stack-2"}},
					Resources: []pkgmodel.Resource{
						{
							Label:   "instance-2",
							Type:    "AWS::EC2::Instance",
							Stack:   "stack-2",
							Target:  "test-target",
							Managed: true,
						},
					},
				}
				_, err := ds.StoreStack(stack1, "test-command-1")
				if err != nil {
					panic(err)
				}
				_, err = ds.StoreStack(stack2, "test-command-2")
				if err != nil {
					panic(err)
				}
			},
			expectedCount: 2,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 2)

				resourcesByStack := make(map[string]string)
				for _, update := range updates {
					resourcesByStack[update.Resource.Label] = update.StackLabel
				}

				assert.Equal(t, "stack-1", resourcesByStack["instance-1"])
				assert.Equal(t, "stack-2", resourcesByStack["instance-2"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds, _ := GetDeps(t)
			if tt.setupDatastore != nil {
				tt.setupDatastore(ds)
			}

			existingTargets := []*pkgmodel.Target{
				{
					Label:     "test-target",
					Config:    json.RawMessage(`{"Region": "us-west-2"}`),
					Namespace: "aws",
				},
			}

			updates, err := GenerateResourceUpdates(
				tt.forma,
				pkgmodel.CommandDestroy,
				pkgmodel.FormaApplyModeReconcile,
				FormaCommandSourceUser,
				existingTargets,
				ds,
				nil,
			)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, updates, tt.expectedCount)

			if tt.validate != nil {
				tt.validate(t, updates)
			}
		})
	}
}

func TestGenerateResourceUpdatesForDestroy_NewResourceUpdateForDestroy(t *testing.T) {
	// Test that the NewResourceUpdateForDestroy function is called correctly
	ds, _ := GetDeps(t)

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: "test-stack"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:      "test-instance",
				Type:       "AWS::EC2::Instance",
				Stack:      "test-stack",
				Target:     "test-target",
				Properties: json.RawMessage(`{"InstanceType": "t2.micro"}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "test-command-id")
	assert.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: "test-stack"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:      "test-instance",
				Type:       "AWS::EC2::Instance",
				Stack:      "test-stack",
				Target:     "test-target",
				Properties: json.RawMessage(`{"InstanceType": "t2.micro"}`),
			},
		},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForDestroy(
		forma,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)
	assert.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, OperationDelete, update.Operation)
	assert.Equal(t, "test-instance", update.Resource.Label)
	assert.Equal(t, "AWS::EC2::Instance", update.Resource.Type)
	assert.Equal(t, "test-stack", update.Resource.Stack)
	assert.Equal(t, "test-target", update.Resource.Target)
}

func TestGenerateResourceUpdatesForDestroy_WithDependencies(t *testing.T) {
	ds, _ := GetDeps(t)

	vpcKsuid := util.NewID()
	subnetKsuid := util.NewID()
	instanceKsuid := util.NewID()

	// Setup existing stack with VPC -> Subnet -> Instance dependency chain
	vpc := pkgmodel.Resource{
		Label:              "test-vpc",
		Type:               "AWS::EC2::VPC",
		Stack:              "infrastructure",
		Target:             "test-target",
		Ksuid:              vpcKsuid,
		Properties:         json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-12345"}`),
		Schema: pkgmodel.Schema{
			Identifier: "VpcId",
			Fields:     []string{"CidrBlock", "VpcId"},
		},
	}

	subnet := pkgmodel.Resource{
		Label:              "test-subnet",
		Type:               "AWS::EC2::Subnet",
		Stack:              "infrastructure",
		Target:             "test-target",
		Ksuid:              subnetKsuid,
		Properties:         fmt.Appendf(nil, `{"VpcId": {"$ref": "formae://%s#/VpcId", "$value": "vpc-12345"}, "CidrBlock": "10.0.1.0/24"}`, vpcKsuid),
		ReadOnlyProperties: json.RawMessage(`{"SubnetId": "subnet-12345"}`),
		Schema: pkgmodel.Schema{
			Identifier: "SubnetId",
			Fields:     []string{"VpcId", "CidrBlock", "SubnetId"},
		},
	}

	instance := pkgmodel.Resource{
		Label:      "test-instance",
		Type:       "AWS::EC2::Instance",
		Stack:      "infrastructure",
		Target:     "test-target",
		Ksuid:      instanceKsuid,
		Properties: fmt.Appendf(nil, `{"SubnetId": {"$ref": "formae://%s#/SubnetId", "$value": "subnet-12345"}, "InstanceType": "t2.micro"}`, subnetKsuid),
		Schema: pkgmodel.Schema{
			Identifier: "InstanceId",
			Fields:     []string{"SubnetId", "InstanceType", "InstanceId"},
		},
	}

	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{vpc, subnet, instance},
	}
	_, err := ds.StoreStack(existingStack, "test-command-id")
	assert.NoError(t, err)

	// Create destroy command for all resources
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{vpc, subnet, instance},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForDestroy(
		forma,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)
	assert.Len(t, updates, 3)

	// All should be delete operations
	for _, update := range updates {
		assert.Equal(t, OperationDelete, update.Operation)
		assert.Equal(t, ResourceUpdateStateNotStarted, update.State)
		assert.Equal(t, "infrastructure", update.StackLabel)
	}

	// Check that dependencies are properly preserved for destroy ordering
	updatesByLabel := make(map[string]ResourceUpdate)
	for _, update := range updates {
		updatesByLabel[update.Resource.Label] = update
	}

	// Instance should still have its dependency on subnet (for destroy, dependents are deleted first)
	instanceUpdate := updatesByLabel["test-instance"]
	expectedSubnetUri := pkgmodel.NewFormaeURI(subnetKsuid, "SubnetId")
	assert.Contains(t, instanceUpdate.RemainingResolvables, expectedSubnetUri)

	// Subnet should still have its dependency on VPC
	subnetUpdate := updatesByLabel["test-subnet"]
	expectedVpcUri := pkgmodel.NewFormaeURI(vpcKsuid, "VpcId")
	assert.Contains(t, subnetUpdate.RemainingResolvables, expectedVpcUri)

	// VPC should have no dependencies
	vpcUpdate := updatesByLabel["test-vpc"]
	assert.Empty(t, vpcUpdate.RemainingResolvables)
}

func TestGenerateResourceUpdatesForDestroy_WithCrossStackDependencies(t *testing.T) {
	ds, _ := GetDeps(t)

	vpcKsuid := util.NewID()
	instanceKsuid := util.NewID()

	// Setup VPC in network stack
	vpc := pkgmodel.Resource{
		Label:              "shared-vpc",
		Type:               "AWS::EC2::VPC",
		Stack:              "network",
		Target:             "test-target",
		Ksuid:              vpcKsuid,
		Properties:         json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-shared"}`),
	}

	networkStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "network"}},
		Resources: []pkgmodel.Resource{vpc},
	}
	_, err := ds.StoreStack(networkStack, "test-command-1")
	assert.NoError(t, err)

	// Setup instance in compute stack that depends on VPC
	instance := pkgmodel.Resource{
		Label:      "app-instance",
		Type:       "AWS::EC2::Instance",
		Stack:      "compute",
		Target:     "test-target",
		Ksuid:      instanceKsuid,
		Properties: fmt.Appendf(nil, `{"VpcId": {"$ref": "formae://%s#/VpcId", "$value": "vpc-shared"}, "InstanceType": "t2.micro"}`, vpcKsuid),
	}

	computeStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "compute"}},
		Resources: []pkgmodel.Resource{instance},
	}
	_, err = ds.StoreStack(computeStack, "test-command-2")
	assert.NoError(t, err)

	// Create destroy command for compute stack only
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "compute"}},
		Resources: []pkgmodel.Resource{instance},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForDestroy(
		forma,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)
	assert.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, OperationDelete, update.Operation)
	assert.Equal(t, "app-instance", update.Resource.Label)
	assert.Equal(t, "compute", update.StackLabel)

	// Cross-stack dependency should be preserved
	expectedVpcUri := pkgmodel.NewFormaeURI(vpcKsuid, "VpcId")
	assert.Contains(t, update.RemainingResolvables, expectedVpcUri)
}

func TestGenerateResourceUpdatesForDestroy_PartialResourcesWithDependencies(t *testing.T) {
	ds, _ := GetDeps(t)

	vpcKsuid := util.NewID()
	subnetKsuid := util.NewID()
	instanceKsuid := util.NewID()

	// Setup stack with multiple resources
	vpc := pkgmodel.Resource{
		Label:              "main-vpc",
		Type:               "AWS::EC2::VPC",
		Stack:              "infrastructure",
		Target:             "test-target",
		Ksuid:              vpcKsuid,
		Properties:         json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-main"}`),
	}

	subnet := pkgmodel.Resource{
		Label:      "main-subnet",
		Type:       "AWS::EC2::Subnet",
		Stack:      "infrastructure",
		Target:     "test-target",
		Ksuid:      subnetKsuid,
		Properties: fmt.Appendf(nil, `{"VpcId": {"$ref": "formae://%s#/VpcId", "$value": "vpc-main"}, "CidrBlock": "10.0.1.0/24"}`, vpcKsuid),
	}

	instance := pkgmodel.Resource{
		Label:      "web-instance",
		Type:       "AWS::EC2::Instance",
		Stack:      "infrastructure",
		Target:     "test-target",
		Ksuid:      instanceKsuid,
		Properties: fmt.Appendf(nil, `{"SubnetId": {"$ref": "formae://%s#/SubnetId", "$value": "subnet-main"}, "InstanceType": "t2.micro"}`, subnetKsuid),
	}

	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{vpc, subnet, instance},
	}
	_, err := ds.StoreStack(existingStack, "test-command-3")
	assert.NoError(t, err)

	// Create destroy command for only the instance (partial destroy)
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{instance}, // Only destroying the instance
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForDestroy(
		forma,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)
	assert.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, OperationDelete, update.Operation)
	assert.Equal(t, "web-instance", update.Resource.Label)
	assert.Equal(t, "infrastructure", update.StackLabel)

	// Dependency on subnet should be preserved (external to this destroy operation)
	expectedSubnetUri := pkgmodel.NewFormaeURI(subnetKsuid, "SubnetId")
	assert.Contains(t, update.RemainingResolvables, expectedSubnetUri)
}
