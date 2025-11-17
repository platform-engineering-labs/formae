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

func TestBuildResourceHierarchy(t *testing.T) {
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

	hierarchy := buildResourceHierarchy(supportedResources)

	assert.Len(t, hierarchy, 3)
	assert.Contains(t, hierarchy, "FakeAWS::EC2::VPC")
	assert.Contains(t, hierarchy, "FakeAWS::S3::Bucket")
	assert.Contains(t, hierarchy, "FakeAWS::EC2::VPCCidrBlock")

	assert.Len(t, hierarchy["FakeAWS::EC2::VPC"].children, 1)
	var mappingProperties []plugin.ListParameter
	var resourceType string
	for childNode, props := range hierarchy["FakeAWS::EC2::VPC"].children {
		mappingProperties = props
		resourceType = childNode.resourceType
	}
	assert.Equal(t, "FakeAWS::EC2::VPCCidrBlock", resourceType)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}}, mappingProperties)
}

func TestBuildResourceHierarchyMultiLevel(t *testing.T) {
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

	hierarchy := buildResourceHierarchy(supportedResources)

	assert.Len(t, hierarchy, 4)
	assert.Contains(t, hierarchy, "FakeAWS::EC2::VPC")
	assert.Contains(t, hierarchy, "FakeAWS::EC2::Subnet")
	assert.Contains(t, hierarchy, "FakeAWS::EC2::NetworkInterface")
	assert.Contains(t, hierarchy, "FakeAWS::S3::Bucket")

	vpcNode := hierarchy["FakeAWS::EC2::VPC"]
	assert.Len(t, vpcNode.children, 1)

	var subnetNode *hierarchyNode
	for node := range vpcNode.children {
		assert.Equal(t, "FakeAWS::EC2::Subnet", node.resourceType)
		subnetNode = node
	}
	assert.NotNil(t, subnetNode)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "VpcId", ListProperty: "VpcId", QueryPath: "$.VpcId"}},
		vpcNode.children[subnetNode])

	subnetHierarchyNode := hierarchy["FakeAWS::EC2::Subnet"]
	assert.Len(t, subnetHierarchyNode.children, 1)

	var networkInterfaceNode *hierarchyNode
	for node := range subnetHierarchyNode.children {
		assert.Equal(t, "FakeAWS::EC2::NetworkInterface", node.resourceType)
		networkInterfaceNode = node
	}
	assert.NotNil(t, networkInterfaceNode)
	assert.Equal(t, []plugin.ListParameter{{ParentProperty: "SubnetId", ListProperty: "SubnetId", QueryPath: "$.SubnetId"}},
		subnetHierarchyNode.children[networkInterfaceNode])

	networkInterfaceHierarchyNode := hierarchy["FakeAWS::EC2::NetworkInterface"]
	assert.Len(t, networkInterfaceHierarchyNode.children, 0)

	bucketNode := hierarchy["FakeAWS::S3::Bucket"]
	assert.Len(t, bucketNode.children, 0)
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

	hierarchy := make(map[string]*hierarchyNode)

	awsHierarchy := buildResourceHierarchy(awsResources)
	maps.Copy(hierarchy, awsHierarchy)

	ociHierarchy := buildResourceHierarchy(ociResources)
	maps.Copy(hierarchy, ociHierarchy)

	assert.Len(t, hierarchy, 4, "Should have descriptors from both AWS and OCI namespaces")
	assert.Contains(t, hierarchy, "AWS::EC2::VPC")
	assert.Contains(t, hierarchy, "AWS::EC2::Subnet")
	assert.Contains(t, hierarchy, "OCI::VCN::VCN")
	assert.Contains(t, hierarchy, "OCI::VCN::Subnet")

	awsVpc := hierarchy["AWS::EC2::VPC"]
	assert.Len(t, awsVpc.children, 1)

	ociVcn := hierarchy["OCI::VCN::VCN"]
	assert.Len(t, ociVcn.children, 1)
}
