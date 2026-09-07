// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package network

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// mockProxy is a minimal Proxy used to verify that StartEgressProxy returns
// exactly what the plugin handed back.
type mockProxy struct{}

func (m *mockProxy) Serve()      {}
func (m *mockProxy) Stop(_ bool) {}

// mockEgressNetworkPlugin implements both NetworkPlugin and EgressProxy, and
// records the arguments it was called with so tests can assert pass-through.
type mockEgressNetworkPlugin struct {
	mockNetworkPlugin

	proxy Proxy
	err   error

	gotCtx    context.Context
	gotConfig json.RawMessage
	called    bool
}

func (m *mockEgressNetworkPlugin) StartEgressProxy(ctx context.Context, config json.RawMessage) (Proxy, error) {
	m.called = true
	m.gotCtx = ctx
	m.gotConfig = config
	return m.proxy, m.err
}

func TestStartEgressProxy_NilConfigReturnsNil(t *testing.T) {
	proxy, err := StartEgressProxy(context.Background(), nil)

	require.NoError(t, err)
	assert.Nil(t, proxy)
}

func TestStartEgressProxy_NoCapabilityAndNoPortReturnsNil(t *testing.T) {
	plugin := &mockNetworkPlugin{name: "no-egress-no-port"}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:      plugin.Name(),
		Tailscale: &pkgmodel.TailscaleConfig{EgressProxyPort: 0},
	}

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.NoError(t, err)
	assert.Nil(t, proxy)
}

func TestStartEgressProxy_NoCapabilityWithPortReturnsError(t *testing.T) {
	plugin := &mockNetworkPlugin{name: "no-egress-with-port"}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:      plugin.Name(),
		Tailscale: &pkgmodel.TailscaleConfig{EgressProxyPort: 1080},
	}

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.Error(t, err)
	assert.Nil(t, proxy)
	assert.Contains(t, err.Error(), plugin.Name())
	assert.Contains(t, err.Error(), "egress")
}

func TestStartEgressProxy_CapableWithPortDelegates(t *testing.T) {
	wantProxy := &mockProxy{}
	plugin := &mockEgressNetworkPlugin{
		mockNetworkPlugin: mockNetworkPlugin{name: "egress-capable"},
		proxy:             wantProxy,
	}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:      plugin.Name(),
		Tailscale: &pkgmodel.TailscaleConfig{EgressProxyPort: 1080, Hostname: "agent-1"},
	}

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

	proxy, err := StartEgressProxy(ctx, cfg)

	require.NoError(t, err)
	assert.Same(t, wantProxy, proxy)
	require.True(t, plugin.called)
	assert.Equal(t, ctx, plugin.gotCtx)
	assert.Contains(t, string(plugin.gotConfig), `"EgressProxyPort":1080`)
	assert.Contains(t, string(plugin.gotConfig), `"Hostname":"agent-1"`)
}

func TestStartEgressProxy_CapablePluginPropagatesStartError(t *testing.T) {
	wantErr := assert.AnError
	plugin := &mockEgressNetworkPlugin{
		mockNetworkPlugin: mockNetworkPlugin{name: "egress-start-fails"},
		err:               wantErr,
	}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:      plugin.Name(),
		Tailscale: &pkgmodel.TailscaleConfig{EgressProxyPort: 1080},
	}

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, proxy)
}

func TestStartEgressProxy_LegacyRawJSONPath(t *testing.T) {
	wantProxy := &mockProxy{}
	plugin := &mockEgressNetworkPlugin{
		mockNetworkPlugin: mockNetworkPlugin{name: "egress-legacy"},
		proxy:             wantProxy,
	}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:          plugin.Name(),
		LegacyRawJSON: json.RawMessage(`{"AuthKey":"tskey-xxx","EgressProxyPort":1081}`),
	}

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.NoError(t, err)
	assert.Same(t, wantProxy, proxy)
	assert.Equal(t, json.RawMessage(`{"AuthKey":"tskey-xxx","EgressProxyPort":1081}`), plugin.gotConfig)
}

func TestStartEgressProxy_LegacyRawJSONNoPortReturnsNil(t *testing.T) {
	plugin := &mockNetworkPlugin{name: "no-egress-legacy"}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:          plugin.Name(),
		LegacyRawJSON: json.RawMessage(`{"AuthKey":"tskey-xxx"}`),
	}

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.NoError(t, err)
	assert.Nil(t, proxy)
}

func TestStartEgressProxy_TypeSetWithTailscaleNilReturnsNil(t *testing.T) {
	plugin := &mockNetworkPlugin{name: "no-egress-nil-tailscale"}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type:      plugin.Name(),
		Tailscale: nil,
	}

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.NoError(t, err)
	assert.Nil(t, proxy)
}

// TestStartEgressProxy_EgressProxyPortSurvivesConfigMarshal builds a real
// pkgmodel.NetworkConfig and drives it through PluginConfigJSON's actual
// marshaling (rather than a hand-written JSON literal), proving the
// untagged TailscaleConfig.EgressProxyPort field round-trips under the
// exact key the gjson probe looks for. A plugin that does not implement
// EgressProxy must therefore see the requested-but-unsupported error, not a
// silent nil,nil that would result if the probe missed the field.
func TestStartEgressProxy_EgressProxyPortSurvivesConfigMarshal(t *testing.T) {
	plugin := &mockNetworkPlugin{name: "no-egress-round-trip"}
	DefaultRegistry.Register(plugin)

	cfg := &pkgmodel.NetworkConfig{
		Type: plugin.Name(),
		Tailscale: &pkgmodel.TailscaleConfig{
			AuthKey:         "tskey-xxx",
			EgressProxyPort: 1082,
		},
	}

	configJSON, err := cfg.PluginConfigJSON()
	require.NoError(t, err)
	require.Contains(t, string(configJSON), `"EgressProxyPort":1082`)

	proxy, err := StartEgressProxy(context.Background(), cfg)

	require.Error(t, err)
	assert.Nil(t, proxy)
	assert.Contains(t, err.Error(), plugin.Name())
}
