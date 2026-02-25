package main

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestPlugin is a minimal resource plugin for blackbox property-based testing.
type TestPlugin struct {
	cloudState     *CloudState
	nativeIDCounter atomic.Int64
}

// Compile-time checks to satisfy protocol
var _ plugin.Plugin = (*TestPlugin)(nil)
var _ plugin.ResourcePlugin = (*TestPlugin)(nil)

func (p *TestPlugin) Name() string {
	return "test-plugin"
}

func (p *TestPlugin) Version() *semver.Version {
	return semver.MustParse("0.0.1")
}

func (p *TestPlugin) Type() plugin.Type {
	return plugin.Resource
}

func (p *TestPlugin) Namespace() string {
	return "Test"
}

func (p *TestPlugin) SupportedResources() []plugin.ResourceDescriptor {
	return []plugin.ResourceDescriptor{
		{
			Type:         "Test::Generic::Resource",
			Discoverable: true,
		},
	}
}

func (p *TestPlugin) RateLimit() plugin.RateLimitConfig {
	return plugin.RateLimitConfig{
		Scope:                            plugin.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 100,
	}
}

func (p *TestPlugin) SchemaForResourceType(resourceType string) (model.Schema, error) {
	return model.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Value", "Tags"},
	}, nil
}

func (p *TestPlugin) Create(_ context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	nativeID := fmt.Sprintf("test-%d", p.nativeIDCounter.Add(1))

	p.cloudState.Put(nativeID, request.ResourceType, string(request.Properties))

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           nativeID,
			ResourceProperties: request.Properties,
		},
	}, nil
}

func (p *TestPlugin) Read(_ context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	entry, ok := p.cloudState.Get(request.NativeID)
	if !ok {
		return nil, nil
	}

	return &resource.ReadResult{
		ResourceType: entry.ResourceType,
		Properties:   entry.Properties,
	}, nil
}

func (p *TestPlugin) Update(_ context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	p.cloudState.Put(request.NativeID, request.ResourceType, string(request.DesiredProperties))

	return nil, nil
}

func (p *TestPlugin) Delete(_ context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	p.cloudState.Delete(request.NativeID)

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        request.NativeID,
		},
	}, nil
}

func (p *TestPlugin) Status(_ context.Context, _ *resource.StatusRequest) (*resource.StatusResult, error) {
	return nil, nil
}

func (p *TestPlugin) List(_ context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	ids := p.cloudState.ListNativeIDs(request.ResourceType)
	if ids == nil {
		ids = []string{}
	}

	return &resource.ListResult{
		NativeIDs: ids,
	}, nil
}

func (p *TestPlugin) DiscoveryFilters() []plugin.MatchFilter {
	return nil
}

func (p *TestPlugin) LabelConfig() plugin.LabelConfig {
	return plugin.LabelConfig{
		DefaultQuery:      "$.Name",
		ResourceOverrides: map[string]string{},
	}
}
