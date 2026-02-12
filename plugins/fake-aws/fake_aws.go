// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

type FakeAWS struct{}

// Compile time checks to satisfy protocol
var _ plugin.Plugin = FakeAWS{}
var _ plugin.ResourcePlugin = FakeAWS{}

// Maintain known symbol reference
var Plugin = FakeAWS{}

// RateLimitMaxRPS allows tests to control the rate limit
var RateLimitMaxRPS int = 5

func (s FakeAWS) Name() string {
	return "fake-aws"
}

func (s FakeAWS) Version() *semver.Version {
	return semver.MustParse("0.0.1")
}

func (s FakeAWS) Type() plugin.Type {
	return plugin.Resource
}

func (s FakeAWS) Namespace() string {
	return "FakeAWS"
}

func (s FakeAWS) overrides(context context.Context) *plugin.ResourcePluginOverrides {
	if f, ok := context.Value(plugin.ResourcePluginOverridesContextKey).(*plugin.ResourcePluginOverrides); ok {
		return f
	}

	return nil
}

func (s FakeAWS) SupportedResources() []plugin.ResourceDescriptor {
	return []plugin.ResourceDescriptor{
		{
			Type: "FakeAWS::S3::Bucket",

			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::VPC",
			Discoverable: true,
		},
		{
			Type: "FakeAWS::EC2::VPCCidrBlock",
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "VpcId", ListProperty: "VpcId", QueryPath: "$.VpcId"}},
			},
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::Instance",
			Discoverable: true,
		},
	}
}

func (s FakeAWS) RateLimit() plugin.RateLimitConfig {
	return plugin.RateLimitConfig{
		Scope:                            plugin.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: RateLimitMaxRPS,
	}
}

func (s FakeAWS) SchemaForResourceType(resourceType string) (model.Schema, error) {
	switch resourceType {
	case "FakeAWS::EC2::VPCCidrBlock":
		return model.Schema{
			Identifier: "Id",
			Fields: []string{
				"AmazonProvidedIpv6CidrBlock",
				"CidrBlock",
				"Ipv4IpamPoolId",
				"Ipv4NetmaskLength",
				"Ipv6CidrBlock",
				"Ipv6CidrBlockNetworkBorderGroup",
				"Ipv6IpamPoolId",
				"Ipv6NetmaskLength",
				"Ipv6Pool",
				"VpcId"},
		}, nil
	default:
		return model.Schema{
			Identifier: "BucketName",
			Fields: []string{
				"AccelerateConfiguration",
				"AccessControl",
				"AnalyticsConfigurations",
				"BucketEncryption",
				"BucketName",
				"CorsConfiguration",
				"IntelligentTieringConfigurations",
				"InventoryConfigurations",
				"LifecycleConfiguration",
				"LoggingConfiguration",
				"MetricsConfigurations",
				"NotificationConfiguration",
				"ObjectLockEnabled",
				"ObjectLockConfiguration",
				"OwnershipControls",
				"PublicAccessBlockConfiguration",
				"ReplicationConfiguration",
				"Tags",
				"VersioningConfiguration",
				"WebsiteConfiguration"},
			Hints: map[string]model.FieldHint{
				"BucketEncryption": {
					HasProviderDefault: true,
				},
				"OwnershipControls": {
					HasProviderDefault: true,
				},
			},
		}, nil
	}
}

func (s FakeAWS) Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	overrides := s.overrides(context)
	if overrides != nil && overrides.Create != nil {
		ret, err := (overrides.Create)(request)
		if err != nil {
			return nil, err
		}

		if ret != nil {
			return ret, nil
		}
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			RequestID:          "1234",
			NativeID:           "5678",
			ResourceProperties: request.Properties,
		},
	}, nil
}

func (s FakeAWS) Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	overrides := s.overrides(context)
	if overrides != nil && overrides.Update != nil {
		ret, err := (overrides.Update)(request)
		if err != nil {
			return nil, err
		}

		if ret != nil {
			return ret, nil
		}
	}

	return nil, nil
}

func (s FakeAWS) Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	overrides := s.overrides(context)
	if overrides != nil && overrides.Delete != nil {
		ret, err := (overrides.Delete)(request)
		if err != nil {
			return nil, err
		}

		if ret != nil {
			return ret, nil
		}
	}

	if request.NativeID != "" {
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusSuccess,
				RequestID:       "delete-123",
				NativeID:        request.NativeID,
			},
		}, nil
	}

	// Otherwise, perform the default (no delete operation).
	return nil, nil
}

func (s FakeAWS) Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	overrides := s.overrides(context)
	if overrides != nil && overrides.Status != nil {
		ret, err := (overrides.Status)(request)
		if err != nil {
			return nil, err
		}

		if ret != nil {
			return ret, nil
		}
	}
	return nil, nil
}

func (s FakeAWS) Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	overrides := s.overrides(context)
	if overrides != nil && overrides.Read != nil {
		ret, err := (overrides.Read)(request)
		if err != nil {
			return nil, err
		}

		if ret != nil {
			return ret, nil
		}
	}

	if request.NativeID != "" {
		return &resource.ReadResult{
			ResourceType: request.ResourceType,
			Properties:   "{}",
		}, nil
	}

	return nil, nil
}

func (s FakeAWS) List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	overrides := s.overrides(context)
	if overrides != nil && overrides.List != nil {
		ret, err := (overrides.List)(request)
		if err != nil {
			return nil, err
		}

		if ret != nil {
			return ret, nil
		}
	}

	return &resource.ListResult{
		NativeIDs: []string{"1234", "5678"},
	}, nil
}

// DiscoveryFilters returns declarative filters for testing discovery exclusion
func (s FakeAWS) DiscoveryFilters() []plugin.MatchFilter {
	return []plugin.MatchFilter{
		{
			// Filter 1: Exclude by top-level property
			ResourceTypes: []string{"FakeAWS::S3::Bucket"},
			Conditions: []plugin.FilterCondition{
				{
					PropertyPath:  "$.SkipDiscovery",
					PropertyValue: "true",
				},
			},
		},
		{
			// Filter 2: Exclude by tag in array format [{"Key": "...", "Value": "..."}]
			ResourceTypes: []string{"FakeAWS::S3::Bucket"},
			Conditions: []plugin.FilterCondition{
				{
					PropertyPath:  `$.Tags[?(@.Key=="SkipDiscovery")].Value`,
					PropertyValue: "true",
				},
			},
		},
		{
			// Filter 3: Exclude by tag in map format {"key": "value"}
			ResourceTypes: []string{"FakeAWS::S3::Bucket"},
			Conditions: []plugin.FilterCondition{
				{
					PropertyPath:  "$.Tags.SkipDiscovery",
					PropertyValue: "true",
				},
			},
		},
	}
}

// LabelConfig returns the label extraction configuration for discovered FakeAWS resources.
// Uses the same pattern as the real AWS plugin for testing.
func (s FakeAWS) LabelConfig() plugin.LabelConfig {
	return plugin.LabelConfig{
		DefaultQuery:      `$.Tags[?(@.Key=='Name')].Value`,
		ResourceOverrides: map[string]string{},
	}
}
