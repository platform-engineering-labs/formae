// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"strings"

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

// MaxRequestsPerSecond allows tests to control the rate limit
var MaxRequestsPerSecond int = 5

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
			Type:         "FakeAWS::S3::Bucket",
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::VPC",
			Discoverable: true,
		},
		{
			Type: "FakeAWS::EC2::VPCCidrBlock",
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"FakeAWS::EC2::VPC": {{ParentProperty: "Id", ListProperty: "VpcId", QueryPath: "$.Id"}},
			},
			Discoverable: true,
		},
		{
			Type:         "FakeAWS::EC2::Instance",
			Discoverable: true,
		},
	}
}

func (s FakeAWS) MaxRequestsPerSecond() int {
	return MaxRequestsPerSecond
}

func (s FakeAWS) SchemaForResourceType(resourceType string) (model.Schema, error) {
	return model.Schema{
		Identifier: "BucketName",
		Tags:       "Tags",
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
		Nonprovisionable: false,
	}, nil
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
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       "1234",
			NativeID:        "5678",
			ResourceType:    request.Resource.Type,
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

	// If the Resource's Id contains "delete", return a success delete result.
	if strings.Contains(*request.NativeID, "delete") {
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusSuccess,
				RequestID:       "delete-123",
				NativeID:        *request.NativeID,
				ResourceType:    request.ResourceType,
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
		ResourceType: request.ResourceType,
		Resources: []resource.Resource{
			{
				NativeID:   "1234",
				Properties: "{}",
			},
			{
				NativeID:   "5678",
				Properties: "{}",
			},
		},
	}, nil
}

func (s FakeAWS) TargetBehavior() resource.TargetBehavior {
	return TargetBehavior
}

// GetResourceFilters exists to satisfy the ResourcePlugin interface
func (s FakeAWS) GetResourceFilters() map[string]plugin.ResourceFilter {
	return make(map[string]plugin.ResourceFilter)
}
