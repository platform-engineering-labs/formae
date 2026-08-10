// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"encoding/json"
	"time"
)

// ErrorCode identifies a category of auth plugin error, letting callers
// branch on failure kind without parsing the human-readable Error string.
type ErrorCode string

const (
	// ErrorCodeUnsupported indicates the plugin does not implement this verb.
	ErrorCodeUnsupported ErrorCode = "unsupported"
	// ErrorCodeNotLoggedIn indicates there is no active session.
	ErrorCodeNotLoggedIn ErrorCode = "not_logged_in"
	// ErrorCodeSessionExpired indicates a previously active session has expired.
	ErrorCodeSessionExpired ErrorCode = "session_expired"
	// ErrorCodeIssuerUnreachable indicates the plugin could not reach its identity issuer.
	ErrorCodeIssuerUnreachable ErrorCode = "issuer_unreachable"
)

// AuthPlugin is the interface that auth plugin binaries must implement.
// Method signatures are net/rpc compatible (two args: request pointer, response pointer; returns error).
type AuthPlugin interface {
	Init(req *InitRequest, resp *InitResponse) error
	Validate(req *ValidateRequest, resp *ValidateResponse) error
	// GetAuthHeader: to fail closed on every host version, return a non-nil
	// error rather than relying on GetAuthHeaderResponse's typed fields,
	// which a host built against an earlier SDK cannot read.
	GetAuthHeader(req *GetAuthHeaderRequest, resp *GetAuthHeaderResponse) error
	LoginStart(req *LoginStartRequest, resp *LoginStartResponse) error
	LoginWait(req *LoginWaitRequest, resp *LoginWaitResponse) error
	Logout(req *LogoutRequest, resp *LogoutResponse) error
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
	CacheKey string // retained for SOURCE compatibility with existing plugins; the host never reads it
	CacheTTL time.Duration

	Subject     string // verified stable subject id; empty when the plugin has no notion of one
	SubjectName string // display hint only; never used for authorization
	ErrorCode   ErrorCode
}

// GetAuthHeaderRequest requests auth headers for outgoing requests (CLI side).
type GetAuthHeaderRequest struct {
	ForceRefresh bool // skip freshness checks and refresh the credential now
}

// GetAuthHeaderResponse contains the headers to attach to outgoing requests.
//
// Error and ErrorCode were added in SDK 0.3.0. A host built against an
// earlier SDK decodes only Headers and cannot observe either field, so a
// nil error paired with empty Headers reads as successful authentication.
// A plugin that must fail closed regardless of host version returns a
// non-nil Go error from GetAuthHeader instead; these typed fields are
// additive information for hosts new enough to read them, not a
// substitute for that error.
type GetAuthHeaderResponse struct {
	Headers   map[string][]string
	Error     string
	ErrorCode ErrorCode
}

// LoginStartRequest begins an interactive login flow.
type LoginStartRequest struct {
	Mode  string // "browser" | "device"
	Force bool
}

// LoginStartResponse describes how the caller should carry out (or skip) the login flow.
type LoginStartResponse struct {
	Status           string // "started" | "already_authenticated"
	Method           string // echoes Mode when Status=="started"
	BrowserURL       string
	VerificationURI  string
	UserCode         string
	SessionID        string
	ExpiresInSeconds int
	Subject          string // set when Status=="already_authenticated"
	SubjectName      string
	Error            string
	ErrorCode        ErrorCode
}

// LoginWaitRequest polls for completion of a login flow started by LoginStart.
type LoginWaitRequest struct{ SessionID string }

// LoginWaitResponse reports the outcome of a login flow.
type LoginWaitResponse struct {
	Subject     string
	SubjectName string
	Error       string
	ErrorCode   ErrorCode
}

// LogoutRequest ends the current session.
type LogoutRequest struct{}

// LogoutResponse is the plugin's response to Logout.
type LogoutResponse struct {
	Error     string
	ErrorCode ErrorCode
}
