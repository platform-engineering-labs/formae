// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"errors"
	"sync"
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
	c := &oidcBrokerClient{namespace: "AWS", call: func(req credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		require.Equal(t, "sts.amazonaws.com", req.Audience)
		require.NotEmpty(t, req.RequestID)
		return credential.IdentityTokenResponse{
			Result: &credential.OidcIdentityTokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
	}}

	tok, err := (&ctxOidcTokenSource{}).IdentityToken(withOidcBrokerClient(context.Background(), c), "sts.amazonaws.com")

	require.NoError(t, err)
	require.Equal(t, "jwt", tok)
}

// Every call carries its own request id, so two calls through the same client
// are distinguishable on the broker side.
func TestCtxSource_GeneratesARequestIDPerCall(t *testing.T) {
	var seen []string
	c := &oidcBrokerClient{namespace: "AWS", call: func(req credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		seen = append(seen, req.RequestID)
		return credential.IdentityTokenResponse{
			Result: &credential.OidcIdentityTokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
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

// A plugin fanning an operation out over goroutines may ask for a token on each
// of them; the transport only carries one call at a time, so the client
// serializes them and every caller still gets its token.
func TestCtxSource_SerializesConcurrentCallsThroughOneClient(t *testing.T) {
	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0

	c := &oidcBrokerClient{namespace: "AWS", call: func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		// Widen the window a genuinely concurrent call would overlap in.
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()

		return credential.IdentityTokenResponse{
			Result: &credential.OidcIdentityTokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Hour)},
		}, nil
	}}
	ctx := withOidcBrokerClient(context.Background(), c)
	source := &ctxOidcTokenSource{}

	const callers = 4
	tokens := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			tokens[i], errs[i] = source.IdentityToken(ctx, "sts.amazonaws.com")
		}()
	}
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, "jwt", tokens[i])
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, maxInFlight, "the transport carries one broker call at a time")
}

func TestCtxSource_TypedEnvelopeErrors(t *testing.T) {
	c := &oidcBrokerClient{namespace: "AWS", call: func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		return credential.IdentityTokenResponse{
			ErrorCode:    credential.ErrCodeInvalidAudience,
			ErrorMessage: "audience sts.amazonaws.com is not allowed",
		}, nil
	}}

	_, err := (&ctxOidcTokenSource{}).IdentityToken(withOidcBrokerClient(context.Background(), c), "sts.amazonaws.com")

	require.ErrorIs(t, err, credential.ErrInvalidAudience)
}

func TestCtxSource_TransportFailureNamesTheNamespace(t *testing.T) {
	callErr := errors.New("no route to broker node")
	c := &oidcBrokerClient{namespace: "AWS", call: func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		return credential.IdentityTokenResponse{}, callErr
	}}

	_, err := (&ctxOidcTokenSource{}).IdentityToken(withOidcBrokerClient(context.Background(), c), "sts.amazonaws.com")

	require.ErrorIs(t, err, callErr)
	assert.Contains(t, err.Error(), "AWS")
}
