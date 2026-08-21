// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeResponse_FailClosedMapping(t *testing.T) {
	cases := map[string]struct {
		resp IdentityTokenResponse
		want error
	}{
		"nil result no code": {IdentityTokenResponse{}, ErrInternal},
		"unknown code":       {IdentityTokenResponse{ErrorCode: "gremlins"}, ErrInternal},
		"empty token":        {IdentityTokenResponse{Result: &OidcIdentityTokenResult{}}, ErrInternal},
		"invalid audience":   {IdentityTokenResponse{ErrorCode: ErrCodeInvalidAudience}, ErrInvalidAudience},
		"mint failed":        {IdentityTokenResponse{ErrorCode: ErrCodeMintFailed, ErrorMessage: "boom"}, ErrMintFailed},
		"broker unavailable": {IdentityTokenResponse{ErrorCode: ErrCodeBrokerUnavailable}, ErrBrokerUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := Encode(&tc.resp)
			require.NoError(t, err)
			_, err = DecodeResponse(b)
			require.ErrorIs(t, err, tc.want)
		})
	}
}
