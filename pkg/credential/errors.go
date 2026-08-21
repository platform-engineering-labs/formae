// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import (
	"errors"
	"fmt"
)

// Sentinel errors callers test with errors.Is. DecodeResponse wraps them
// with the wire ErrorMessage, when one was sent.
var (
	ErrBrokerUnavailable = errors.New("oidc-credential broker unavailable")
	ErrInvalidAudience   = errors.New("audience not allowed by the oidc-credential broker")
	ErrMintFailed        = errors.New("identity token mint failed")
	ErrInternal          = errors.New("internal oidc-credential protocol error")
)

// errorSentinels maps wire ErrorCode values to their sentinel error.
var errorSentinels = map[string]error{
	ErrCodeBrokerUnavailable: ErrBrokerUnavailable,
	ErrCodeInvalidAudience:   ErrInvalidAudience,
	ErrCodeMintFailed:        ErrMintFailed,
	ErrCodeInternal:          ErrInternal,
}

// DecodeResponse decodes an encoded IdentityTokenResponse envelope and maps
// it fail-closed:
//
//   - nil Result + empty ErrorCode -> ErrInternal
//   - unknown non-empty ErrorCode  -> ErrInternal (message wrapped)
//   - known ErrorCode              -> the matching sentinel (message wrapped)
//   - Result with an empty Token   -> ErrInternal
func DecodeResponse(data []byte) (*OidcIdentityTokenResult, error) {
	var resp IdentityTokenResponse
	if err := Decode(data, &resp); err != nil {
		return nil, err
	}

	if resp.ErrorCode != "" {
		sentinel, ok := errorSentinels[resp.ErrorCode]
		if !ok {
			sentinel = ErrInternal
		}
		if resp.ErrorMessage != "" {
			return nil, fmt.Errorf("%s: %w", resp.ErrorMessage, sentinel)
		}
		return nil, sentinel
	}

	if resp.Result == nil || resp.Result.Token == "" {
		return nil, ErrInternal
	}

	return resp.Result, nil
}
