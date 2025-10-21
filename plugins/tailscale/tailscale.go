// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"tailscale.com/tsnet"
)

type Tailscale struct{}

// Version set at compile time
var Version = "0.0.0"

// Compile time checks to satisfy protocol
var _ plugin.Plugin = Tailscale{}
var _ plugin.NetworkPlugin = Tailscale{}

// Plugin maintains the known symbol reference
var Plugin = Tailscale{}

func (t Tailscale) Name() string {
	return "tailscale"
}

func (t Tailscale) Type() plugin.Type {
	return plugin.Network
}

func (t Tailscale) Version() *semver.Version {
	return semver.MustParse(Version)
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
	srv.Hostname = ValueOrDefault(cfg.Hostname, "formae")
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
	srv.Hostname = ValueOrDefault(cfg.Hostname, "formae")
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
