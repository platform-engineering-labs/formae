// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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

func decodeErrorCode(t *testing.T, data []byte) string {
	t.Helper()
	var resp IdentityTokenResponse
	require.NoError(t, Decode(data, &resp))
	return resp.ErrorCode
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
	validReq, err := Encode(OidcIdentityTokenRequest{Audience: "aws"})
	require.NoError(t, err)

	tests := []struct {
		name         string
		request      []byte
		plugin       OidcCredentialPlugin
		expectResult bool
		expectCode   string
	}{
		{
			name:       "decode failure maps to internal",
			request:    []byte("not a valid encoded request"),
			plugin:     &fakeIdentityPlugin{},
			expectCode: ErrCodeInternal,
		},
		{
			name:       "ErrInvalidAudience maps to invalid_audience",
			request:    validReq,
			plugin:     &fakeIdentityPlugin{err: ErrInvalidAudience},
			expectCode: ErrCodeInvalidAudience,
		},
		{
			name:       "wrapped ErrInvalidAudience still maps via errors.Is",
			request:    validReq,
			plugin:     &fakeIdentityPlugin{err: fmt.Errorf("upstream: %w", ErrInvalidAudience)},
			expectCode: ErrCodeInvalidAudience,
		},
		{
			name:       "other plugin error maps to mint_failed",
			request:    validReq,
			plugin:     &fakeIdentityPlugin{err: errors.New("upstream STS call failed")},
			expectCode: ErrCodeMintFailed,
		},
		{
			name:       "nil result and nil error maps to internal",
			request:    validReq,
			plugin:     &fakeIdentityPlugin{result: nil, err: nil},
			expectCode: ErrCodeInternal,
		},
		{
			name:         "success carries the result through, unmapped",
			request:      validReq,
			plugin:       &fakeIdentityPlugin{result: &OidcIdentityTokenResult{Token: "tok-123"}},
			expectResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := handle(context.Background(), tt.plugin, tt.request, time.Second, nil)
			require.NotNil(t, out)

			if tt.expectResult {
				result, err := DecodeResponse(out)
				require.NoError(t, err)
				assert.Equal(t, "tok-123", result.Token)
				return
			}

			assert.Equal(t, tt.expectCode, decodeErrorCode(t, out))
		})
	}
}

func TestHandle_TimesOutAndMapsToInternal(t *testing.T) {
	req, err := Encode(OidcIdentityTokenRequest{Audience: "aws"})
	require.NoError(t, err)

	plugin := &fakeIdentityPlugin{
		unblock: make(chan struct{}), // never closed: IdentityToken blocks forever
		result:  &OidcIdentityTokenResult{Token: "too-late"},
	}

	start := time.Now()
	out := handle(context.Background(), plugin, req, 20*time.Millisecond, nil)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, time.Second, "handle must return once the timeout elapses, not wait for the plugin")
	assert.Equal(t, ErrCodeInternal, decodeErrorCode(t, out))
}

func TestHandle_LogsMintFailureWithoutLeakingOntoTheWire(t *testing.T) {
	req, err := Encode(OidcIdentityTokenRequest{Audience: "aws", RequestID: "req-42"})
	require.NoError(t, err)

	plugin := &fakeIdentityPlugin{err: errors.New("upstream STS call failed: token-shaped-secret-abc123")}
	log := &fakeErrorLogger{}

	out := handle(context.Background(), plugin, req, time.Second, log)

	require.Len(t, log.calls, 1)
	assert.Contains(t, log.calls[0], "req-42")
	assert.Contains(t, log.calls[0], "aws")
	assert.Contains(t, log.calls[0], ErrCodeMintFailed)
	assert.Contains(t, log.calls[0], "upstream STS call failed: token-shaped-secret-abc123")

	var resp IdentityTokenResponse
	require.NoError(t, Decode(out, &resp))
	assert.Equal(t, ErrCodeMintFailed, resp.ErrorCode)
	assert.Empty(t, resp.ErrorMessage, "plugin error text must never reach the wire envelope")
	assert.NotContains(t, string(out), "token-shaped-secret-abc123", "plugin error text must never be encoded onto the wire")
}
