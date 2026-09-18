// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/require"
)

type boundedIdentityPlugin struct {
	calls  atomic.Int32
	invoke func(context.Context) (*OidcIdentityTokenResult, error)
}

func (p *boundedIdentityPlugin) IdentityToken(ctx context.Context, _ *OidcIdentityTokenRequest) (*OidcIdentityTokenResult, error) {
	p.calls.Add(1)
	return p.invoke(ctx)
}

func TestBoundedRequestRejectsMissingOrExpiredDeadline(t *testing.T) {
	for _, deadline := range []uint64{0, uint64(time.Now().Unix() - 1), uint64(time.Now().Unix()), ^uint64(0)} {
		p := &boundedIdentityPlugin{invoke: func(context.Context) (*OidcIdentityTokenResult, error) {
			return &OidcIdentityTokenResult{Token: "must-not-mint"}, nil
		}}
		a := &CredentialActor{plugin: p}
		answer, err := a.HandleCall(gen.PID{}, gen.Ref{ID: [3]uint64{0, 0, deadline}}, OidcBoundedIdentityTokenRequest{Request: OidcIdentityTokenRequest{Audience: "test"}})
		require.NoError(t, err)
		require.Equal(t, ErrCodeInternal, answer.(IdentityTokenResponse).ErrorCode)
		require.Zero(t, p.calls.Load())
	}
}

func TestBoundedRequestUsesEnclosingDeadlineAndResponseMargin(t *testing.T) {
	deadline := time.Now().Add(2 * time.Second).Truncate(time.Second)
	observed := make(chan time.Time, 1)
	stopped := make(chan struct{})
	p := &boundedIdentityPlugin{invoke: func(ctx context.Context) (*OidcIdentityTokenResult, error) {
		d, ok := ctx.Deadline()
		if !ok {
			observed <- time.Time{}
		} else {
			observed <- d
		}
		<-ctx.Done()
		close(stopped)
		return &OidcIdentityTokenResult{Token: "late"}, nil
	}}
	a := &CredentialActor{plugin: p}
	answer, err := a.HandleCall(gen.PID{}, gen.Ref{ID: [3]uint64{0, 0, uint64(deadline.Unix())}}, OidcBoundedIdentityTokenRequest{})
	require.NoError(t, err)
	require.Nil(t, answer.(IdentityTokenResponse).Result)
	require.Equal(t, deadline.Add(-250*time.Millisecond), <-observed)
	select {
	case <-stopped:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cooperative plugin did not stop")
	}
	require.True(t, time.Now().Before(deadline), "plugin must stop within caller lifetime margin")
}

func TestBoundedRequestCapsLongDeadlineAndReturnsSuccess(t *testing.T) {
	p := &boundedIdentityPlugin{invoke: func(ctx context.Context) (*OidcIdentityTokenResult, error) {
		d, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(d), 10*time.Second)
		return &OidcIdentityTokenResult{Token: "bounded"}, nil
	}}
	a := &CredentialActor{plugin: p}
	answer, err := a.HandleCall(gen.PID{}, gen.Ref{ID: [3]uint64{0, 0, uint64(time.Now().Add(time.Hour).Unix())}}, OidcBoundedIdentityTokenRequest{})
	require.NoError(t, err)
	require.Equal(t, "bounded", answer.(IdentityTokenResponse).Result.Token)
}

func TestBoundedRequestWaitsForCooperativeMethodToUnwind(t *testing.T) {
	canceled, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	p := &boundedIdentityPlugin{invoke: func(ctx context.Context) (*OidcIdentityTokenResult, error) {
		<-ctx.Done()
		close(canceled)
		<-release
		close(returned)
		return &OidcIdentityTokenResult{Token: "canceled-result"}, nil
	}}
	a := &CredentialActor{plugin: p}
	answer := make(chan IdentityTokenResponse, 1)
	go func() {
		value, _ := a.HandleCall(gen.PID{}, gen.Ref{ID: [3]uint64{0, 0, uint64(time.Now().Add(2 * time.Second).Unix())}}, OidcBoundedIdentityTokenRequest{})
		answer <- value.(IdentityTokenResponse)
	}()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("cooperative method did not observe deadline")
	}
	select {
	case <-answer:
		t.Fatal("broker replied while its canceled credential method was still unwinding")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case response := <-answer:
		require.Nil(t, response.Result, "canceled method's token must be suppressed")
		require.Equal(t, ErrCodeInternal, response.ErrorCode)
		select {
		case <-returned:
		default:
			t.Fatal("reply preceded method return")
		}
	case <-time.After(time.Second):
		t.Fatal("broker did not reply after method returned")
	}
}
