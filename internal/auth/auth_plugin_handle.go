// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"fmt"
	"net/rpc"
	"strings"
	"sync/atomic"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

const AuthTagPrefix = "auth:"

// IsAuthTag returns true if the tag identifies an auth plugin.
func IsAuthTag(tag string) bool {
	return strings.HasPrefix(tag, AuthTagPrefix)
}

// AuthTagName extracts the plugin name from an auth tag.
func AuthTagName(tag string) string {
	return strings.TrimPrefix(tag, AuthTagPrefix)
}

// AuthTag creates an auth plugin tag from a plugin name.
func AuthTag(name string) string {
	return AuthTagPrefix + name
}

// AuthPluginHandle is a thread-safe handle to an auth plugin process.
// The supervisor manages the process lifecycle and swaps the RPC client
// on restarts. The middleware reads the client pointer concurrently to
// validate incoming requests.
type AuthPluginHandle struct {
	name       string
	binaryPath string
	configJSON json.RawMessage
	client     atomic.Pointer[rpc.Client]
	conn       *MetaPortConn
}

// AuthPluginConfig contains the information needed to spawn an auth plugin process.
type AuthPluginConfig struct {
	Name       string
	BinaryPath string
	ConfigJSON json.RawMessage
}

// NewAuthPluginHandle creates a new auth plugin handle.
func NewAuthPluginHandle(name, binaryPath string, configJSON json.RawMessage) *AuthPluginHandle {
	return &AuthPluginHandle{
		name:       name,
		binaryPath: binaryPath,
		configJSON: configJSON,
	}
}

// Name returns the plugin name.
func (h *AuthPluginHandle) Name() string { return h.name }

// BinaryPath returns the path to the plugin binary.
func (h *AuthPluginHandle) BinaryPath() string { return h.binaryPath }

// ConfigJSON returns the plugin configuration.
func (h *AuthPluginHandle) ConfigJSON() json.RawMessage { return h.configJSON }

// SwapClient atomically replaces the RPC client pointer.
func (h *AuthPluginHandle) SwapClient(client *rpc.Client) {
	h.client.Store(client)
}

// SetConn sets the MetaPortConn used for data routing.
func (h *AuthPluginHandle) SetConn(conn *MetaPortConn) {
	h.conn = conn
}

// Conn returns the current MetaPortConn.
func (h *AuthPluginHandle) Conn() *MetaPortConn { return h.conn }

// Feed forwards binary data from the actor to the MetaPortConn.
func (h *AuthPluginHandle) Feed(data []byte) {
	if h.conn != nil {
		h.conn.Feed(data)
	}
}

// Close closes the MetaPortConn and nils the client pointer.
func (h *AuthPluginHandle) Close() {
	if h.conn != nil {
		h.conn.Close()
	}
	h.client.Store(nil)
}

// Validate calls the auth plugin's Validate method via RPC.
// Returns an error if no client is available.
func (h *AuthPluginHandle) Validate(req *pkgauth.ValidateRequest) (*pkgauth.ValidateResponse, error) {
	client := h.client.Load()
	if client == nil {
		return nil, fmt.Errorf("auth plugin %q: no client available", h.name)
	}
	var resp pkgauth.ValidateResponse
	if err := client.Call("AuthPlugin.Validate", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
