//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func recordingServer(t *testing.T, seen *[]http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.0.0"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHostedConnectionSendsInstallationHeaderOnEveryRequest(t *testing.T) {
	var seen []http.Header
	srv := recordingServer(t, &seen)

	conn := &pkgmodel.HostedConnection{
		Endpoint:     srv.URL,
		Installation: "3f2b8c14-0000-4000-8000-000000000000",
	}
	c := NewClient(conn, http.Header{"Authorization": []string{"Bearer t"}}, srv.Client())

	_, err := c.Stats()
	require.NoError(t, err)
	_, err = c.Stats()
	require.NoError(t, err)

	require.Len(t, seen, 2)
	for _, h := range seen {
		assert.Equal(t, []string{"3f2b8c14-0000-4000-8000-000000000000"}, h.Values(InstallationHeader),
			"the edge rejects the routing header duplicated or comma-joined")
		assert.Equal(t, "Bearer t", h.Get("Authorization"))
	}
}

func TestClassicConnectionSendsNoInstallationHeader(t *testing.T) {
	var seen []http.Header
	srv := recordingServer(t, &seen)

	c := NewClient(&pkgmodel.ClassicConnection{URL: srv.URL}, nil, srv.Client())

	_, err := c.Stats()
	require.NoError(t, err)

	require.Len(t, seen, 1)
	assert.Empty(t, seen[0].Values(InstallationHeader))
	assert.Empty(t, seen[0].Get("Authorization"))
}

// A profile that configures an auth plugin against a self-hosted agent still
// authenticates: classic and unauthenticated are not the same thing.
func TestClassicConnectionSendsAuthorizationWhenConfigured(t *testing.T) {
	var seen []http.Header
	srv := recordingServer(t, &seen)

	c := NewClient(&pkgmodel.ClassicConnection{URL: srv.URL},
		http.Header{"Authorization": []string{"Basic abc"}}, srv.Client())

	_, err := c.Stats()
	require.NoError(t, err)

	require.Len(t, seen, 1)
	assert.Equal(t, "Basic abc", seen[0].Get("Authorization"))
}

// A redirect to another origin must carry neither the credential nor the
// routing header. Both test servers listen on 127.0.0.1, so this also pins
// that the policy compares the port: a hostname-only check would pass here.
func TestHostedConnectionRefusesCrossOriginRedirect(t *testing.T) {
	var elsewhereSeen []http.Header
	elsewhere := recordingServer(t, &elsewhereSeen)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/v1/stats", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	conn := &pkgmodel.HostedConnection{
		Endpoint:     redirector.URL,
		Installation: "3f2b8c14-0000-4000-8000-000000000000",
	}
	c := NewClient(conn, http.Header{"Authorization": []string{"Bearer t"}}, redirector.Client())

	_, err := c.Stats()
	require.Error(t, err)
	assert.Empty(t, elsewhereSeen, "neither the credential nor the routing header may reach another origin")
}

func TestHostedConnectionFollowsSameOriginRedirect(t *testing.T) {
	var seen []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/redirected" {
			http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
			return
		}
		seen = append(seen, r.Header.Clone())
		_, _ = w.Write([]byte(`{"version":"0.0.0"}`))
	}))
	t.Cleanup(srv.Close)

	conn := &pkgmodel.HostedConnection{
		Endpoint:     srv.URL,
		Installation: "3f2b8c14-0000-4000-8000-000000000000",
	}
	c := NewClient(conn, http.Header{"Authorization": []string{"Bearer t"}}, srv.Client())

	_, err := c.Stats()
	require.NoError(t, err)
	require.Len(t, seen, 1)
	assert.Equal(t, "3f2b8c14-0000-4000-8000-000000000000", seen[0].Get(InstallationHeader))
	assert.Equal(t, "Bearer t", seen[0].Get("Authorization"))
}
