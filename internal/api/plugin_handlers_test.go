// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_manager"
)

func TestMergeRegisteredPlugins_OrbitalOnly(t *testing.T) {
	orbital := []plugin_manager.Plugin{
		{Name: "formae-plugin-aws", Namespace: "aws", InstalledVersion: "1.2.3", Type: "resource"},
	}
	got := mergeRegisteredPlugins(orbital, nil, nil)

	assert.Equal(t, orbital, got)
}

func TestMergeRegisteredPlugins_RegisteredNotInOrbital(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Name: "azure", Namespace: "azure", Version: "0.1.0"},
	}
	paths := map[string]string{"azure": "/plugins/azure/v0.1.0/schema/pkl/PklProject"}

	got := mergeRegisteredPlugins(nil, registered, paths)

	assert.Len(t, got, 1)
	assert.Equal(t, "azure", got[0].Name)
	assert.Equal(t, "azure", got[0].Namespace)
	assert.Equal(t, "resource", got[0].Type)
	assert.Equal(t, "plugin", got[0].Kind)
	assert.Equal(t, "0.1.0", got[0].InstalledVersion)
	assert.Equal(t, "/plugins/azure/v0.1.0/schema/pkl/PklProject", got[0].LocalPath)
	assert.Nil(t, got[0].Metadata)
}

func TestMergeRegisteredPlugins_RegisteredWithoutLocalPath(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Name: "azure", Namespace: "azure", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(nil, registered, map[string]string{})

	assert.Len(t, got, 1)
	assert.Empty(t, got[0].LocalPath)
}

func TestMergeRegisteredPlugins_OverlappingName(t *testing.T) {
	orbital := []plugin_manager.Plugin{
		{Name: "aws", Namespace: "aws", InstalledVersion: "1.2.3", Type: "resource"},
	}
	registered := []messages.RegisteredPluginInfo{
		{Name: "aws", Namespace: "aws", Version: "1.2.3"},
	}

	got := mergeRegisteredPlugins(orbital, registered, nil)

	assert.Len(t, got, 1)
	assert.Equal(t, "aws", got[0].Name)
}

func TestMergeRegisteredPlugins_OverlappingNameCaseInsensitive(t *testing.T) {
	orbital := []plugin_manager.Plugin{
		{Name: "azure", Namespace: "azure", InstalledVersion: "0.1.0", Type: "resource"},
	}
	registered := []messages.RegisteredPluginInfo{
		{Name: "Azure", Namespace: "azure", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(orbital, registered, nil)

	assert.Len(t, got, 1)
}

// Two plugins sharing a namespace but with different names must both
// surface — dedupe is on plugin name, not namespace.
func TestMergeRegisteredPlugins_MultiplePluginsPerNamespace(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Name: "azure-storage", Namespace: "azure", Version: "0.1.0"},
		{Name: "azure-network", Namespace: "azure", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(nil, registered, nil)

	assert.Len(t, got, 2)
}

func TestMergeRegisteredPlugins_SkipsEmptyName(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Name: "", Namespace: "azure", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(nil, registered, nil)

	assert.Empty(t, got)
}
