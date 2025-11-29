// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"context"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetastructure_NetworkingEnabled(t *testing.T) {
	cfg := &model.Config{
		Agent: model.AgentConfig{
			Server: model.ServerConfig{
				Nodename: "test-agent",
				Hostname: "localhost",
				Secret:   "test-secret",
			},
		},
	}

	pluginManager := plugin.NewManager("")

	m, err := NewMetastructure(context.Background(), cfg, pluginManager, "test")
	require.NoError(t, err)
	require.NotNil(t, m)

	// Verify networking is enabled
	assert.NotEqual(t, gen.NetworkModeDisabled, m.options.Network.Mode,
		"Network mode should not be disabled")
	assert.Equal(t, gen.NetworkModeEnabled, m.options.Network.Mode,
		"Network mode should be enabled")

	// Verify cookie is set from config
	assert.Equal(t, "test-secret", m.options.Network.Cookie,
		"Network cookie should match config secret")
}
