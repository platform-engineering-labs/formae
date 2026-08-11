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
// forcedResp/forcedErr on a forced one. Call counts use atomics and
// forcedDelay is honored before returning, so the same stub is safe to
// drive concurrently from a test that overlaps forced refreshes.
type stubAuthProvider struct {
	initialResp *pkgauth.GetAuthHeaderResponse
	initialErr  error
	forcedResp  *pkgauth.GetAuthHeaderResponse
	forcedErr   error
	forcedDelay time.Duration

	initialCalls atomic.Int32
	forcedCalls  atomic.Int32
}

func (s *stubAuthProvider) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	if forceRefresh {
		s.forcedCalls.Add(1)
		if s.forcedDelay > 0 {
			time.Sleep(s.forcedDelay)
		}
		return s.forcedResp, s.forcedErr
	}
	s.initialCalls.Add(1)
	return s.initialResp, s.initialErr
}

// stubOp is a test double for the op closure withAuthRetry drives. Each call
// consumes the next entry in behaviors (the last entry repeats once
// exhausted), and calls is the count of invocations — standing in for "HTTP
// requests issued" in tests that must prove a retry did or did not happen.
// Use this when the test's pass/fail condition doesn't depend on which
// credential op actually received; use credentialOp when it must.
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

// credentialOp is a test double that only succeeds when given exactly the
// accepted Authorization value, failing with AuthenticationError for
// anything else — modeling an agent that actually checks the credential
// rather than merely counting requests. It exists so a test asserting
// success also proves the REFRESHED header, not the original one, is what
// reached the operation: a stubOp with canned pass/fail behaviors would
// stay green even if withAuthRetry retried with the stale header.
type credentialOp struct {
	accept string
	calls  []string
}

func (c *credentialOp) run(h http.Header, _ *http.Client) error {
	got := h.Get("Authorization")
	c.calls = append(c.calls, got)
	if got != c.accept {
		return api.AuthenticationError{}
	}
	return nil
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
// exactly one forced GetAuthHeader call, and the operation actually receives
// the REFRESHED credential (not the stale one it was first given).
func TestWithAuthRetry_RecoversAfterForcedRefresh(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  headerResp("fresh"),
	}
	a := appWithAuthPlugin(provider)
	op := &credentialOp{accept: "fresh"}

	err := a.withAuthRetry(op.run)

	require.NoError(t, err)
	assert.Equal(t, []string{"stale", "fresh"}, op.calls)
	assert.Equal(t, int32(1), provider.forcedCalls.Load())
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
	assert.Equal(t, int32(1), provider.forcedCalls.Load())
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
	assert.Equal(t, int32(0), provider.forcedCalls.Load())
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
	assert.Equal(t, int32(0), provider.forcedCalls.Load())
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
	assert.Equal(t, int32(1), provider.forcedCalls.Load())
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
	assert.Equal(t, int32(1), provider.forcedCalls.Load())
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

// TestWithAuthRetry_NoCredentialOnInitialFetchFailsClosed verifies that a
// GetAuthHeader response with no ErrorCode and no Error, but no usable
// credential in Headers either, is NOT treated as success on the initial
// fetch: no HTTP request is issued, and the error names the real cause
// instead of silently attaching an empty Authorization header. This
// includes a credential returned under any key other than the canonical
// "Authorization" one — internal/api.NewClient only ever transmits that
// key, so a value under any other name (or spelling) is one the CLI can
// never actually send.
func TestWithAuthRetry_NoCredentialOnInitialFetchFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string][]string
	}{
		{name: "nil headers", headers: nil},
		{name: "empty headers map", headers: map[string][]string{}},
		{name: "map with only an empty value", headers: map[string][]string{"Authorization": {""}}},
		{name: "credential under a different header entirely", headers: map[string][]string{"X-Api-Key": {"secret-key"}}},
		{name: "credential under a non-canonical lowercase key", headers: map[string][]string{"authorization": {"secret-key"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &stubAuthProvider{
				initialResp: &pkgauth.GetAuthHeaderResponse{Headers: tt.headers},
			}
			a := appWithAuthPlugin(provider)
			op := &stubOp{behaviors: []error{nil}}

			err := a.withAuthRetry(op.run)

			require.Error(t, err)
			assert.Equal(t, "the auth plugin returned no credential", err.Error())
			assert.Equal(t, 0, op.calls, "no request may be sent with an unusable credential")
		})
	}
}

// TestHasCredential pins hasCredential's exact semantics directly: it must
// test the credential value the CLI will actually transmit — the canonical
// "Authorization" header as http.Header.Get would resolve it — rather than
// crediting any non-empty value found under any key. http.Header.Get
// canonicalises the key it looks up, not the keys already stored in the
// map, so a plain-map "authorization" (lowercase) entry is NOT the same as
// a canonical "Authorization" one; this test asserts that real behavior
// rather than an assumption about it.
func TestHasCredential(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string][]string
		want    bool
	}{
		{name: "nil map", headers: nil, want: false},
		{name: "empty map", headers: map[string][]string{}, want: false},
		{name: "canonical Authorization with a value", headers: map[string][]string{"Authorization": {"secret"}}, want: true},
		{name: "canonical Authorization with only an empty value", headers: map[string][]string{"Authorization": {""}}, want: false},
		{name: "credential under a different header entirely", headers: map[string][]string{"X-Api-Key": {"secret"}}, want: false},
		{name: "non-canonical lowercase authorization key", headers: map[string][]string{"authorization": {"secret"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasCredential(tt.headers))
		})
	}
}

// TestWithAuthRetry_NoCredentialOnForcedRefreshFailsClosed verifies the same
// fail-closed behavior on the FORCED refresh: op 401s once, the forced
// GetAuthHeader call reports success but returns no usable credential, and
// the operation must not be retried with that empty header — nor must the
// result be confused with AuthorizationDeniedError, since no credential was
// ever obtained for the agent to deny.
func TestWithAuthRetry_NoCredentialOnForcedRefreshFailsClosed(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  &pkgauth.GetAuthHeaderResponse{Headers: map[string][]string{}},
	}
	a := appWithAuthPlugin(provider)
	op := &stubOp{behaviors: []error{api.AuthenticationError{}, nil}}

	err := a.withAuthRetry(op.run)

	require.Error(t, err)
	assert.Equal(t, "the auth plugin returned no credential", err.Error())
	var denied api.AuthorizationDeniedError
	assert.False(t, errors.As(err, &denied), "an unusable credential must not be reported as a denial")
	assert.Equal(t, 1, op.calls, "the second attempt must not be sent with an unusable credential")
}

// TestWithAuthRetry_ConcurrentForcedRefreshesCoalesce drives several
// overlapping 401s through withAuthRetry on the same App at once — the
// shape Bubbletea's overlapping status polls can produce — and asserts that
// only one forced GetAuthHeader(true) call reaches the plugin, with every
// caller still recovering successfully using the shared refreshed
// credential. Concurrent independent refreshes would be actively harmful
// against a plugin with rotating refresh tokens: one refresh would consume
// the token another caller's refresh is mid-use of.
func TestWithAuthRetry_ConcurrentForcedRefreshesCoalesce(t *testing.T) {
	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  headerResp("fresh"),
		forcedDelay: 20 * time.Millisecond,
	}
	a := appWithAuthPlugin(provider)

	const callers = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, callers)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			op := &credentialOp{accept: "fresh"}
			errs[i] = a.withAuthRetry(op.run)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "caller %d must recover using the shared refreshed credential", i)
	}
	assert.Equal(t, int32(1), provider.forcedCalls.Load(), "concurrent callers must coalesce into a single forced refresh")
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
// API client against an httptest server that authorizes purely on the
// Authorization header value it receives — rejecting the stale credential
// and accepting only the refreshed one — proving the retry happens at the
// individual-request boundary with the genuinely refreshed credential
// reaching the server, not merely that a second request was sent.
func TestWithAuthRetry_HTTPIntegration(t *testing.T) {
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "fresh" {
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
	assert.Equal(t, int32(1), provider.forcedCalls.Load())
}

// TestRunBeforeCommand_StylesAuthorizationDeniedError verifies that when the
// Stats preflight exhausts its forced-refresh retry and withAuthRetry
// returns AuthorizationDeniedError, runBeforeCommand renders it at the call
// site like its sibling branches in the same switch (ECONNREFUSED,
// AuthenticationError, version mismatch), rather than passing it through as
// flat text. The frozen error text itself must still be present verbatim —
// only the presentation changes.
func TestRunBeforeCommand_StylesAuthorizationDeniedError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	provider := &stubAuthProvider{
		initialResp: headerResp("stale"),
		forcedResp:  headerResp("still-stale"),
	}
	a := newAppWithServer(ts.URL)
	a.Config.Cli.Auth = json.RawMessage(`{"type":"stub"}`)
	a.authClientFactory = func() (authHeaderProvider, error) { return provider, nil }

	compatible, _, _, err := a.runBeforeCommand(true)

	assert.False(t, compatible)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the agent denied access for this installation; check with your org admin")
}
