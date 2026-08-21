// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package credential defines the wire contract between the formae agent and
// an oidc-credential broker plugin: the OidcCredentialPlugin interface, the
// request/response types carried over that boundary, and the msgpack+zstd
// codec their Ergo marshaler hooks serialize them with.
package credential

import (
	"context"
	"encoding/json"
	"time"
)

// OidcCredentialPlugin mints short-lived OIDC identity tokens on behalf of
// the agent.
type OidcCredentialPlugin interface {
	IdentityToken(ctx context.Context, req *OidcIdentityTokenRequest) (*OidcIdentityTokenResult, error)
}

// Configurable is implemented by plugins that accept configuration before
// serving requests.
type Configurable interface {
	Configure(config json.RawMessage) error
}

// OidcIdentityTokenRequest asks the broker to mint an identity token scoped
// to Audience.
type OidcIdentityTokenRequest struct {
	Audience  string `json:"audience"`
	RequestID string `json:"requestId"`
}

// OidcIdentityTokenResult carries a minted identity token and its expiry.
type OidcIdentityTokenResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// IdentityTokenResponse is the wire envelope for an IdentityToken call: on
// success Result is populated, on failure ErrorCode (and optionally
// ErrorMessage) is populated. See ResponseError for the fail-closed mapping
// from this envelope to a Go error.
type IdentityTokenResponse struct {
	Result       *OidcIdentityTokenResult `json:"result,omitempty"`
	ErrorCode    string                   `json:"errorCode,omitempty"`
	ErrorMessage string                   `json:"errorMessage,omitempty"`
}

// Known ErrorCode values carried on the wire in IdentityTokenResponse.
const (
	ErrCodeBrokerUnavailable = "broker_unavailable"
	ErrCodeInvalidAudience   = "invalid_audience"
	ErrCodeMintFailed        = "mint_failed"
	ErrCodeInternal          = "internal"
)
