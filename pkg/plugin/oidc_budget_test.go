// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/stretchr/testify/require"
)

func TestOidcBrokerReservesFullSynchronousCallBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		budget   time.Duration
		wantCall bool
	}{{"short", 9 * time.Second, false}, {"sufficient", 11 * time.Second, true}, {"no deadline", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cancel := func() {}
			if tc.budget > 0 {
				ctx, cancel = context.WithTimeout(ctx, tc.budget)
			}
			defer cancel()
			called := false
			c := &oidcBrokerClient{call: func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
				called = true
				return credential.IdentityTokenResponse{}, nil
			}}
			_, err := c.invoke(ctx, credential.OidcIdentityTokenRequest{})
			require.Equal(t, tc.wantCall, called)
			if tc.wantCall {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
}
func TestOidcBrokerBudgetIncludesSerializationWait(t *testing.T) {
	called := false
	c := &oidcBrokerClient{call: func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		called = true
		return credential.IdentityTokenResponse{}, nil
	}}
	c.gateOnce.Do(func() { c.gate = make(chan struct{}, 1) })
	c.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second+10*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.invoke(ctx, credential.OidcIdentityTokenRequest{}); result <- err }()
	time.Sleep(30 * time.Millisecond)
	<-c.gate
	require.ErrorIs(t, <-result, context.DeadlineExceeded)
	require.False(t, called)
}
func TestOidcBrokerRejectsCanceledCallResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &oidcBrokerClient{call: func(credential.OidcIdentityTokenRequest) (credential.IdentityTokenResponse, error) {
		cancel()
		return credential.IdentityTokenResponse{Result: &credential.OidcIdentityTokenResult{Token: "never-return"}}, nil
	}}
	response, err := c.invoke(ctx, credential.OidcIdentityTokenRequest{})
	require.True(t, errors.Is(err, context.Canceled))
	require.Nil(t, response.Result)
}
