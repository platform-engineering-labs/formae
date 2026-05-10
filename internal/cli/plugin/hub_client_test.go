// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(serverURL string, totalTimeout time.Duration) *httpHubClient {
	return &httpHubClient{
		baseURL: serverURL,
		http:    &http.Client{Timeout: totalTimeout},
	}
}

func TestHubClient_Available_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/plugins/foo", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"plugin_not_found"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	res, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.NoError(t, err)
	assert.True(t, res.Available)
	assert.Empty(t, res.GitHubRepoURL)
}

func TestHubClient_Conflict_200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"foo","github_repo_url":"https://github.com/x/y"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	res, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.NoError(t, err)
	assert.False(t, res.Available)
	assert.Equal(t, "https://github.com/x/y", res.GitHubRepoURL)
}

func TestHubClient_NameMismatch_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"otherplugin","github_repo_url":"https://github.com/x/y"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable), "name mismatch must NOT be HubUnreachableError")
	assert.Contains(t, err.Error(), "foo")
	assert.Contains(t, err.Error(), "otherplugin")
}

func TestHubClient_500_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.True(t, errors.As(err, &unreachable))
	assert.Contains(t, err.Error(), "500")
}

func TestHubClient_429_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.True(t, errors.As(err, &unreachable))
}

func TestHubClient_408_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestTimeout)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.True(t, errors.As(err, &unreachable))
}

func TestHubClient_ReadTimeout_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 50*time.Millisecond)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.True(t, errors.As(err, &unreachable))
}

func TestHubClient_401_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable))
	assert.Contains(t, err.Error(), "401")
}

func TestHubClient_403_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable))
	assert.Contains(t, err.Error(), "403")
}

func TestHubClient_405_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable))
	assert.Contains(t, err.Error(), "405")
}

func TestHubClient_400_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable))
}

func TestHubClient_200_NonJSON_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not the right hub</html>"))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable))
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestHubClient_200_MissingFields_HardFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"foo":"bar"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, 1*time.Second)
	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable))
	assert.Contains(t, err.Error(), "missing required fields")
}

func TestHubClient_TLSHostnameMismatch_HardFail(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use the real NewHubClient so the production TLS verification is
	// exercised — the test client used elsewhere lacks a Transport and
	// would inherit DefaultTransport, which does verify TLS by default
	// but doesn't carry our other phase timeouts. Casting back to the
	// concrete type just to keep the call path identical.
	c := NewHubClient(srv.URL).(*httpHubClient)

	_, err := c.CheckPluginAvailability(context.Background(), "foo")

	require.Error(t, err)
	var unreachable *HubUnreachableError
	assert.False(t, errors.As(err, &unreachable), "TLS validation failure must NOT be HubUnreachableError")
	assert.Contains(t, err.Error(), "TLS validation failed")
}
