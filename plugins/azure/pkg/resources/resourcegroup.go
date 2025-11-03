// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/client"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/config"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/prov"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/registry"
)

const ResourceTypeResourceGroup = "Azure::Resources::ResourceGroup"

var ResourceGroupDescriptor = plugin.ResourceDescriptor{
	Type:                                     ResourceTypeResourceGroup,
	ParentResourceTypesWithMappingProperties: nil,
	Discoverable:                             true,
}

var ResourceGroupSchema = model.Schema{
	Identifier: "id",
	Fields: []string{
		"name",
		"location",
		"tags",
		"managedBy",
	},
	Nonprovisionable: false,
	Hints: map[string]model.FieldHint{
		"name": {
			Required:   true,
			CreateOnly: true,
		},
		"location": {
			Required:   true,
			CreateOnly: true,
		},
		"tags": {
			Required: false,
		},
		"managedBy": {
			Required: false,
		},
	},
	Discoverable: true,
}

func init() {
	registry.Register(ResourceTypeResourceGroup, ResourceGroupDescriptor, ResourceGroupSchema,
		func(client *client.Client, cfg *config.Config) prov.Provisioner {
			return &ResourceGroup{client, cfg}
		})
}

type ResourceGroup struct {
	Client *client.Client
	Config *config.Config
}

func (rg *ResourceGroup) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	// Parse properties JSON
	var props map[string]interface{}
	if err := json.Unmarshal(request.Resource.Properties, &props); err != nil {
		return nil, fmt.Errorf("failed to parse resource properties: %w", err)
	}

	// Extract location (required)
	location, ok := props["location"].(string)
	if !ok || location == "" {
		return nil, fmt.Errorf("location is required")
	}

	// Build ResourceGroup parameters
	params := armresources.ResourceGroup{
		Location: &location,
	}

	// Add tags if present using model.GetTagsFromProperties
	tags := model.GetTagsFromProperties(request.Resource.Properties)
	if len(tags) > 0 {
		azureTags := make(map[string]*string)
		for _, tag := range tags {
			val := tag.Value
			azureTags[tag.Key] = &val
		}
		params.Tags = azureTags
	}

	// Add managedBy if present
	if managedBy, ok := props["managedBy"].(string); ok && managedBy != "" {
		params.ManagedBy = &managedBy
	}

	// Call Azure API to create resource group
	// Note: Resource Groups are synchronous operations (no LRO polling needed)
	result, err := rg.Client.ResourceGroupsClient.CreateOrUpdate(
		ctx,
		request.Resource.Label, // Resource group name
		params,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource group: %w", err)
	}

	// Return CreateResult
	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        *result.ID,
			ResourceType:    request.Resource.Type,
		},
	}, nil
}

func (rg *ResourceGroup) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (rg *ResourceGroup) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (rg *ResourceGroup) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (rg *ResourceGroup) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (rg *ResourceGroup) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	return nil, fmt.Errorf("not implemented")
}
