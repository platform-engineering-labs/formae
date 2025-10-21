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

func TestGenerateResourceUpdatesForPatch_VPCSubnetReplaceScenario_WithoutCreatingVPCInPatch(t *testing.T) {
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

	// Persist existing stack
	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{existingVpc, existingSubnet},
	}
	_, err := ds.StoreStack(existingStack, "test-command-id")
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

	// Create forma directly - only new VPC, no subnet
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{newVpc}, // Only VPC, subnet will be implicitly deleted
	}
	mode := pkgmodel.FormaApplyModePatch

	targetMap := map[string]*pkgmodel.Target{
		"test-target": {
			Label:     "test-target",
			Config:    json.RawMessage(`{"Region": "us-west-2"}`),
			Namespace: "aws",
		},
	}

	updates, err := generateResourceUpdatesForPatch(
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

	// Should have 2 deletes (VPC + Subnet) and 1 create (VPC only)
	assert.Len(t, updatesByOperation[OperationDelete], 2, "Should have 2 delete operations")
	assert.Len(t, updatesByOperation[OperationCreate], 1, "Should have 1 create operation")

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

	// Should not have subnet create
	_, hasSubnetCreate := createsByLabel["test-subnet"]
	assert.False(t, hasSubnetCreate, "Should not have subnet create operation")

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
	expectedVpcUri := pkgmodel.NewFormaeURI(vpcKsuid, "VpcId")
	assert.Contains(t, subnetDelete.RemainingResolvables, expectedVpcUri)
	assert.Empty(t, vpcDelete.RemainingResolvables, "VPC delete should have no dependencies")
	assert.Empty(t, vpcCreate.RemainingResolvables, "VPC create should have no dependencies")
}

func TestGenerateResourceUpdatesForApply_PatchMode(t *testing.T) {
	ds, _ := GetDeps(t)

	resource := pkgmodel.Resource{
		Label:    "my-s3-bucket",
		Type:     "AWS::S3::Bucket",
		Stack:    "infrastructure",
		Target:   "test-target",
		Ksuid:    util.NewID(),
		NativeID: "bucket-12345",
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

	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{resource},
	}
	mode := pkgmodel.FormaApplyModePatch

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
}

func TestResourceUpdatesForPatch_GeneratesUpdateOperationsForUnmanagedResources(t *testing.T) {
	ds, _ := GetDeps(t)

	existingResource := pkgmodel.Resource{
		Label:    "my-vpc",
		NativeID: "vpc-12345",
		Type:     "AWS::EC2::VPC",
		Stack:    "infrastructure",
		Target:   "test-target",
	}

	existingStack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{existingResource},
	}
	_, err := ds.StoreStack(existingStack, "test-command-1")
	assert.NoError(t, err)

	unmanagedResource := pkgmodel.Resource{
		Label:    "my-s3-bucket",
		Type:     "AWS::S3::Bucket",
		Stack:    "$unmanaged",
		Target:   "test-target",
		NativeID: "bucket-12345",
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
	_, err = ds.StoreStack(unmanagedStack, "discovery-command-1")
	assert.NoError(t, err)

	managedResource := pkgmodel.Resource{
		Label:    "my-s3-bucket",
		Type:     "AWS::S3::Bucket",
		Stack:    "infrastructure",
		Target:   "test-target",
		NativeID: "bucket-12345",
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

	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
		Resources: []pkgmodel.Resource{managedResource},
	}
	mode := pkgmodel.FormaApplyModePatch

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
