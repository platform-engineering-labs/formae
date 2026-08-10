// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"sort"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// testMiddlewarePlugin is a configurable mock for middleware tests.
type testMiddlewarePlugin struct {
	pkgauth.UnimplementedAuthPlugin
	validResponse bool
	subject       string
	subjectName   string
	callCount     int

	// lastHeaders captures exactly what the most recent Validate call
	// received, so tests can assert on the plugin's actual observed input.
	lastHeaders map[string][]string

	// leakOnHeader, when non-empty, makes Validate respond with leakSubject
	// whenever req.Headers contains this header — used to prove a header
	// never reaches the plugin by showing what the plugin would report if
	// it did.
	leakOnHeader string
	leakSubject  string
}

func (p *testMiddlewarePlugin) Init(req *pkgauth.InitRequest, resp *pkgauth.InitResponse) error {
	return nil
}

func (p *testMiddlewarePlugin) Validate(req *pkgauth.ValidateRequest, resp *pkgauth.ValidateResponse) error {
	p.callCount++
	p.lastHeaders = req.Headers

	if p.leakOnHeader != "" {
		if _, leaked := req.Headers[p.leakOnHeader]; leaked {
			resp.Valid = true
			resp.Subject = p.leakSubject
			resp.CacheTTL = time.Minute
			return nil
		}
	}

	resp.Valid = p.validResponse
	resp.Subject = p.subject
	resp.SubjectName = p.subjectName
	resp.CacheTTL = time.Minute
	return nil
}

func (p *testMiddlewarePlugin) GetAuthHeader(req *pkgauth.GetAuthHeaderRequest, resp *pkgauth.GetAuthHeaderResponse) error {
	return nil
}

// setupMiddleware creates a connected AuthPluginHandle with a mock plugin and returns
// the Echo instance, handle, cache, and mock plugin for assertions.
func setupMiddleware(t *testing.T, validResponse bool) (*echo.Echo, *AuthPluginHandle, *AuthCache, *testMiddlewarePlugin) {
	t.Helper()

	mock := &testMiddlewarePlugin{validResponse: validResponse}

	clientConn, serverConn := net.Pipe()
	srv := rpc.NewServer()
	_ = srv.RegisterName("AuthPlugin", mock)
	go srv.ServeConn(serverConn)
	client := rpc.NewClient(clientConn)
	t.Cleanup(func() { _ = client.Close() })

	handle := NewAuthPluginHandle("basic", "/path", nil)
	handle.Connect(dummyConn(), client)

	cache := NewAuthCache()

	e := echo.New()
	e.Use(NewAuthMiddleware(handle, cache))
	e.GET("/test", func(c echo.Context) error {
		subject, _ := c.Get(ContextKeySubject).(string)
		subjectName, _ := c.Get(ContextKeySubjectName).(string)
		// The response body carries the subject (existing assertions key off
		// it) and this header carries the subject name, so tests can assert
		// on both context values independently.
		c.Response().Header().Set("X-Test-Subject-Name", subjectName)
		return c.String(http.StatusOK, subject)
	})

	return e, handle, cache, mock
}

func TestMiddleware_ValidCredentials(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, true)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0") // test:test
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mock.callCount != 1 {
		t.Fatalf("expected 1 Validate call, got %d", mock.callCount)
	}
}

func TestMiddleware_InvalidCredentials(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, false)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic YmFkOmJhZA==") // bad:bad
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if mock.callCount != 1 {
		t.Fatalf("expected 1 Validate call, got %d", mock.callCount)
	}
}

func TestMiddleware_MissingAuthHeader(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, false)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	// Plugin is still called — it decides what "missing header" means
	if mock.callCount != 1 {
		t.Fatalf("expected 1 Validate call, got %d", mock.callCount)
	}
}

func TestMiddleware_CacheHit(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, true)

	// First request: cache miss → calls Validate
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}
	if mock.callCount != 1 {
		t.Fatalf("first request: expected 1 call, got %d", mock.callCount)
	}

	// Second request with same credentials: cache hit → no Validate call
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("second request: expected 200, got %d", rec2.Code)
	}
	if mock.callCount != 1 {
		t.Fatalf("second request: expected still 1 call (cache hit), got %d", mock.callCount)
	}
}

func TestMiddleware_FreshResponseSetsSubjectOnContext(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, true)
	mock.subject = "s"
	mock.subjectName = "Sam Sample"

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "s" {
		t.Fatalf("expected subject %q on context, got %q", "s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Test-Subject-Name"); got != "Sam Sample" {
		t.Fatalf("expected subject name %q on context, got %q", "Sam Sample", got)
	}
}

func TestMiddleware_CacheHitPopulatesSubjectOnContext(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, true)
	mock.subject = "s"
	mock.subjectName = "Sam Sample"

	// First request: cache miss → calls Validate, subject comes from the fresh response.
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}
	if rec1.Body.String() != "s" {
		t.Fatalf("first request: expected subject %q, got %q", "s", rec1.Body.String())
	}
	if got := rec1.Header().Get("X-Test-Subject-Name"); got != "Sam Sample" {
		t.Fatalf("first request: expected subject name %q, got %q", "Sam Sample", got)
	}

	// Second request with the same credentials: cache hit, but the subject and
	// subject name must still land on the context, reconstructed from the
	// cache entry, and the plugin must not be called again.
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("second request: expected 200, got %d", rec2.Code)
	}
	if rec2.Body.String() != "s" {
		t.Fatalf("second request (cache hit): expected subject %q on context, got %q", "s", rec2.Body.String())
	}
	if got := rec2.Header().Get("X-Test-Subject-Name"); got != "Sam Sample" {
		t.Fatalf("second request (cache hit): expected subject name %q on context, got %q", "Sam Sample", got)
	}
	if mock.callCount != 1 {
		t.Fatalf("expected 1 Validate call (second request served from cache), got %d", mock.callCount)
	}
}

func TestComputeCacheKey_Injective(t *testing.T) {
	oneValue := map[string][]string{"Authorization": {"Bearer secretAuthorizationXYZ"}}
	twoValues := map[string][]string{"Authorization": {"Bearer secret", "XYZ"}}

	if computeCacheKey(oneValue) == computeCacheKey(twoValues) {
		t.Fatal("expected different cache keys for header sets whose values concatenate to the same bytes")
	}

	// Two headers with one value each vs. one header with three values: build
	// both through http.Header, the same canonicalizing constructor the
	// middleware uses when it reads c.Request().Header, so the header keys
	// here match what computeCacheKey actually receives on the request path.
	twoHeaders := http.Header{}
	twoHeaders.Set("A", "X")
	twoHeaders.Set("Ab", "Y")

	oneHeaderThreeValues := http.Header{}
	oneHeaderThreeValues.Set("A", "X")
	oneHeaderThreeValues.Add("A", "Ab")
	oneHeaderThreeValues.Add("A", "Y")

	if computeCacheKey(map[string][]string(twoHeaders)) == computeCacheKey(map[string][]string(oneHeaderThreeValues)) {
		t.Fatal("expected different cache keys when the same bytes are distributed across a different number of headers and values")
	}

	// The same headers, assigned into two maps in opposite order, must hash
	// to the same key: the key must not depend on map iteration order. Go
	// randomizes iteration order per range, independently for each map, so
	// the comparison is repeated many times to make an order-dependent
	// implementation overwhelmingly likely to disagree at least once.
	ascending := map[string][]string{}
	ascending["Accept-Charset"] = []string{"utf-8"}
	ascending["Authorization"] = []string{"Bearer token"}
	ascending["X-Custom-Auth"] = []string{"one", "two"}
	ascending["X-Signature"] = []string{"sig"}

	descending := map[string][]string{}
	descending["X-Signature"] = []string{"sig"}
	descending["X-Custom-Auth"] = []string{"one", "two"}
	descending["Authorization"] = []string{"Bearer token"}
	descending["Accept-Charset"] = []string{"utf-8"}

	for i := 0; i < 50; i++ {
		if computeCacheKey(ascending) != computeCacheKey(descending) {
			t.Fatalf("expected the same cache key regardless of map iteration order (repetition %d)", i)
		}
	}
}

// TestMiddleware_PluginNeverObservesExcludedHeaders covers every header in
// the production nonAuthHeaders exclusion set (read from the package
// variable itself, not restated here) and, for each one, proves the plugin
// never sees it: a request carrying that header, with a plugin mock that
// would report a different subject if it could see the header, still gets
// the configured subject, and the header is absent from what the plugin
// actually received.
func TestMiddleware_PluginNeverObservesExcludedHeaders(t *testing.T) {
	headers := make([]string, 0, len(nonAuthHeaders))
	for header := range nonAuthHeaders {
		headers = append(headers, header)
	}
	sort.Strings(headers)

	for _, header := range headers {
		header := header
		t.Run(header, func(t *testing.T) {
			e, _, _, mock := setupMiddleware(t, true)
			mock.subject = "s"
			canonical := http.CanonicalHeaderKey(header)
			// If the plugin could see this excluded header, it would report
			// this subject instead of the configured one.
			mock.leakOnHeader = canonical
			mock.leakSubject = "leaked"

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
			req.Header.Set(header, "excluded-value")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if rec.Body.String() != "s" {
				t.Fatalf("expected subject %q, got %q (the excluded header %q reached the plugin)", "s", rec.Body.String(), canonical)
			}
			if _, present := mock.lastHeaders[canonical]; present {
				t.Fatalf("expected the plugin to never observe the excluded header %q, got %v", canonical, mock.lastHeaders)
			}
		})
	}
}

func TestMiddleware_DifferentCredentialsDifferentCacheKeys(t *testing.T) {
	e, _, _, mock := setupMiddleware(t, true)

	// First request: alice
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Authorization", "Basic YWxpY2U6cGFzcw==") // alice:pass
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	// Second request: bob (different credentials → cache miss)
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Basic Ym9iOnBhc3M=") // bob:pass
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if mock.callCount != 2 {
		t.Fatalf("expected 2 Validate calls for different credentials, got %d", mock.callCount)
	}
}

func TestMiddleware_DisconnectedHandleReturns503(t *testing.T) {
	// Handle with no connection — Validate returns error → 503
	handle := NewAuthPluginHandle("basic", "/path", nil)
	cache := NewAuthCache()

	e := echo.New()
	e.Use(NewAuthMiddleware(handle, cache))
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for disconnected handle, got %d", rec.Code)
	}
}

func TestMiddleware_InitErrorReturns503WithMessage(t *testing.T) {
	// Handle with permanent init error — Validate returns the error → 503
	handle := NewAuthPluginHandle("basic", "/path", nil)
	initErr := fmt.Errorf("auth plugin \"basic\": missing required config field")
	handle.SetInitError(initErr)

	cache := NewAuthCache()

	e := echo.New()
	e.Use(NewAuthMiddleware(handle, cache))
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for init error, got %d", rec.Code)
	}
}

func TestMiddleware_HealthEndpointSkipped(t *testing.T) {
	mock := &testMiddlewarePlugin{validResponse: false}
	clientConn, serverConn := net.Pipe()
	srv := rpc.NewServer()
	_ = srv.RegisterName("AuthPlugin", mock)
	go srv.ServeConn(serverConn)
	client := rpc.NewClient(clientConn)
	t.Cleanup(func() { _ = client.Close() })

	handle := NewAuthPluginHandle("basic", "/path", nil)
	handle.Connect(dummyConn(), client)

	e := echo.New()
	e.Use(NewAuthMiddleware(handle, NewAuthCache()))
	e.GET("/api/v1/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for health endpoint, got %d", rec.Code)
	}
	if mock.callCount != 0 {
		t.Fatalf("expected 0 Validate calls for health, got %d", mock.callCount)
	}
}
