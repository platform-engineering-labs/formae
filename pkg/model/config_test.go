// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkConfig_PluginConfigJSON_LegacyRawJSONTakesPrecedence(t *testing.T) {
	legacy := json.RawMessage(`{"legacy":true}`)
	cfg := &NetworkConfig{
		Type: "tailscale",
		Tailscale: &TailscaleConfig{
			Hostname: "should-be-ignored",
		},
		LegacyRawJSON: legacy,
	}

	got, err := cfg.PluginConfigJSON()
	require.NoError(t, err)
	assert.JSONEq(t, string(legacy), string(got))
}

func TestNetworkConfig_PluginConfigJSON_MarshalsTypedTailscale(t *testing.T) {
	cfg := &NetworkConfig{
		Type: "tailscale",
		Tailscale: &TailscaleConfig{
			TLS:           true,
			AuthKey:       "key-123",
			Hostname:      "formae-agent",
			AdvertiseTags: []string{"tag:formae"},
		},
	}

	got, err := cfg.PluginConfigJSON()
	require.NoError(t, err)

	want, err := json.Marshal(cfg.Tailscale)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

func TestNetworkConfig_PluginConfigJSON_NilTailscaleMarshalsNull(t *testing.T) {
	cfg := &NetworkConfig{
		Type:      "tailscale",
		Tailscale: nil,
	}

	got, err := cfg.PluginConfigJSON()
	require.NoError(t, err)
	assert.Equal(t, "null", string(got))
}
