// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIdentityPlugin answers IdentityToken with a canned result/error, or
// blocks until unblock is closed when it's non-nil - used to exercise
// callWithTimeout's deadline.
type fakeIdentityPlugin struct {
	result  *OidcIdentityTokenResult
	err     error
	unblock chan struct{}
}

func (p *fakeIdentityPlugin) IdentityToken(ctx context.Context, req *OidcIdentityTokenRequest) (*OidcIdentityTokenResult, error) {
	if p.unblock != nil {
		<-p.unblock
	}
	return p.result, p.err
}

// fakeErrorLogger is the seam handle needs to be testable without a running
// Ergo node: it satisfies errorLogger without implementing all of gen.Log.
type fakeErrorLogger struct {
	calls []string
}

func (f *fakeErrorLogger) Error(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func TestHandle_ErrorMapping(t *testing.T) {
	req := OidcIdentityTokenRequest{Audience: "aws"}

	tests := []struct {
		name         string
		plugin       OidcCredentialPlugin
		expectResult bool
		expectCode   string
	}{
		{
			name:       "ErrInvalidAudience maps to invalid_audience",
			plugin:     &fakeIdentityPlugin{err: ErrInvalidAudience},
			expectCode: ErrCodeInvalidAudience,
		},
		{
			name:       "wrapped ErrInvalidAudience still maps via errors.Is",
			plugin:     &fakeIdentityPlugin{err: fmt.Errorf("upstream: %w", ErrInvalidAudience)},
			expectCode: ErrCodeInvalidAudience,
		},
		{
			name:       "other plugin error maps to mint_failed",
			plugin:     &fakeIdentityPlugin{err: errors.New("upstream STS call failed")},
			expectCode: ErrCodeMintFailed,
		},
		{
			name:       "nil result and nil error maps to internal",
			plugin:     &fakeIdentityPlugin{result: nil, err: nil},
			expectCode: ErrCodeInternal,
		},
		{
			name:         "success carries the result through, unmapped",
			plugin:       &fakeIdentityPlugin{result: &OidcIdentityTokenResult{Token: "tok-123"}},
			expectResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := handle(context.Background(), tt.plugin, req, time.Second, nil)

			if tt.expectResult {
				result, err := ResponseError(resp)
				require.NoError(t, err)
				assert.Equal(t, "tok-123", result.Token)
				return
			}

			assert.Equal(t, tt.expectCode, resp.ErrorCode)
		})
	}
}

func TestHandle_TimesOutAndMapsToInternal(t *testing.T) {
	req := OidcIdentityTokenRequest{Audience: "aws"}

	plugin := &fakeIdentityPlugin{
		unblock: make(chan struct{}), // never closed: IdentityToken blocks forever
		result:  &OidcIdentityTokenResult{Token: "too-late"},
	}

	start := time.Now()
	resp := handle(context.Background(), plugin, req, 20*time.Millisecond, nil)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, time.Second, "handle must return once the timeout elapses, not wait for the plugin")
	assert.Equal(t, ErrCodeInternal, resp.ErrorCode)
}

func TestHandle_LogsMintFailureWithoutLeakingOntoTheWire(t *testing.T) {
	req := OidcIdentityTokenRequest{Audience: "aws", RequestID: "req-42"}

	plugin := &fakeIdentityPlugin{err: errors.New("upstream STS call failed: token-shaped-secret-abc123")}
	log := &fakeErrorLogger{}

	resp := handle(context.Background(), plugin, req, time.Second, log)

	require.Len(t, log.calls, 1)
	assert.Contains(t, log.calls[0], "req-42")
	assert.Contains(t, log.calls[0], "aws")
	assert.Contains(t, log.calls[0], ErrCodeMintFailed)
	assert.Contains(t, log.calls[0], "upstream STS call failed: token-shaped-secret-abc123")

	assert.Equal(t, ErrCodeMintFailed, resp.ErrorCode)
	assert.Empty(t, resp.ErrorMessage, "plugin error text must never reach the wire envelope")

	var buf bytes.Buffer
	require.NoError(t, resp.MarshalEDF(&buf))
	assert.NotContains(t, buf.String(), "token-shaped-secret-abc123", "plugin error text must never be encoded onto the wire")
}

// HandleCall answers the registered request type with a typed envelope and
// refuses anything else, so an unexpected message is a failed call rather
// than a silently empty response.
func TestHandleCall_TypeSwitch(t *testing.T) {
	actor := &CredentialActor{plugin: &fakeIdentityPlugin{result: &OidcIdentityTokenResult{Token: "tok-123"}}}

	answer, err := actor.HandleCall(gen.PID{}, gen.Ref{}, OidcIdentityTokenRequest{Audience: "aws"})

	require.NoError(t, err)
	resp, ok := answer.(IdentityTokenResponse)
	require.True(t, ok, "HandleCall must answer with a typed IdentityTokenResponse, got %T", answer)
	require.NotNil(t, resp.Result)
	assert.Equal(t, "tok-123", resp.Result.Token)
}

func TestHandleCall_UnknownRequestTypeErrors(t *testing.T) {
	actor := &CredentialActor{plugin: &fakeIdentityPlugin{}}

	answer, err := actor.HandleCall(gen.PID{}, gen.Ref{}, "not a request")

	assert.Nil(t, answer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "string", "the error must name the unexpected type")
}
