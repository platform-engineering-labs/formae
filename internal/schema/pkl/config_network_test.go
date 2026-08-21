//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailscaleEgressProxyPortCarriesThrough(t *testing.T) {
	config, err := PKL{}.FormaeConfig("./testdata/config/network_egress_proxy.pkl")
	require.NoError(t, err)

	require.NotNil(t, config.Network)
	require.NotNil(t, config.Network.Tailscale)
	assert.Equal(t, 1080, config.Network.Tailscale.EgressProxyPort)
}

// A tailscale config with no egressProxyPort set leaves the knob at 0, which
// is what makes a config written before the egress proxy existed behave
// exactly as it did before.
func TestTailscaleEgressProxyPortAbsentDefaultsToZero(t *testing.T) {
	config, err := PKL{}.FormaeConfig("./testdata/config/test_config.pkl")
	require.NoError(t, err)

	require.NotNil(t, config.Network)
	require.NotNil(t, config.Network.Tailscale)
	assert.Equal(t, 0, config.Network.Tailscale.EgressProxyPort)
}
