// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func TestResourcePluginConfig_DisabledPlugin(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.ResourcePlugins = []pkgmodel.ResourcePluginUserConfig{
			{
				Type:    "fakeaws",
				Enabled: false,
			},
		}

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
		defer def()
		require.NoError(t, err)

		// Start a test helper actor so we can interact with the actor system
		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		require.NoError(t, err)

		// Simulate a plugin announcing itself to the PluginCoordinator.
		// Because the config has Enabled=false for "fakeaws", the coordinator
		// should reject the announcement and not register the plugin.
		err = testutil.Send(m.Node, "PluginCoordinator", messages.PluginAnnouncement{
			Namespace:            "FakeAWS",
			Version:              "0.0.1",
			NodeName:             "fake-plugin-node",
			MaxRequestsPerSecond: 10,
			Capabilities: plugin.PluginCapabilities{
				SupportedResources: []plugin.ResourceDescriptor{
					{Type: "FakeAWS::S3::Bucket"},
				},
			},
		})
		require.NoError(t, err)

		// Give the coordinator time to process the announcement
		time.Sleep(500 * time.Millisecond)

		// Query the coordinator for registered plugins — the disabled plugin
		// should NOT appear in the list.
		result, err := testutil.Call(m.Node, "PluginCoordinator", messages.GetRegisteredPlugins{})
		require.NoError(t, err)

		pluginsResult, ok := result.(messages.GetRegisteredPluginsResult)
		require.True(t, ok, "expected GetRegisteredPluginsResult, got %T", result)
		assert.Empty(t, pluginsResult.Plugins, "disabled plugin should not be registered")
	})
}

func TestResourcePluginConfig_RateLimitOverride(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.ResourcePlugins = []pkgmodel.ResourcePluginUserConfig{
			{
				Type:      "fakeaws",
				Enabled:   true,
				RateLimit: &pkgmodel.RateLimitConfig{Scope: pkgmodel.RateLimitScopeNamespace, MaxRequestsPerSecondForNamespace: 3},
			},
		}

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
		defer def()
		require.NoError(t, err)

		// Start a test helper actor so we can interact with the actor system
		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		require.NoError(t, err)

		// Simulate a plugin announcing itself with a default rate limit of 10.
		// The user config overrides this to 3.
		err = testutil.Send(m.Node, "PluginCoordinator", messages.PluginAnnouncement{
			Namespace:            "FakeAWS",
			Version:              "0.0.1",
			NodeName:             "fake-plugin-node",
			MaxRequestsPerSecond: 10,
			Capabilities: plugin.PluginCapabilities{
				SupportedResources: []plugin.ResourceDescriptor{
					{Type: "FakeAWS::S3::Bucket"},
				},
			},
		})
		require.NoError(t, err)

		// Give the coordinator time to process the announcement
		time.Sleep(500 * time.Millisecond)

		// Query the coordinator for registered plugins — the plugin should be
		// registered with the overridden rate limit of 3, not the announced 10.
		result, err := testutil.Call(m.Node, "PluginCoordinator", messages.GetRegisteredPlugins{})
		require.NoError(t, err)

		pluginsResult, ok := result.(messages.GetRegisteredPluginsResult)
		require.True(t, ok, "expected GetRegisteredPluginsResult, got %T", result)
		require.Len(t, pluginsResult.Plugins, 1, "enabled plugin should be registered")
		assert.Equal(t, "FakeAWS", pluginsResult.Plugins[0].Namespace)
		assert.Equal(t, 3, pluginsResult.Plugins[0].MaxRequestsPerSecond, "rate limit should be overridden from 10 to 3")
		assert.Equal(t, 1, pluginsResult.Plugins[0].ResourceCount)
	})
}
