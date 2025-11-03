// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package azure

import (
	"context"
	"fmt"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/client"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/config"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/registry"

	// Import resources to trigger init() registration
	_ "github.com/platform-engineering-labs/formae/plugins/azure/pkg/resources"
)

type Azure struct{}

var Plugin = Azure{}

func (a Azure) Name() string {
	return "azure"
}

func (a Azure) Type() plugin.Type {
	return plugin.Resource
}

func (a Azure) Version() *semver.Version {
	// TODO: Load from version file
	return semver.MustParse("0.1.0")
}

func (a Azure) Namespace() string {
	return "Azure"
}

func (a Azure) SupportedResources() []plugin.ResourceDescriptor {
	return registry.SupportedResources()
}

func (a Azure) SchemaForResourceType(resourceType string) (model.Schema, error) {
	schema := registry.SchemaForResourceType(resourceType)
	if schema.Identifier == "" {
		return model.Schema{}, fmt.Errorf("unsupported resource type: %s", resourceType)
	}
	return schema, nil
}

func (a Azure) MaxRequestsPerSecond() int {
	// Azure ARM API has rate limits, but we'll start conservative
	return 10
}

func (a Azure) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	targetConfig := config.FromTarget(request.Target)
	client, err := client.NewClient(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client: %w", err)
	}

	if !registry.HasProvisioner(request.Resource.Type) {
		return nil, fmt.Errorf("unsupported resource type: %s", request.Resource.Type)
	}

	provisioner := registry.Get(request.Resource.Type, client, targetConfig)
	return provisioner.Create(ctx, request)
}

func (a Azure) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	targetConfig := config.FromTarget(request.Target)
	client, err := client.NewClient(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client: %w", err)
	}

	if !registry.HasProvisioner(request.Resource.Type) {
		return nil, fmt.Errorf("unsupported resource type: %s", request.Resource.Type)
	}

	provisioner := registry.Get(request.Resource.Type, client, targetConfig)
	return provisioner.Update(ctx, request)
}

func (a Azure) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	targetConfig := config.FromTarget(request.Target)
	client, err := client.NewClient(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client: %w", err)
	}

	if !registry.HasProvisioner(request.ResourceType) {
		return nil, fmt.Errorf("unsupported resource type: %s", request.ResourceType)
	}

	provisioner := registry.Get(request.ResourceType, client, targetConfig)
	return provisioner.Delete(ctx, request)
}

func (a Azure) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	targetConfig := config.FromTarget(request.Target)
	client, err := client.NewClient(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client: %w", err)
	}

	if !registry.HasProvisioner(request.ResourceType) {
		return nil, fmt.Errorf("unsupported resource type: %s", request.ResourceType)
	}

	provisioner := registry.Get(request.ResourceType, client, targetConfig)
	return provisioner.Read(ctx, request)
}

func (a Azure) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	targetConfig := config.FromTarget(request.Target)
	client, err := client.NewClient(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client: %w", err)
	}

	if !registry.HasProvisioner(request.ResourceType) {
		return nil, fmt.Errorf("unsupported resource type: %s", request.ResourceType)
	}

	provisioner := registry.Get(request.ResourceType, client, targetConfig)
	return provisioner.Status(ctx, request)
}

func (a Azure) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	targetConfig := config.FromTarget(request.Target)
	client, err := client.NewClient(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure client: %w", err)
	}

	if !registry.HasProvisioner(request.ResourceType) {
		return nil, fmt.Errorf("unsupported resource type: %s", request.ResourceType)
	}

	provisioner := registry.Get(request.ResourceType, client, targetConfig)
	return provisioner.List(ctx, request)
}

func (a Azure) TargetBehavior() resource.TargetBehavior {
	return targetBehavior{}
}

func (a Azure) GetResourceFilters() map[string]plugin.ResourceFilter {
	// TODO: Implement resource filters for discovery
	return map[string]plugin.ResourceFilter{}
}
