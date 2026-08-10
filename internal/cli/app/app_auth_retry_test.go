// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubAuthProvider is a test double for authHeaderProvider. It returns
// initialResp on an unforced GetAuthHeader call and forcedResp on a forced
// one, and counts each kind separately so tests can assert on exactly how
// many times a forced refresh was requested.
type stubAuthProvider struct {
	initialResp *pkgauth.GetAuthHeaderResponse
	forcedResp  *pkgauth.GetAuthHeaderResponse
	err         error

	initialCalls int
	forcedCalls  int
}

func (s *stubAuthProvider) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	if forceRefresh {
		s.forcedCalls++
		return s.forcedResp, s.err
	}
	s.initialCalls++
	return s.initialResp, s.err
}

// stubOp is a test double for the op closure withAuthRetry drives. Each call
// consumes the next entry in behaviors (the last entry repeats once
// exhausted), and calls is the count of invocations — standing in for "HTTP
// requests issued" in tests that must prove a retry did or did not happen.
type stubOp struct {
	behaviors []error
	calls     int
}

func (s *stubOp) run(http.Header, *http.Client) error {
	i := s.calls
	if i >= len(s.behaviors) {
		i = len(s.behaviors) - 1
	}
	s.calls++
	return s.behaviors[i]
}

func headerResp(value string) *pkgauth.GetAuthHeaderResponse {
	return &pkgauth.GetAuthHeaderResponse{Headers: map[string][]string{"Authorization": {value}}}
}

func appWithAuthPlugin(provider authHeaderProvider) *App {
	return &App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{
				Auth: json.RawMessage(`{"type":"stub"}`),
			},
		},
		authClientFactory: func() (authHeaderProvider, error) { return provider, nil },
	}
}

// (a) op 401s once, forced refresh succeeds, second op succeeds → nil error,
// exactly one forced GetAuthHeader call.
func TestWithAuthRetry_RecoversAfterForcedRefresh(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  headerResp("fresh"),
	}
	a := appWithAuthPlugin(provider)
	op := &stubOp{behaviors: []error{api.AuthenticationError{}, nil}}

	err := a.withAuthRetry(op.run)

	require.NoError(t, err)
	assert.Equal(t, 2, op.calls)
	assert.Equal(t, 1, provider.forcedCalls)
}

// (b) both attempts 401 → AuthorizationDeniedError, whose message tells the
// user to check with their org admin rather than their local config.
func TestWithAuthRetry_BothAttempts401ReturnsAuthorizationDenied(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  headerResp("still-stale"),
	}
	a := appWithAuthPlugin(provider)
	op := &stubOp{behaviors: []error{api.AuthenticationError{}, api.AuthenticationError{}}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	var denied api.AuthorizationDeniedError
	require.True(t, errors.As(err, &denied))
	assert.Contains(t, err.Error(), "check with your org admin")
	assert.Equal(t, 2, op.calls)
	assert.Equal(t, 1, provider.forcedCalls)
}

// (c) a non-auth error returns unchanged with zero forced refreshes.
func TestWithAuthRetry_NonAuthErrorSkipsRetry(t *testing.T) {
	provider := &stubAuthProvider{initialResp: headerResp("stale")}
	a := appWithAuthPlugin(provider)
	boom := errors.New("connection reset by peer")
	op := &stubOp{behaviors: []error{boom}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.Equal(t, boom, err)
	assert.Equal(t, 1, op.calls)
	assert.Equal(t, 0, provider.forcedCalls)
}

// (d) no auth plugin configured → no retry, the original AuthenticationError
// is preserved unchanged.
func TestWithAuthRetry_NoAuthPluginSkipsRetry(t *testing.T) {
	a := &App{
		Config: &pkgmodel.Config{Cli: pkgmodel.CliConfig{}},
		authClientFactory: func() (authHeaderProvider, error) {
			t.Fatal("authClientFactory must not be called when no auth plugin is configured")
			return nil, nil
		},
	}
	op := &stubOp{behaviors: []error{api.AuthenticationError{}}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.Equal(t, api.AuthenticationError{}, err)
	assert.Equal(t, 1, op.calls)
}

// (e) GetAuthHeader reports a typed failure (not_logged_in) on the initial
// call: zero HTTP requests are issued and the error is the mapper's copy,
// not a request sent unauthenticated with resp.Headers used blindly.
func TestWithAuthRetry_TypedGetAuthHeaderFailureSkipsRequestEntirely(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: &pkgauth.GetAuthHeaderResponse{ErrorCode: pkgauth.ErrorCodeNotLoggedIn},
	}
	a := appWithAuthPlugin(provider)
	op := &stubOp{behaviors: []error{nil}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.Equal(t, "not signed in — run 'formae login'", err.Error())
	assert.Equal(t, 0, op.calls)
	assert.Equal(t, 0, provider.forcedCalls)
}

// agentStatsJSONFor returns a minimal JSON stats body reporting the given
// version, for the httptest integration test below.
func agentStatsJSONFor(t *testing.T, version string) []byte {
	t.Helper()
	b, err := json.Marshal(apimodel.Stats{Version: version, AgentID: "test-agent"})
	require.NoError(t, err)
	return b
}

// TestWithAuthRetry_HTTPIntegration drives withAuthRetry through the real
// API client against an httptest server that 401s the first request and
// 200s the second, proving the retry happens at the individual-request
// boundary (exactly two server hits) rather than replaying a whole command.
func TestWithAuthRetry_HTTPIntegration(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentStatsJSONFor(t, "test-version"))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  headerResp("fresh"),
	}
	a := newAppWithServer(ts.URL)
	a.Config.Cli.Auth = json.RawMessage(`{"type":"stub"}`)
	a.authClientFactory = func() (authHeaderProvider, error) { return provider, nil }

	var stats *apimodel.Stats
	err := a.withAuthRetry(func(h http.Header, net *http.Client) error {
		client := a.apiClient(h, net)
		s, err := client.Stats()
		if err != nil {
			return err
		}
		stats = s
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, "test-version", stats.Version)
	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
	assert.Equal(t, 1, provider.forcedCalls)
}
