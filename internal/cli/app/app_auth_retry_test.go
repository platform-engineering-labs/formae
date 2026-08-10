// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package app

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/network"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubAuthProvider is a test double for authHeaderProvider. It returns
// initialResp/initialErr on an unforced GetAuthHeader call and
// forcedResp/forcedErr on a forced one, and counts each kind separately so
// tests can assert on exactly how many times a forced refresh was requested.
type stubAuthProvider struct {
	initialResp *pkgauth.GetAuthHeaderResponse
	initialErr  error
	forcedResp  *pkgauth.GetAuthHeaderResponse
	forcedErr   error

	initialCalls int
	forcedCalls  int
}

func (s *stubAuthProvider) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	if forceRefresh {
		s.forcedCalls++
		return s.forcedResp, s.forcedErr
	}
	s.initialCalls++
	return s.initialResp, s.initialErr
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

// TestWithAuthRetry_ForcedTypedFailureSkipsRetry verifies that a typed
// failure on the FORCED GetAuthHeader call (op already 401'd once) gets the
// same treatment as one on the initial call: the operation is not attempted
// a second time, and the error is the mapper's copy for that code.
func TestWithAuthRetry_ForcedTypedFailureSkipsRetry(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  &pkgauth.GetAuthHeaderResponse{ErrorCode: pkgauth.ErrorCodeIssuerUnreachable},
	}
	a := appWithAuthPlugin(provider)
	op := &stubOp{behaviors: []error{api.AuthenticationError{}, nil}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.Equal(t, "the identity provider is unreachable — try again shortly", err.Error())
	assert.Equal(t, 1, op.calls, "op must not be retried after a typed forced-refresh failure")
	assert.Equal(t, 1, provider.forcedCalls)
}

// TestWithAuthRetry_ForcedRPCFailureSurfacesRealError verifies that when the
// forced GetAuthHeader call itself fails at the RPC layer (e.g. a crashed
// plugin subprocess), the real failure is returned — distinct from both the
// stale AuthenticationError that triggered the retry and from
// AuthorizationDeniedError, since no credential was ever obtained to be
// denied.
func TestWithAuthRetry_ForcedRPCFailureSurfacesRealError(t *testing.T) {
	boom := errors.New("auth plugin subprocess is not responding")
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedErr:   boom,
	}
	a := appWithAuthPlugin(provider)
	op := &stubOp{behaviors: []error{api.AuthenticationError{}}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.NotEqual(t, api.AuthenticationError{}, err)
	var denied api.AuthorizationDeniedError
	assert.False(t, errors.As(err, &denied))
	assert.Equal(t, 1, op.calls, "op must not be retried when no fresh credential was obtained")
	assert.Equal(t, 1, provider.forcedCalls)
}

// TestWithAuthRetry_ForcedAuthClientFailureSurfacesRealError verifies that
// when the auth plugin client itself cannot be obtained for the forced
// refresh (e.g. the plugin binary is gone), that failure is returned as
// itself rather than masked behind the original AuthenticationError.
func TestWithAuthRetry_ForcedAuthClientFailureSurfacesRealError(t *testing.T) {
	boom := errors.New("auth plugin binary not found")
	calls := 0
	a := &App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{Auth: json.RawMessage(`{"type":"stub"}`)},
		},
		authClientFactory: func() (authHeaderProvider, error) {
			calls++
			if calls == 1 {
				return &stubAuthProvider{initialResp: headerResp("stale")}, nil
			}
			return nil, boom
		},
	}
	op := &stubOp{behaviors: []error{api.AuthenticationError{}}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.Equal(t, boom, err)
	assert.Equal(t, 1, op.calls, "op must not be retried when the auth client itself is unavailable")
}

// countingNetPlugin is a test double for network.NetworkPlugin that counts
// how many times Client is called, so tests can prove the network transport
// is built at most once per App. The counter and a short delay inside Client
// are both there so a concurrent-access test can actually land two callers
// inside construction at once, rather than merely hoping a race exists.
type countingNetPlugin struct {
	name  string
	calls atomic.Int32
}

func (p *countingNetPlugin) Name() string { return p.name }

func (p *countingNetPlugin) Client(json.RawMessage) (*http.Client, error) {
	p.calls.Add(1)
	time.Sleep(time.Millisecond)
	return &http.Client{}, nil
}

func (p *countingNetPlugin) Listen(json.RawMessage, int) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

// TestNetHTTPClient_MemoizedAcrossWithAuthRetryClosures verifies that the
// network plugin transport is built at most once per App and shared by
// every withAuthRetry closure, rather than rebuilt on each one — the auth
// header is meant to be re-fetched per operation (and force-refreshed on
// retry), but the transport underneath it is not.
func TestNetHTTPClient_MemoizedAcrossWithAuthRetryClosures(t *testing.T) {
	plugin := &countingNetPlugin{name: "counting-stub-net-" + t.Name()}
	network.DefaultRegistry.Register(plugin)

	a := &App{
		Config: &pkgmodel.Config{
			Network: &pkgmodel.NetworkConfig{Type: plugin.name},
		},
	}

	// Two independent withAuthRetry calls stand in for two conversion-point
	// closures within a single command (e.g. the Stats preflight and the
	// command's own operation), each of which fetches its own auth
	// header/net pair via getAuthAndNetHandlers.
	op := &stubOp{behaviors: []error{nil}}
	require.NoError(t, a.withAuthRetry(op.run))
	require.NoError(t, a.withAuthRetry(op.run))

	assert.Equal(t, int32(1), plugin.calls.Load(), "the network transport must be built once per App, not once per withAuthRetry closure")
}

// TestNetHTTPClient_ConcurrentCallersBuildTransportOnce drives netHTTPClient
// from many goroutines released at once — modeling two Bubbletea-driven
// status fetches racing on the same App, which happens whenever a fetch
// hasn't returned before the next tick's command batch fires — and asserts
// the network plugin's Client is invoked exactly once, with every goroutine
// observing the identical *http.Client instance. Run under -race, this fails
// on an unsynchronized memoization even when the assertions below would
// otherwise pass, since the race detector flags the unsynchronized
// check-then-act itself.
func TestNetHTTPClient_ConcurrentCallersBuildTransportOnce(t *testing.T) {
	plugin := &countingNetPlugin{name: "concurrent-stub-net-" + t.Name()}
	network.DefaultRegistry.Register(plugin)

	a := &App{
		Config: &pkgmodel.Config{
			Network: &pkgmodel.NetworkConfig{Type: plugin.name},
		},
	}

	const goroutines = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*http.Client, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			c, err := a.netHTTPClient()
			results[i] = c
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		require.NoError(t, errs[i])
		require.NotNil(t, results[i])
		assert.Same(t, results[0], results[i], "every concurrent caller must observe the same memoized transport")
	}
	assert.Equal(t, int32(1), plugin.calls.Load(), "the network transport must be built exactly once under concurrent access")
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
