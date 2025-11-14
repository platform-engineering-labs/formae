// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_operation"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
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

func TestInjectResolvables(t *testing.T) {
	t.Run("should not inject anything if no parent is set", func(t *testing.T) {
		op := ListOperation{
			ResourceType: "AWS::EC2::VPC",
			TargetLabel:  "test-target",
		}
		props := injectResolvables(`{"VpcId":"123"}`, op)
		assert.JSONEq(t, `{"VpcId":"123"}`, string(props))
	})

	t.Run("should inject a ref for every list parameter", func(t *testing.T) {
		op := ListOperation{
			ResourceType: "AWS::ECS::TaskSet",
			TargetLabel:  "test-target",
			ParentKSUID:  "35R2vyf6mT5wEs0mTWT5bp1Lf0E",
			ListParams: util.MapToString(map[string]plugin_operation.ListParam{
				"Service": {
					ParentProperty: "Service",
					ListParam:      "ServiceName",
					ListValue:      "my-service",
				},
				"Cluster": {
					ParentProperty: "Cluster",
					ListParam:      "ClusterName",
					ListValue:      "my-cluster",
				},
			}),
		}
		props := injectResolvables(`{"Id":"123"}`, op)
		assert.JSONEq(t, `
		{
			"Id":"123",
			"ServiceName":
			{
				"$ref":"formae://35R2vyf6mT5wEs0mTWT5bp1Lf0E#/Service",
				"$value":"my-service"
			},
			"ClusterName":
			{
				"$ref":"formae://35R2vyf6mT5wEs0mTWT5bp1Lf0E#/Cluster",
				"$value":"my-cluster"
			}
		}`, string(props))
	})
}
