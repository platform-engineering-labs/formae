// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func TestConstructParentResourceDescriptors(t *testing.T) {
	supportedResources := []plugin.ResourceDescriptor{
		{
			Type:         "FakeAWS::S3::Bucket",
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::VPC",
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::VPCCidrBlock",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}},
			},
		},
		{
			Type:         "FakeAWS::EC2::LegacyVPCCidrBlock",
			Discoverable: false,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}},
			},
		},
		{
			Type:         "FakeAWS::EC2::Instance",
			Discoverable: false,
		},
	}

	descriptors := constructParentResourceDescriptors(supportedResources)

	// VPCCidrBlock is included. Despite having a parent (VPC), it might have children.
	// This allows discoverNestedResources() to look it up when processing resources 
	// that have VPCCidrBlock as their parent.
	assert.Len(t, descriptors, 3)
	assert.Contains(t, descriptors, "FakeAWS::EC2::VPC")
	assert.Contains(t, descriptors, "FakeAWS::S3::Bucket")
	assert.Contains(t, descriptors, "FakeAWS::EC2::VPCCidrBlock")

	assert.Len(t, descriptors["FakeAWS::EC2::VPC"].NestedResourcesWithMappingProperties, 1)
	// There should only be one entry for the VPC CIDR Block
	var mappingProperties []plugin.ListParameter
	var resourceType string
	for t, props := range descriptors["FakeAWS::EC2::VPC"].NestedResourcesWithMappingProperties {
		mappingProperties = props
		resourceType = t.ResourceType
	}
	assert.Equal(t, "FakeAWS::EC2::VPCCidrBlock", resourceType)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}}, mappingProperties)
}

// This test ensures intermediate resources (those that are both children and parents)
// are correctly included in the parent resource descriptors map.
func TestConstructParentResourceDescriptorsMultiLevel(t *testing.T) {
	supportedResources := []plugin.ResourceDescriptor{
		{
			Type:         "FakeAWS::EC2::VPC",
			Discoverable: true,
			// VPC is a root (no parents)
		},
		{
			Type:         "FakeAWS::EC2::Subnet",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "VpcId", ListProperty: "VpcId", QueryPath: "$.VpcId"}},
			},
			// Subnet is both a child (of VPC) AND a parent (of NetworkInterface)
		},
		{
			Type:         "FakeAWS::EC2::NetworkInterface",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::Subnet": {{ParentProperty: "SubnetId", ListProperty: "SubnetId", QueryPath: "$.SubnetId"}},
			},
			// NetworkInterface is a child of subnet
		},
		{
			Type:         "FakeAWS::S3::Bucket",
			Discoverable: true,
			// Bucket is a separate root (no parents)
		},
	}

	descriptors := constructParentResourceDescriptors(supportedResources)

	assert.Len(t, descriptors, 4)
	assert.Contains(t, descriptors, "FakeAWS::EC2::VPC")
	assert.Contains(t, descriptors, "FakeAWS::EC2::Subnet")
	assert.Contains(t, descriptors, "FakeAWS::EC2::NetworkInterface")
	assert.Contains(t, descriptors, "FakeAWS::S3::Bucket")

	vpcDescriptor := descriptors["FakeAWS::EC2::VPC"]
	assert.Len(t, vpcDescriptor.NestedResourcesWithMappingProperties, 1)
	
	var subnetNode *ResourceDescriptorTreeNode
	for node := range vpcDescriptor.NestedResourcesWithMappingProperties {
		assert.Equal(t, "FakeAWS::EC2::Subnet", node.ResourceType)
		subnetNode = node
	}
	assert.NotNil(t, subnetNode)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "VpcId", ListProperty: "VpcId", QueryPath: "$.VpcId"}}, 
		vpcDescriptor.NestedResourcesWithMappingProperties[subnetNode])

	subnetDescriptor := descriptors["FakeAWS::EC2::Subnet"]
	assert.Len(t, subnetDescriptor.NestedResourcesWithMappingProperties, 1)
	
	var networkInterfaceNode *ResourceDescriptorTreeNode
	for node := range subnetDescriptor.NestedResourcesWithMappingProperties {
		assert.Equal(t, "FakeAWS::EC2::NetworkInterface", node.ResourceType)
		networkInterfaceNode = node
	}
	assert.NotNil(t, networkInterfaceNode)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "SubnetId", ListProperty: "SubnetId", QueryPath: "$.SubnetId"}}, 
		subnetDescriptor.NestedResourcesWithMappingProperties[networkInterfaceNode])

	// NetworkInterface should have no children
	networkInterfaceDescriptor := descriptors["FakeAWS::EC2::NetworkInterface"]
	assert.Len(t, networkInterfaceDescriptor.NestedResourcesWithMappingProperties, 0)

	// Bucket should have no children
	bucketDescriptor := descriptors["FakeAWS::S3::Bucket"]
	assert.Len(t, bucketDescriptor.NestedResourcesWithMappingProperties, 0)
}

func TestMultipleNamespacesMerge(t *testing.T) {
	awsResources := []plugin.ResourceDescriptor{
		{
			Type:         "AWS::EC2::VPC",
			Discoverable: true,
		},
		{
			Type:         "AWS::EC2::Subnet",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"AWS::EC2::VPC": {{ParentProperty: "VpcId", ListProperty: "VpcId", QueryPath: "$.VpcId"}},
			},
		},
	}

	ociResources := []plugin.ResourceDescriptor{
		{
			Type:         "OCI::VCN::VCN",
			Discoverable: true,
		},
		{
			Type:         "OCI::VCN::Subnet",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"OCI::VCN::VCN": {{ParentProperty: "Id", ListProperty: "VcnId", QueryPath: "$.Id"}},
			},
		},
	}

	parentDescriptors := make(map[string]ResourceDescriptorTreeNode)

	awsDescriptors := constructParentResourceDescriptors(awsResources)
	maps.Copy(parentDescriptors, awsDescriptors)

	ociDescriptors := constructParentResourceDescriptors(ociResources)
	maps.Copy(parentDescriptors, ociDescriptors)

	// After processing both namespaces, we should have descriptors from both
	assert.Len(t, parentDescriptors, 4, "Should have descriptors from both AWS and OCI namespaces")
	assert.Contains(t, parentDescriptors, "AWS::EC2::VPC")
	assert.Contains(t, parentDescriptors, "AWS::EC2::Subnet")
	assert.Contains(t, parentDescriptors, "OCI::VCN::VCN")
	assert.Contains(t, parentDescriptors, "OCI::VCN::Subnet")

	// Ensure parent-child relationships preserved
	awsVpc := parentDescriptors["AWS::EC2::VPC"]
	assert.Len(t, awsVpc.NestedResourcesWithMappingProperties, 1)

	ociVcn := parentDescriptors["OCI::VCN::VCN"]
	assert.Len(t, ociVcn.NestedResourcesWithMappingProperties, 1)
}
