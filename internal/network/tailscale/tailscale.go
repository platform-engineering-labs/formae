// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/platform-engineering-labs/formae/internal/network"
)

// defaultHostname is the tailnet hostname used when the config omits one.
const defaultHostname = "formae"

func init() {
	network.DefaultRegistry.Register(&Tailscale{})
}

var _ network.NetworkPlugin = (*Tailscale)(nil)

// Tailscale implements the NetworkPlugin interface using Tailscale's tsnet.
//
// All entry points share a single tsnet node. A tsnet server derives its
// state directory and node identity from the running executable, so two
// servers in one process would fight over the same state; the node is built
// on the first call that needs it and reused by every later call.
type Tailscale struct {
	// newNode builds the shared node from the resolved config. Nil in the
	// registered plugin, where it falls back to the tsnet-backed node;
	// tests inject a fake so no test ever joins a tailnet.
	newNode func(cfg *Config) node

	mu   sync.Mutex
	node node
	// hostname and advertiseTags are the identity the running node was
	// built with, kept so a later config that would need a different node
	// can be rejected instead of silently reusing this one.
	hostname      string
	advertiseTags []string
}

func (t *Tailscale) Name() string {
	return "tailscale"
}

func (t *Tailscale) Client(config json.RawMessage) (*http.Client, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}

	n, err := t.sharedNode(cfg)
	if err != nil {
		return nil, err
	}

	return n.HTTPClient(), nil
}

func (t *Tailscale) Listen(config json.RawMessage, port int) (net.Listener, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}

	n, err := t.sharedNode(cfg)
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf(":%d", port)

	if cfg.Tls {
		return n.ListenTLS("tcp", addr)
	}

	return n.Listen("tcp", addr)
}

// sharedNode returns the process-wide tsnet node, building it from cfg on the
// first call. Later calls reuse it, and are rejected when cfg describes a node
// identity the running node cannot serve.
func (t *Tailscale) sharedNode(cfg *Config) (node, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	resolved := *cfg
	resolved.Hostname = valueOrDefault(cfg.Hostname, defaultHostname)

	if t.node != nil {
		if err := t.checkNodeIdentity(&resolved); err != nil {
			return nil, err
		}

		return t.node, nil
	}

	newNode := t.newNode
	if newNode == nil {
		newNode = newTsnetNode
	}

	t.node = newNode(&resolved)
	t.hostname = resolved.Hostname
	t.advertiseTags = slices.Clone(resolved.AdvertiseTags)

	return t.node, nil
}

// checkNodeIdentity reports whether cfg describes the node that is already
// running. Hostname and advertise tags become the node's prefs when it starts
// and cannot be changed by a later caller, so a difference is an error. The
// auth key is deliberately not compared: it is consumed only while the node
// starts, so a second caller carrying a different (or rotated) key still
// describes the same node.
//
// The error names the differing fields but never their values — a config
// value must not be able to reach a log line by way of an error message.
func (t *Tailscale) checkNodeIdentity(cfg *Config) error {
	var differing []string

	if cfg.Hostname != t.hostname {
		differing = append(differing, "hostname")
	}

	if !sameTags(cfg.AdvertiseTags, t.advertiseTags) {
		differing = append(differing, "advertiseTags")
	}

	if len(differing) == 0 {
		return nil
	}

	return fmt.Errorf("tailscale: configuration does not match the running node: differing %s",
		strings.Join(differing, ", "))
}

// sameTags reports whether two advertise tag lists describe the same set. Tags
// are a set on the node's prefs, so ordering carries no meaning.
func sameTags(a, b []string) bool {
	sortedA := slices.Sorted(slices.Values(a))
	sortedB := slices.Sorted(slices.Values(b))

	return slices.Equal(sortedA, sortedB)
}

// parseConfig decodes and validates the plugin config JSON.
func parseConfig(config json.RawMessage) (*Config, error) {
	cfg := &Config{}

	err := json.Unmarshal(config, cfg)
	if err != nil {
		return nil, fmt.Errorf("tailscale: error parsing config: %v", err)
	}

	if cfg.AuthKey == "" {
		return nil, fmt.Errorf("tailscale: configuration missing auth key")
	}

	return cfg, nil
}
