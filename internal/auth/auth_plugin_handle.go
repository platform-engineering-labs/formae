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
	initErr    atomic.Pointer[error] // non-nil if Init failed permanently
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

// Connect establishes the connection to the auth plugin process.
// Must be called before Validate. Both conn and client are always set together
// because the rpc.Client reads/writes through the MetaPortConn.
func (h *AuthPluginHandle) Connect(conn *MetaPortConn, client *rpc.Client) {
	h.conn = conn
	h.client.Store(client)
}

// Close tears down the connection. After Close, Validate returns an error
// until Connect is called again (e.g. after a supervisor restart).
func (h *AuthPluginHandle) Close() {
	if h.conn != nil {
		_ = h.conn.Close()
	}
	h.conn = nil
	h.client.Store(nil)
}

// Feed forwards binary data from the actor to the MetaPortConn.
func (h *AuthPluginHandle) Feed(data []byte) {
	if h.conn != nil {
		h.conn.Feed(data)
	}
}

// SetInitError records a permanent init failure. Subsequent Validate calls
// return this error instead of "not connected", making the cause visible.
func (h *AuthPluginHandle) SetInitError(err error) {
	h.initErr.Store(&err)
}

// Validate calls the auth plugin's Validate method via RPC.
// Returns an error if Connect has not been called yet or the connection was closed.
func (h *AuthPluginHandle) Validate(req *pkgauth.ValidateRequest) (*pkgauth.ValidateResponse, error) {
	if errPtr := h.initErr.Load(); errPtr != nil {
		return nil, *errPtr
	}
	client := h.client.Load()
	if client == nil {
		return nil, fmt.Errorf("auth plugin %q: not connected", h.name)
	}
	var resp pkgauth.ValidateResponse
	if err := client.Call("AuthPlugin.Validate", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
