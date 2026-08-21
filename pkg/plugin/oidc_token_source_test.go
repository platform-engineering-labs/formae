// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/credential"
)

func TestCtxSource_NoClientInCtx_ErrorsIsNoBroker(t *testing.T) {
	_, err := (&ctxOidcTokenSource{}).IdentityToken(context.Background(), "sts.amazonaws.com")
	require.ErrorIs(t, err, ErrNoOidcBroker)
}

func TestCtxSource_RoundTripsThroughCallFunc(t *testing.T) {
	c := &oidcBrokerClient{namespace: "AWS", call: func(payload []byte) ([]byte, error) {
		var req credential.OidcIdentityTokenRequest
		require.NoError(t, credential.Decode(payload, &req))
		require.Equal(t, "sts.amazonaws.com", req.Audience)
		require.NotEmpty(t, req.RequestID)
		return credential.Encode(&credential.IdentityTokenResponse{
			Result: &credential.OidcIdentityTokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Hour)},
		})
	}}

	tok, err := (&ctxOidcTokenSource{}).IdentityToken(withOidcBrokerClient(context.Background(), c), "sts.amazonaws.com")

	require.NoError(t, err)
	require.Equal(t, "jwt", tok)
}

// Every call carries its own request id, so two calls through the same client
// are distinguishable on the broker side.
func TestCtxSource_GeneratesARequestIDPerCall(t *testing.T) {
	var seen []string
	c := &oidcBrokerClient{namespace: "AWS", call: func(payload []byte) ([]byte, error) {
		var req credential.OidcIdentityTokenRequest
		require.NoError(t, credential.Decode(payload, &req))
		seen = append(seen, req.RequestID)
		return credential.Encode(&credential.IdentityTokenResponse{
			Result: &credential.OidcIdentityTokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Hour)},
		})
	}}
	ctx := withOidcBrokerClient(context.Background(), c)
	source := &ctxOidcTokenSource{}

	_, err := source.IdentityToken(ctx, "sts.amazonaws.com")
	require.NoError(t, err)
	_, err = source.IdentityToken(ctx, "sts.amazonaws.com")
	require.NoError(t, err)

	require.Len(t, seen, 2)
	assert.NotEqual(t, seen[0], seen[1])
}

func TestCtxSource_TypedEnvelopeErrors(t *testing.T) {
	c := &oidcBrokerClient{namespace: "AWS", call: func([]byte) ([]byte, error) {
		return credential.Encode(&credential.IdentityTokenResponse{
			ErrorCode:    credential.ErrCodeInvalidAudience,
			ErrorMessage: "audience sts.amazonaws.com is not allowed",
		})
	}}

	_, err := (&ctxOidcTokenSource{}).IdentityToken(withOidcBrokerClient(context.Background(), c), "sts.amazonaws.com")

	require.ErrorIs(t, err, credential.ErrInvalidAudience)
}

func TestCtxSource_TransportFailureNamesTheNamespace(t *testing.T) {
	callErr := errors.New("no route to broker node")
	c := &oidcBrokerClient{namespace: "AWS", call: func([]byte) ([]byte, error) {
		return nil, callErr
	}}

	_, err := (&ctxOidcTokenSource{}).IdentityToken(withOidcBrokerClient(context.Background(), c), "sts.amazonaws.com")

	require.ErrorIs(t, err, callErr)
	assert.Contains(t, err.Error(), "AWS")
}
