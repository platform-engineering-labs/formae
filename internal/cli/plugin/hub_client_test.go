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
