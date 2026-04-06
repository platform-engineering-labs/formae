// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"time"
)

// AuthPlugin is the interface that auth plugin binaries must implement.
// Method signatures are net/rpc compatible (two args: request pointer, response pointer; returns error).
type AuthPlugin interface {
	Init(req *InitRequest, resp *InitResponse) error
	Validate(req *ValidateRequest, resp *ValidateResponse) error
	GetAuthHeader(req *GetAuthHeaderRequest, resp *GetAuthHeaderResponse) error
}

// InitRequest is sent once at startup to configure the plugin.
type InitRequest struct {
	Config json.RawMessage
}

// InitResponse is the plugin's response to Init.
type InitResponse struct {
	Error string
}

// ValidateRequest contains the HTTP headers to validate.
type ValidateRequest struct {
	Headers map[string][]string
}

// ValidateResponse contains the validation result and caching hints.
type ValidateResponse struct {
	Valid    bool
	Error    string
	CacheKey string
	CacheTTL time.Duration
}

// GetAuthHeaderRequest requests auth headers for outgoing requests (CLI side).
type GetAuthHeaderRequest struct{}

// GetAuthHeaderResponse contains the headers to attach to outgoing requests.
type GetAuthHeaderResponse struct {
	Headers map[string][]string
}
