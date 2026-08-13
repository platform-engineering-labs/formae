// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	formae "github.com/platform-engineering-labs/formae"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// agentStatsJSON returns a minimal JSON stats body with the current formae
// version, so runBeforeCommand passes version compatibility.
func agentStatsJSON(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(apimodel.Stats{
		Version: formae.Version,
		AgentID: "test-agent",
	})
	require.NoError(t, err)
	return b
}

// newAppWithServer builds a minimal *App whose API config points at the given
// httptest server base URL (e.g. "http://127.0.0.1:PORT"). Usage reporting is
// disabled so runBeforeCommand does not call into a nil Usage sender.
func newAppWithServer(baseURL string) *App {
	return &App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{
				Connection: &pkgmodel.ClassicConnection{
					URL:  baseURL,
					Port: 80,
				},
				DisableUsageReporting: true,
			},
		},
	}
}

// TestListResourceSummaries_HappyPath verifies that ListResourceSummaries
// returns summaries from the agent unchanged on the happy path.
func TestListResourceSummaries_HappyPath(t *testing.T) {
	want := []pkgmodel.ResourceSummary{
		{Label: "my-bucket", Stack: "default", Type: "AWS::S3::Bucket", NativeID: "my-bucket", Ksuid: "3HCvcUX7215dJAkxJefX6Epd9VE"},
		{Label: "my-role", Stack: "default", Type: "AWS::IAM::Role", NativeID: "my-role", Ksuid: "3HCvcUX7215dJAkxJefX6Epd9VF"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentStatsJSON(t))
	})
	mux.HandleFunc("/api/v1/resources/summary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(want)
		_, _ = w.Write(b)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	a := newAppWithServer(ts.URL)
	got, nags, err := a.ListResourceSummaries("", false)

	require.NoError(t, err)
	assert.Empty(t, nags)
	assert.Equal(t, want, got)
}

// TestListResourceSummaries_EmptyList verifies that an empty summary list is
// returned as an empty non-nil slice, not nil.
func TestListResourceSummaries_EmptyList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentStatsJSON(t))
	})
	mux.HandleFunc("/api/v1/resources/summary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	a := newAppWithServer(ts.URL)
	got, _, err := a.ListResourceSummaries("", false)

	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TestResourceDetailByKsuid_HappyPath verifies that ResourceDetailByKsuid
// returns the resource when the agent finds it.
func TestResourceDetailByKsuid_HappyPath(t *testing.T) {
	want := &pkgmodel.Resource{
		Label:    "my-bucket",
		Stack:    "default",
		Type:     "AWS::S3::Bucket",
		NativeID: "my-bucket",
		Ksuid:    "3HCvcUX7215dJAkxJefX6Epd9VE",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentStatsJSON(t))
	})
	mux.HandleFunc("/api/v1/resources/by-ksuid/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(want)
		_, _ = w.Write(b)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	a := newAppWithServer(ts.URL)
	got, nags, err := a.ResourceDetailByKsuid("3HCvcUX7215dJAkxJefX6Epd9VE", false)

	require.NoError(t, err)
	assert.Empty(t, nags)
	require.NotNil(t, got)
	assert.Equal(t, want.Label, got.Label)
	assert.Equal(t, want.Ksuid, got.Ksuid)
}

// TestResourceDetailByKsuid_NotFound verifies that ResourceDetailByKsuid
// returns (nil, nil) when the agent reports no resource for the ksuid.
func TestResourceDetailByKsuid_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentStatsJSON(t))
	})
	mux.HandleFunc("/api/v1/resources/by-ksuid/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	a := newAppWithServer(ts.URL)
	got, nags, err := a.ResourceDetailByKsuid("3HCvcUX7215dJAkxJefX6Epd9VE", false)

	require.NoError(t, err)
	assert.Empty(t, nags)
	assert.Nil(t, got)
}
