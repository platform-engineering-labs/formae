// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
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

	assert.Len(t, descriptors, 2)
	assert.Contains(t, descriptors, "FakeAWS::EC2::VPC")
	assert.Contains(t, descriptors, "FakeAWS::S3::Bucket")

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
