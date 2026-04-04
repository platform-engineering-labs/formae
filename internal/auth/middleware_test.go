// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// testMiddlewarePlugin is a configurable mock for middleware tests.
type testMiddlewarePlugin struct {
	validResponse bool
	callCount     int
}

func (p *testMiddlewarePlugin) Init(req *pkgauth.InitRequest, resp *pkgauth.InitResponse) error {
	return nil
}

func (p *testMiddlewarePlugin) Validate(req *pkgauth.ValidateRequest, resp *pkgauth.ValidateResponse) error {
	p.callCount++
	resp.Valid = p.validResponse
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
		return c.String(http.StatusOK, "ok")
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
