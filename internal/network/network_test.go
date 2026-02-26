// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package network

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockNetworkPlugin implements NetworkPlugin for registry tests.
type mockNetworkPlugin struct {
	name string
}

func (m *mockNetworkPlugin) Name() string { return m.name }
func (m *mockNetworkPlugin) Client(_ json.RawMessage) (*http.Client, error) {
	panic("not implemented")
}
func (m *mockNetworkPlugin) Listen(_ json.RawMessage, _ int) (net.Listener, error) {
	panic("not implemented")
}

func TestRegistry_RegisterAndGetByName(t *testing.T) {
	r := NewRegistry()
	plugin := &mockNetworkPlugin{name: "tailscale"}

	r.Register(plugin)

	got, err := r.Get("tailscale")
	require.NoError(t, err)
	assert.Equal(t, plugin, got)
}

func TestRegistry_GetUnknownNameReturnsError(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestRegistry_GetIsCaseInsensitive(t *testing.T) {
	r := NewRegistry()
	plugin := &mockNetworkPlugin{name: "tailscale"}

	r.Register(plugin)

	got, err := r.Get("Tailscale")
	require.NoError(t, err)
	assert.Equal(t, plugin, got)
}

func TestRegistry_GetByConfigExtractsType(t *testing.T) {
	r := NewRegistry()
	plugin := &mockNetworkPlugin{name: "tailscale"}
	r.Register(plugin)

	config := json.RawMessage(`{"Type": "tailscale", "AuthKey": "tskey-xxx"}`)
	got, err := r.GetByConfig(config)
	require.NoError(t, err)
	assert.Equal(t, plugin, got)
}

func TestRegistry_GetByConfigMissingType(t *testing.T) {
	r := NewRegistry()

	config := json.RawMessage(`{"AuthKey": "tskey-xxx"}`)
	_, err := r.GetByConfig(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Type")
}

func TestRegistry_RegisterMultipleAndRetrieveEach(t *testing.T) {
	r := NewRegistry()
	ts := &mockNetworkPlugin{name: "tailscale"}
	wg := &mockNetworkPlugin{name: "wireguard"}

	r.Register(ts)
	r.Register(wg)

	got, err := r.Get("tailscale")
	require.NoError(t, err)
	assert.Equal(t, ts, got)

	got, err = r.Get("wireguard")
	require.NoError(t, err)
	assert.Equal(t, wg, got)
}
