// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package network

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/imconc"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Proxy is a running egress proxy started by a network plugin.
type Proxy interface {
	imconc.Routine // Stop(force bool)
	Serve()
}

// EgressProxy is an optional capability of a NetworkPlugin: a plugin that can
// proxy outbound traffic through its network, in addition to serving inbound
// connections via NetworkPlugin.Listen. Plugins with no notion of egress do
// not implement it.
type EgressProxy interface {
	StartEgressProxy(ctx context.Context, config json.RawMessage) (Proxy, error)
}

// StartEgressProxy resolves the network plugin named by cfg.Type and starts
// its egress proxy on the port carried in cfg.
//
// It returns (nil, nil) when there is no network config, or when the
// configured egress port is absent or 0 — the default, egress-off state. It
// returns an error when a non-zero egress port is requested but the resolved
// plugin does not implement EgressProxy, so a requested-but-unsupported
// configuration surfaces as a diagnostic rather than an agent that starts
// and never proxies.
func StartEgressProxy(ctx context.Context, cfg *pkgmodel.NetworkConfig) (Proxy, error) {
	if cfg == nil {
		return nil, nil
	}

	configJSON, err := cfg.PluginConfigJSON()
	if err != nil {
		return nil, err
	}

	port := gjson.GetBytes(configJSON, "EgressProxyPort").Int()
	if port == 0 {
		return nil, nil
	}

	plugin, err := DefaultRegistry.Get(cfg.Type)
	if err != nil {
		return nil, err
	}

	egressPlugin, ok := plugin.(EgressProxy)
	if !ok {
		return nil, fmt.Errorf("network plugin %q does not support egress proxying, but egress port %d was requested", cfg.Type, port)
	}

	return egressPlugin.StartEgressProxy(ctx, configJSON)
}
