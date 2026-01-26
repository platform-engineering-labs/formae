// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_coordinator

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginCoordinator_CaseInsensitiveNamespaceLookup(t *testing.T) {
	c := &PluginCoordinator{
		plugins: map[string]*RegisteredPlugin{
			"Azure": {
				Namespace:            "Azure",
				MaxRequestsPerSecond: 10,
			},
			"aws": {
				Namespace:            "aws",
				MaxRequestsPerSecond: 20,
			},
		},
	}

	plugin, ok := c.findPluginByNamespace("azure")
	require.True(t, ok)
	assert.Equal(t, "Azure", plugin.Namespace)

	plugin, ok = c.findPluginByNamespace("AZURE")
	require.True(t, ok)
	assert.Equal(t, "Azure", plugin.Namespace)

	plugin, ok = c.findPluginByNamespace("AWS")
	require.True(t, ok)
	assert.Equal(t, "aws", plugin.Namespace)

	plugin, ok = c.findPluginByNamespace("aws")
	require.True(t, ok)
	assert.Equal(t, "aws", plugin.Namespace)

	_, ok = c.findPluginByNamespace("NonExistent")
	assert.False(t, ok)
}

func TestPluginCoordinator_GetPluginInfo_CaseInsensitive(t *testing.T) {
	c := &PluginCoordinator{
		plugins: map[string]*RegisteredPlugin{
			"Azure": {
				Namespace:            "Azure",
				MaxRequestsPerSecond: 10,
				SupportedResources: []plugin.ResourceDescriptor{
					{Type: "Azure::Compute::VM", Discoverable: true},
				},
			},
		},
	}

	resp := c.getPluginInfo(messages.GetPluginInfo{Namespace: "azure"})
	assert.True(t, resp.Found)
	assert.Equal(t, "azure", resp.Namespace)
	assert.Len(t, resp.SupportedResources, 1)

	resp = c.getPluginInfo(messages.GetPluginInfo{Namespace: "AZURE"})
	assert.True(t, resp.Found)
	assert.Equal(t, "AZURE", resp.Namespace)

	resp = c.getPluginInfo(messages.GetPluginInfo{Namespace: "Azure"})
	assert.True(t, resp.Found)
	assert.Equal(t, "Azure", resp.Namespace)
}
