// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_coordinator

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// RemotePluginInfo implements plugin.PluginInfo for external/distributed plugins.
// It holds cached data received from plugin announcements.
type RemotePluginInfo struct {
	namespace          string
	supportedResources []plugin.ResourceDescriptor
	resourceSchemas    map[string]model.Schema
	matchFilters       []plugin.MatchFilter
	labelConfig        plugin.LabelConfig
}

// NewRemotePluginInfo creates a new RemotePluginInfo instance
func NewRemotePluginInfo(
	namespace string,
	supportedResources []plugin.ResourceDescriptor,
	schemas map[string]model.Schema,
	filters []plugin.MatchFilter,
	labelConfig plugin.LabelConfig,
) *RemotePluginInfo {
	return &RemotePluginInfo{
		namespace:          namespace,
		supportedResources: supportedResources,
		resourceSchemas:    schemas,
		matchFilters:       filters,
		labelConfig:        labelConfig,
	}
}

func (r *RemotePluginInfo) GetNamespace() string {
	return r.namespace
}

func (r *RemotePluginInfo) SupportedResources() []plugin.ResourceDescriptor {
	return r.supportedResources
}

func (r *RemotePluginInfo) SchemaForResourceType(resourceType string) (model.Schema, error) {
	schema, ok := r.resourceSchemas[resourceType]
	if !ok {
		return model.Schema{}, fmt.Errorf("schema not found for resource type: %s", resourceType)
	}
	return schema, nil
}

func (r *RemotePluginInfo) DiscoveryFilters() []plugin.MatchFilter {
	return r.matchFilters
}

func (r *RemotePluginInfo) LabelConfig() plugin.LabelConfig {
	return r.labelConfig
}

// Verify RemotePluginInfo implements PluginInfo
var _ plugin.PluginInfo = (*RemotePluginInfo)(nil)
