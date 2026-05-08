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

// The make-install case: agent has registered the plugin, orbital has no
// record. mergeRegisteredPlugins must surface a synthetic entry with the
// disk-discovered LocalPath so extract --schema-location local can resolve
// the schema.
func TestMergeRegisteredPlugins_RegisteredNotInOrbital(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Namespace: "azure", Version: "0.1.0"},
	}
	paths := map[string]string{"azure": "/plugins/azure/v0.1.0/schema/pkl/PklProject"}

	got := mergeRegisteredPlugins(nil, registered, paths)

	assert.Len(t, got, 1)
	assert.Equal(t, "azure", got[0].Namespace)
	assert.Equal(t, "azure", got[0].Name)
	assert.Equal(t, "resource", got[0].Type)
	assert.Equal(t, "plugin", got[0].Kind)
	assert.Equal(t, "0.1.0", got[0].InstalledVersion)
	assert.Equal(t, "/plugins/azure/v0.1.0/schema/pkl/PklProject", got[0].LocalPath)
}

// A registered plugin without a schema PklProject on disk (auth plugin, or
// half-installed resource plugin) still surfaces, with empty LocalPath. A
// downstream `--schema-location local` request will fail with the same-box
// error rather than silently producing a broken URI.
func TestMergeRegisteredPlugins_RegisteredWithoutLocalPath(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Namespace: "azure", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(nil, registered, map[string]string{})

	assert.Len(t, got, 1)
	assert.Equal(t, "azure", got[0].Namespace)
	assert.Empty(t, got[0].LocalPath)
}

// A plugin known to both orbital and the registry must appear once. Orbital
// is the canonical source for metadata (display name, category, channel),
// so the orbital entry wins.
func TestMergeRegisteredPlugins_OverlappingNamespace(t *testing.T) {
	orbital := []plugin_manager.Plugin{
		{Name: "formae-plugin-aws", Namespace: "aws", InstalledVersion: "1.2.3", Type: "resource"},
	}
	registered := []messages.RegisteredPluginInfo{
		{Namespace: "aws", Version: "1.2.3"},
	}

	got := mergeRegisteredPlugins(orbital, registered, nil)

	assert.Len(t, got, 1)
	assert.Equal(t, "formae-plugin-aws", got[0].Name)
}

// Namespace match is case-insensitive: PluginCoordinator can register
// either "Azure" or "azure" depending on the manifest, while orbital
// metadata is conventionally lowercase. The merge must dedupe regardless.
func TestMergeRegisteredPlugins_OverlappingNamespaceCaseInsensitive(t *testing.T) {
	orbital := []plugin_manager.Plugin{
		{Name: "formae-plugin-azure", Namespace: "azure", InstalledVersion: "0.1.0", Type: "resource"},
	}
	registered := []messages.RegisteredPluginInfo{
		{Namespace: "Azure", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(orbital, registered, nil)

	assert.Len(t, got, 1)
}

// Registered entries with empty namespace are skipped — there's nothing to
// route on, and they would collide on the empty-string key.
func TestMergeRegisteredPlugins_SkipsEmptyNamespace(t *testing.T) {
	registered := []messages.RegisteredPluginInfo{
		{Namespace: "", Version: "0.1.0"},
	}

	got := mergeRegisteredPlugins(nil, registered, nil)

	assert.Empty(t, got)
}
