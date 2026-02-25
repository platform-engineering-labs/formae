// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/platform-engineering-labs/formae/internal/network"
	"tailscale.com/tsnet"
)

func init() {
	network.DefaultRegistry.Register(Tailscale{})
}

var _ network.NetworkPlugin = Tailscale{}

// Tailscale implements the NetworkPlugin interface using Tailscale's tsnet.
type Tailscale struct{}

func (t Tailscale) Name() string {
	return "tailscale"
}

func (t Tailscale) Client(config json.RawMessage) (*http.Client, error) {
	cfg := &Config{}
	err := json.Unmarshal(config, cfg)
	if err != nil {
		return nil, fmt.Errorf("tailscale: error parsing config: %v", err)
	}

	if cfg.AuthKey == "" {
		return nil, fmt.Errorf("tailscale: configuration missing auth key")
	}

	srv := new(tsnet.Server)
	srv.Hostname = valueOrDefault(cfg.Hostname, "formae")
	srv.AuthKey = cfg.AuthKey
	srv.AdvertiseTags = cfg.AdvertiseTags

	return srv.HTTPClient(), nil
}

func (t Tailscale) Listen(config json.RawMessage, port int) (net.Listener, error) {
	cfg := &Config{}
	err := json.Unmarshal(config, cfg)
	if err != nil {
		return nil, fmt.Errorf("tailscale: error parsing config: %v", err)
	}

	if cfg.AuthKey == "" {
		return nil, fmt.Errorf("tailscale: configuration missing auth key")
	}

	srv := new(tsnet.Server)
	srv.Hostname = valueOrDefault(cfg.Hostname, "formae")
	srv.AuthKey = cfg.AuthKey
	srv.AdvertiseTags = cfg.AdvertiseTags

	var ln net.Listener

	if cfg.Tls {
		ln, err = srv.ListenTLS("tcp", fmt.Sprintf(":%d", port))
	} else {
		ln, err = srv.Listen("tcp", fmt.Sprintf(":%d", port))
	}

	if err != nil {
		return nil, err
	}

	return ln, nil
}
