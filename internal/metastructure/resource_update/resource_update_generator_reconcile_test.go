// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestGenerateResourceUpdatesForReconcile(t *testing.T) {
	tests := []struct {
		name           string
		forma          *pkgmodel.Forma
		mode           pkgmodel.FormaApplyMode
		existingStacks []*pkgmodel.Stack
		setupDatastore func(*mockDatastore)
		expectedCount  int
		expectedError  string
		validate       func(t *testing.T, updates []ResourceUpdate)
	}{
		{
			name: "create new stack - all resources should be created",
			mode: pkgmodel.FormaApplyModeReconcile,
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "new-stack"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:      "test-instance",
						Type:       "AWS::EC2::Instance",
						Stack:      "new-stack",
						Target:     "test-target",
						Properties: json.RawMessage(`{"InstanceType": "t2.micro"}`),
						Managed:    true,
					},
					{
						Label:      "test-vpc",
						Type:       "AWS::EC2::VPC",
						Stack:      "new-stack",
						Target:     "test-target",
						Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
						Managed:    true,
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't persist anything - stack doesn't exist yet
			},
			expectedCount: 2,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 2)

				// Check that both resources have create operations
				for _, update := range updates {
					assert.Equal(t, OperationCreate, update.Operation)
					assert.Equal(t, ResourceUpdateStateNotStarted, update.State)
					assert.Equal(t, "new-stack", update.StackLabel)
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
			name: "res stack does not exist - should return error",
			mode: pkgmodel.FormaApplyModeReconcile,
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					// Note: Stack is NOT defined here, only referenced by resource
				},
				Resources: []pkgmodel.Resource{
					{
						Label:   "test-instance",
						Type:    "AWS::EC2::Instance",
						Stack:   "non-existent-res-stack",
						Target:  "test-target",
						Managed: true,
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't persist anything - res stack won't exist
			},
			expectedError: "stack res provided: non-existent-res-stack does not exist",
		},
		{
			name: "res stack exists in database - should succeed and reconcile",
			mode: pkgmodel.FormaApplyModeReconcile,
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					// Note: Stack is NOT defined here, only referenced by resource
				},
				Resources: []pkgmodel.Resource{
					{
						Label:      "new-instance",
						Type:       "AWS::EC2::Instance",
						Stack:      "existing-res-stack",
						Target:     "test-target",
						Properties: json.RawMessage(`{"InstanceType": "t3.micro"}`),
						Managed:    true,
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
							Label:      "existing-instance",
							Type:       "AWS::EC2::Instance",
							Stack:      "existing-res-stack",
							Target:     "test-target",
							Properties: json.RawMessage(`{"InstanceType": "t2.micro"}`),
							Managed:    true,
						},
					},
				}
				_, err := ds.StoreStack(existingStack, "test-command-setup")
				if err != nil {
					panic(err)
				}
			},
			expectedCount: 2,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 2)

				// Should have one create (new-instance) and one delete (existing-instance)
				// because reconcile mode assumes Forma is complete
				operationsByType := make(map[OperationType]ResourceUpdate)
				for _, update := range updates {
					operationsByType[update.Operation] = update
					assert.Equal(t, "existing-res-stack", update.StackLabel)
				}

				createOp := operationsByType[OperationCreate]
				assert.Equal(t, "new-instance", createOp.Resource.Label)

				deleteOp := operationsByType[OperationDelete]
				assert.Equal(t, "existing-instance", deleteOp.Resource.Label)
			},
		},
		{
			name: "multiple stacks - create resources in each new stack",
			mode: pkgmodel.FormaApplyModeReconcile,
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "stack-1"},
					{Label: "stack-2"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:  "instance-1",
						Type:   "AWS::EC2::Instance",
						Stack:  "stack-1",
						Target: "test-target",
					},
					{
						Label:  "instance-2",
						Type:   "AWS::EC2::Instance",
						Stack:  "stack-2",
						Target: "test-target",
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't persist anything - both stacks are new
			},
			expectedCount: 2,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 2)

				resourcesByStack := make(map[string]string)
				operationsByResource := make(map[string]OperationType)
				for _, update := range updates {
					resourcesByStack[update.Resource.Label] = update.StackLabel
					operationsByResource[update.Resource.Label] = update.Operation
				}

				assert.Equal(t, "stack-1", resourcesByStack["instance-1"])
				assert.Equal(t, "stack-2", resourcesByStack["instance-2"])
				assert.Equal(t, OperationCreate, operationsByResource["instance-1"])
				assert.Equal(t, OperationCreate, operationsByResource["instance-2"])
			},
		},
		{
			name: "empty stack - no resources to create",
			mode: pkgmodel.FormaApplyModeReconcile,
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "empty-stack"},
				},
				Resources: []pkgmodel.Resource{},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't persist anything - stack doesn't exist yet
			},
			expectedCount: 0,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Empty(t, updates)
			},
		},
		{
			name: "resources with different targets",
			mode: pkgmodel.FormaApplyModeReconcile,
			forma: &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{
					{Label: "multi-target-stack"},
				},
				Resources: []pkgmodel.Resource{
					{
						Label:  "aws-instance",
						Type:   "AWS::EC2::Instance",
						Stack:  "multi-target-stack",
						Target: "aws-target",
					},
					{
						Label:  "gcp-instance",
						Type:   "GCP::Compute::Instance",
						Stack:  "multi-target-stack",
						Target: "gcp-target",
					},
				},
			},
			setupDatastore: func(ds *mockDatastore) {
				// Don't persist anything - stack doesn't exist yet
			},
			expectedCount: 2,
			validate: func(t *testing.T, updates []ResourceUpdate) {
				assert.Len(t, updates, 2)

				targetsByResource := make(map[string]string)
				for _, update := range updates {
					targetsByResource[update.Resource.Label] = update.Resource.Target
					assert.Equal(t, OperationCreate, update.Operation)
				}

				assert.Equal(t, "aws-target", targetsByResource["aws-instance"])
				assert.Equal(t, "gcp-target", targetsByResource["gcp-instance"])
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
				{
					Label:     "aws-target",
					Config:    json.RawMessage(`{"Region": "us-west-2"}`),
					Namespace: "aws",
				},
				{
					Label:     "gcp-target",
					Config:    json.RawMessage(`{"Project": "my-project", "Region": "us-central1"}`),
					Namespace: "gcp",
				},
			}

			updates, err := GenerateResourceUpdates(
				tt.forma,
				pkgmodel.CommandApply,
				tt.mode,
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

func TestGenerateResourceUpdatesForReconcile_NewResourceUpdateForCreate(t *testing.T) {
	// Test that the NewResourceUpdateForCreate function is called correctly
	ds, _ := GetDeps(t)

	mode := pkgmodel.FormaApplyModeReconcile
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

	updates, err := generateResourceUpdatesForReconcile(
		forma,
		mode,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)
	assert.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, OperationCreate, update.Operation)
	assert.Equal(t, "test-instance", update.Resource.Label)
	assert.Equal(t, "AWS::EC2::Instance", update.Resource.Type)
	assert.Equal(t, "test-stack", update.Resource.Stack)
	assert.Equal(t, "test-target", update.Resource.Target)
	assert.Equal(t, ResourceUpdateStateNotStarted, update.State)
}

func TestGenerateResourceUpdatesForReconcile_VPCSubnetReplaceScenario(t *testing.T) {
	ds, _ := GetDeps(t)

	vpcKsuid := util.NewID()
	subnetKsuid := util.NewID()

	// Setup existing stack with VPC -> Subnet dependency
	existingVpc := pkgmodel.Resource{
		Label:    "test-vpc",
		Type:     "AWS::EC2::VPC",
		Stack:    "infrastructure",
		Target:   "test-target",
		Ksuid:    vpcKsuid,
		NativeID: "vpc-12345",
		Schema: pkgmodel.Schema{
			Identifier: "VpcId",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"CidrBlock": {
					CreateOnly: true,
				},
			},
			Fields: []string{"CidrBlock", "VpcId", "Tags"},
		},
		Properties: json.RawMessage(`{
			"CidrBlock": "10.0.0.0/16",
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Test"
			}
			]
			}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-12345"}`),
	}

	existingSubnet := pkgmodel.Resource{
		Label:    "test-subnet",
		Type:     "AWS::EC2::Subnet",
		Stack:    "infrastructure",
		Target:   "test-target",
		Ksuid:    subnetKsuid,
		NativeID: "subnet-12345",
		Schema: pkgmodel.Schema{
			Identifier: "SubnetId",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"CidrBlock": {
					CreateOnly: true,
				},
				"VpcId": {
					CreateOnly: true,
				},
			},
			Fields: []string{"VpcId", "CidrBlock", "SubnetId", "Tags"},
		},
		Properties: fmt.Appendf(nil, `{
			"VpcId": {
			"$ref": "formae://%s#/VpcId",
			"$value": "vpc-12345"
			},
			"CidrBlock": "10.0.1.0/24",
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Test"
			}
			]
			}`, vpcKsuid),
		ReadOnlyProperties: json.RawMessage(`{"SubnetId": "subnet-12345"}`),
	}

	// persist existing stack
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{existingVpc, existingSubnet},
	}
	_, err := ds.StoreStack(existingStack, "test-command-4")
	assert.NoError(t, err)

	// Create new resources with changed VPC CidrBlock (requires replacement)
	// Note: Only including the new VPC, not the subnet (subnet will be implicitly deleted)
	newVpc := pkgmodel.Resource{
		Label:    "test-vpc",
		Type:     "AWS::EC2::VPC",
		Stack:    "infrastructure",
		Target:   "test-target",
		Ksuid:    vpcKsuid,
		NativeID: "new-vpc-67890",
		Schema: pkgmodel.Schema{
			Identifier: "VpcId",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"CidrBlock": {
					CreateOnly: true,
				},
			},
			Fields: []string{"CidrBlock", "VpcId", "Tags"},
		},
		Properties: json.RawMessage(`{
			"CidrBlock": "172.16.0.0/16",
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Test"
			}
			]
			}`),
	}

	// Create apply command with replace mode - only new VPC, no subnet
	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{newVpc, existingSubnet}, // Only VPC, subnet will be implicitly deleted
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForReconcile(
		forma,
		mode,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)

	// Should have 4 operations: delete VPC, create VPC, delete Subnet, and create Subnet (Subnet is implicitly replaced)
	assert.Len(t, updates, 4)

	// Categorize updates by operation and resource
	updatesByOperation := make(map[OperationType][]ResourceUpdate)
	for _, update := range updates {
		updatesByOperation[update.Operation] = append(updatesByOperation[update.Operation], update)
	}

	// Should have 2 deletes (VPC + Subnet) and 2 create (VPC + Subnet)
	assert.Len(t, updatesByOperation[OperationDelete], 2, "Should have 2 delete operations")
	assert.Len(t, updatesByOperation[OperationCreate], 2, "Should have 2 create operation")

	// Check delete operations
	deletesByLabel := make(map[string]ResourceUpdate)
	for _, deleteUpdate := range updatesByOperation[OperationDelete] {
		deletesByLabel[deleteUpdate.Resource.Label] = deleteUpdate
	}

	vpcDelete := deletesByLabel["test-vpc"]
	assert.Equal(t, "AWS::EC2::VPC", vpcDelete.Resource.Type)
	assert.Equal(t, "infrastructure", vpcDelete.StackLabel)
	assert.JSONEq(t, string(existingVpc.Properties), string(vpcDelete.Resource.Properties))

	subnetDelete := deletesByLabel["test-subnet"]
	assert.Equal(t, "AWS::EC2::Subnet", subnetDelete.Resource.Type)
	assert.Equal(t, "infrastructure", subnetDelete.StackLabel)
	assert.JSONEq(t, string(existingSubnet.Properties), string(subnetDelete.Resource.Properties))

	// Check create operation (should only be VPC)
	createsByLabel := make(map[string]ResourceUpdate)
	for _, createUpdate := range updatesByOperation[OperationCreate] {
		createsByLabel[createUpdate.Resource.Label] = createUpdate
	}

	vpcCreate := createsByLabel["test-vpc"]
	assert.Equal(t, "AWS::EC2::VPC", vpcCreate.Resource.Type)
	assert.Equal(t, "infrastructure", vpcCreate.StackLabel)
	assert.JSONEq(t, string(newVpc.Properties), string(vpcCreate.Resource.Properties))

	subnetCreate := createsByLabel["test-subnet"]
	assert.Equal(t, "AWS::EC2::Subnet", subnetCreate.Resource.Type)
	assert.Equal(t, "infrastructure", subnetCreate.StackLabel)
	assert.JSONEq(t, string(existingSubnet.Properties), string(subnetCreate.Resource.Properties))

	assert.Equal(t, vpcCreate.GroupID, vpcDelete.GroupID, "VPC create and delete should have same group ID")
	assert.Equal(t, subnetCreate.GroupID, subnetDelete.GroupID, "Subnet create and delete should have same group ID")

	// Should not have subnet create
	_, hasSubnetCreate := createsByLabel["test-subnet"]
	assert.True(t, hasSubnetCreate, "Should have subnet create operation")

	// Verify that VPC delete and create have the same group ID (VPC replacement)
	assert.Equal(t, vpcDelete.GroupID, vpcCreate.GroupID, "VPC delete and create should have same group ID")

	// Subnet delete should have a different group ID (implicit delete, not replacement)
	assert.NotEqual(t, vpcDelete.GroupID, subnetDelete.GroupID, "VPC and Subnet should have different group IDs")

	// All operations should be in NotStarted state
	for _, update := range updates {
		assert.Equal(t, ResourceUpdateStateNotStarted, update.State)
	}

	// Verify that CIDR block changed in the VPC create operation
	vpcCreateParsed := gjson.Parse(string(vpcCreate.Resource.Properties))
	assert.Equal(t, "172.16.0.0/16", vpcCreateParsed.Get("CidrBlock").String())

	// Verify that old CIDR blocks are preserved in delete operations
	vpcDeleteParsed := gjson.Parse(string(vpcDelete.Resource.Properties))
	assert.Equal(t, "10.0.0.0/16", vpcDeleteParsed.Get("CidrBlock").String())

	subnetDeleteParsed := gjson.Parse(string(subnetDelete.Resource.Properties))
	assert.Equal(t, "10.0.1.0/24", subnetDeleteParsed.Get("CidrBlock").String())

	// Verify dependencies are preserved
	assert.Contains(t, subnetDelete.RemainingResolvables, pkgmodel.NewFormaeURI(vpcKsuid, "VpcId"))
	assert.Empty(t, vpcDelete.RemainingResolvables, "VPC delete should have no dependencies")
	assert.Empty(t, vpcCreate.RemainingResolvables, "VPC create should have no dependencies")
}

func TestGenerateResourceUpdatesForReconcile_VPCSubnetReplace_Adding_New_Subnet(t *testing.T) {
	ds, _ := GetDeps(t)

	vpcKsuid := util.NewID()
	subnetKsuid := util.NewID()

	// Setup existing stack with VPC -> Subnet dependency
	existingVpc := pkgmodel.Resource{
		Label:  "test-vpc",
		Type:   "AWS::EC2::VPC",
		Stack:  "infrastructure",
		Target: "test-target",
		Ksuid:  vpcKsuid,
		Schema: pkgmodel.Schema{
			Identifier: "VpcId",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"CidrBlock": {
					CreateOnly: true,
				},
			},
			Fields: []string{"CidrBlock", "VpcId", "Tags"},
		},
		Properties: json.RawMessage(`{
			"CidrBlock": "10.0.0.0/16",
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Test"
			}
			]
			}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "vpc-12345"}`),
	}

	newSubnet := pkgmodel.Resource{
		Label:  "test-subnet",
		Type:   "AWS::EC2::Subnet",
		Stack:  "infrastructure",
		Target: "test-target",
		Ksuid:  subnetKsuid,
		Schema: pkgmodel.Schema{
			Identifier: "SubnetId",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"CidrBlock": {
					CreateOnly: true,
				},
				"VpcId": {
					CreateOnly: true,
				},
			},
			Fields: []string{"VpcId", "CidrBlock", "SubnetId", "Tags"},
		},
		Properties: fmt.Appendf(nil, `{
			"VpcId": {
			"$ref": "formae://%s#/VpcId",
			"$value": "vpc-12345"
			},
			"CidrBlock": "10.0.1.0/24",
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Test"
			}
			]
			}`, vpcKsuid),
		ReadOnlyProperties: json.RawMessage(`{"SubnetId": "subnet-12345"}`),
	}

	// persist existing stack
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{existingVpc},
	}
	_, err := ds.StoreStack(existingStack, "test-command-5")
	assert.NoError(t, err)

	// Create new resources with changed VPC CidrBlock (requires replacement)
	// Note: Only including the new VPC, not the subnet (subnet will be implicitly deleted)
	newVpc := pkgmodel.Resource{
		Label:  "test-vpc",
		Type:   "AWS::EC2::VPC",
		Stack:  "infrastructure",
		Target: "test-target",
		Ksuid:  vpcKsuid,
		Schema: pkgmodel.Schema{
			Identifier: "VpcId",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"CidrBlock": {
					CreateOnly: true,
				},
			},
			Fields: []string{"CidrBlock", "VpcId", "Tags"},
		},
		Properties: json.RawMessage(`{
			"CidrBlock": "172.16.0.0/16",
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Test"
			}
			]
			}`),
	}

	// Create apply command with replace mode - only new VPC, no subnet
	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{newVpc, newSubnet}, // Only VPC, subnet will be implicitly deleted
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForReconcile(
		forma,
		mode,
		FormaCommandSourceUser,
		targetMap,
		ds,
	)

	assert.NoError(t, err)

	// Should have 3 operations: delete VPC, delete Subnet (implicit), create VPC
	assert.Len(t, updates, 3)

	// Categorize updates by operation and resource
	updatesByOperation := make(map[OperationType][]ResourceUpdate)
	for _, update := range updates {
		updatesByOperation[update.Operation] = append(updatesByOperation[update.Operation], update)
	}

	// Should have 1 deletes (VPC only) and 2 create (VPC + Subnet)
	assert.Len(t, updatesByOperation[OperationDelete], 1, "Should have 1 delete operations")
	assert.Len(t, updatesByOperation[OperationCreate], 2, "Should have 1 create operation")

	// Check delete operations
	deletesByLabel := make(map[string]ResourceUpdate)
	for _, deleteUpdate := range updatesByOperation[OperationDelete] {
		deletesByLabel[deleteUpdate.Resource.Label] = deleteUpdate
	}

	vpcDelete := deletesByLabel["test-vpc"]
	assert.Equal(t, "AWS::EC2::VPC", vpcDelete.Resource.Type)
	assert.Equal(t, "infrastructure", vpcDelete.StackLabel)
	assert.JSONEq(t, string(existingVpc.Properties), string(vpcDelete.Resource.Properties))

	// Check create operation (should only be VPC)
	createsByLabel := make(map[string]ResourceUpdate)
	for _, createUpdate := range updatesByOperation[OperationCreate] {
		createsByLabel[createUpdate.Resource.Label] = createUpdate
	}

	vpcCreate := createsByLabel["test-vpc"]
	assert.Equal(t, "AWS::EC2::VPC", vpcCreate.Resource.Type)
	assert.Equal(t, "infrastructure", vpcCreate.StackLabel)
	assert.JSONEq(t, string(newVpc.Properties), string(vpcCreate.Resource.Properties))

	subnetCreate := createsByLabel["test-subnet"]
	assert.Equal(t, "AWS::EC2::Subnet", subnetCreate.Resource.Type)
	assert.Equal(t, "infrastructure", subnetCreate.StackLabel)
	assert.JSONEq(t, string(newSubnet.Properties), string(subnetCreate.Resource.Properties))

	// Verify that VPC delete and create have the same group ID (VPC replacement)
	assert.Equal(t, vpcDelete.GroupID, vpcCreate.GroupID, "VPC delete and create should have same group ID")

	// Subnet delete should have a different group ID (implicit delete, not replacement)
	assert.NotEqual(t, vpcDelete.GroupID, subnetCreate.GroupID, "VPC and Subnet should have different group IDs")

	// All operations should be in NotStarted state
	for _, update := range updates {
		assert.Equal(t, ResourceUpdateStateNotStarted, update.State)
	}

	// Verify that CIDR block changed in the VPC create operation
	vpcCreateParsed := gjson.Parse(string(vpcCreate.Resource.Properties))
	assert.Equal(t, "172.16.0.0/16", vpcCreateParsed.Get("CidrBlock").String())

	// Verify that old CIDR blocks are preserved in delete operations
	vpcDeleteParsed := gjson.Parse(string(vpcDelete.Resource.Properties))
	assert.Equal(t, "10.0.0.0/16", vpcDeleteParsed.Get("CidrBlock").String())

	subnetDeleteParsed := gjson.Parse(string(subnetCreate.Resource.Properties))
	assert.Equal(t, "10.0.1.0/24", subnetDeleteParsed.Get("CidrBlock").String())

	// Verify dependencies are preserved
	assert.Contains(t, subnetCreate.RemainingResolvables, pkgmodel.NewFormaeURI(vpcKsuid, "VpcId"))
	assert.Empty(t, vpcDelete.RemainingResolvables, "VPC delete should have no dependencies")
	assert.Empty(t, vpcCreate.RemainingResolvables, "VPC create should have no dependencies")
}

func TestGenerateResourceUpdatesForReconcile_MissingResolvable(t *testing.T) {
	ds, _ := GetDeps(t)

	resource := pkgmodel.Resource{
		Label:  "pel-record-set",
		Type:   "AWS::Route53::RecordSet",
		Stack:  "infrastructure",
		Target: "test-target",
		Ksuid:  util.NewID(),
		Schema: pkgmodel.Schema{
			Identifier: "Id",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"HostedZoneId": {
					CreateOnly: true,
				},
			},
			Fields: []string{"HostedZoneId"},
		},
		Properties: json.RawMessage(`{"HostedZoneId": {"$ref":"formae://aws/infrastructure/does-not-exist#/Id"}}`),
	}

	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resource},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 1)
}

func TestResourceUpdatesForReconcile_GeneratesUpdateOperationsForUnmanagedResources(t *testing.T) {
	ds, _ := GetDeps(t)

	unmanagedResource := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "$unmanaged",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
		Managed: false,
	}

	// First persist initial resource
	unmanagedStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "$unmanaged"}},
		Resources: []pkgmodel.Resource{unmanagedResource},
	}
	_, err := ds.StoreStack(unmanagedStack, "discovery-command-1")
	assert.NoError(t, err)

	managedResource := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "FormaeStackLabel",
			"Value": "infrastructure"
			},
			{
			"Key": "FormaeResourceLabel",
			"Value": "my-s3-bucket"
			},
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
		Managed: true,
	}

	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{managedResource},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 1)
	assert.Equal(t, OperationUpdate, updates[0].Operation)
	assert.Equal(t, "my-s3-bucket", updates[0].Resource.Label)
	assert.Equal(t, true, updates[0].Resource.Managed)
}

// TODO(naxty): are the remaining tests in this file needed?
func TestGenerateResourceUpdatesForReconcile_Create(t *testing.T) {
	ds, _ := GetDeps(t)

	resource := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
	}

	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resource},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 1)
	assert.Equal(t, OperationCreate, updates[0].Operation)
	assert.Equal(t, "my-s3-bucket", updates[0].Resource.Label)
}

func TestGenerateResourceUpdatesForReconcile_StackExists_NoChanges(t *testing.T) {
	ds, _ := GetDeps(t)

	resource := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
	}

	// First persist the stack
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resource},
	}
	_, err := ds.StoreStack(existingStack, "test-command-1")
	assert.NoError(t, err)

	mode := pkgmodel.FormaApplyModeReconcile
	forma := existingStack

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 0) // No changes needed
}

func TestGenerateResourceUpdatesForReconcile_ImplicitDelete(t *testing.T) {
	ds, _ := GetDeps(t)

	resource1 := pkgmodel.Resource{
		Label:  "my-s3-bucket-keep",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
		Managed: true,
	}

	resource2 := pkgmodel.Resource{
		Label:  "my-s3-bucket-delete",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-to-delete-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
		Managed: true,
	}

	// First persist stack with both resources
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resource1, resource2},
	}
	_, err := ds.StoreStack(existingStack, "test-command-1")
	assert.NoError(t, err)

	// Apply with only first resource (second should be deleted)
	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resource1},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 1)
	assert.Equal(t, OperationDelete, updates[0].Operation)
	assert.Equal(t, "my-s3-bucket-delete", updates[0].Resource.Label)
}

func TestGenerateResourceUpdatesForReconcile_Update(t *testing.T) {
	ds, _ := GetDeps(t)

	resourceInitial := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
	}

	resourceUpdate := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Disabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
	}

	// First persist initial resource
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resourceInitial},
	}
	_, err := ds.StoreStack(existingStack, "test-command-1")
	assert.NoError(t, err)

	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resourceUpdate},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 1)
	assert.Equal(t, OperationUpdate, updates[0].Operation)
}

func TestGenerateResourceUpdatesForReconcile_ReplaceCreateOnlyProperty(t *testing.T) {
	ds, _ := GetDeps(t)

	resourceInitial := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
	}

	resourceUpdate := pkgmodel.Resource{
		Label:  "my-s3-bucket",
		Type:   "AWS::S3::Bucket",
		Stack:  "infrastructure",
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "BucketName",
			Tags:       "Tags",
			Hints: map[string]pkgmodel.FieldHint{
				"BucketName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
		},
		Properties: json.RawMessage(`{
			"BucketName": "my-unique-bucket-name-new",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`),
	}

	// First persist initial resource
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resourceInitial},
	}
	_, err := ds.StoreStack(existingStack, "test-command-1")
	assert.NoError(t, err)

	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resourceUpdate},
	}

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	assert.NoError(t, err)
	assert.Len(t, updates, 2)

	// Should have delete and create operations for replacement
	operationsByType := make(map[OperationType]ResourceUpdate)
	for _, update := range updates {
		operationsByType[update.Operation] = update
	}

	deleteUpdate := operationsByType[OperationDelete]
	createUpdate := operationsByType[OperationCreate]

	assert.Equal(t, "my-s3-bucket", deleteUpdate.Resource.Label)
	assert.Equal(t, "my-s3-bucket", createUpdate.Resource.Label)

	// Should have same group ID for replacement
	assert.NotEmpty(t, deleteUpdate.GroupID)
	assert.Equal(t, deleteUpdate.GroupID, createUpdate.GroupID)
}
