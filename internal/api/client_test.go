// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatEndpointStandardPort(t *testing.T) {
	want := "http://localhost:49684"
	got := formatEndpoint("http://localhost", 49684)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPSDefault(t *testing.T) {
	want := "https://example.awsapprunner.com"
	got := formatEndpoint("https://example.awsapprunner.com", 443)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPDefault(t *testing.T) {
	want := "http://example.com"
	got := formatEndpoint("http://example.com", 80)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPSNonDefault(t *testing.T) {
	want := "https://example.com:8443"
	got := formatEndpoint("https://example.com", 8443)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPWith443(t *testing.T) {
	want := "http://example.com:443"
	got := formatEndpoint("http://example.com", 443)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPSWith80(t *testing.T) {
	want := "https://example.com:80"
	got := formatEndpoint("https://example.com", 80)

	assert.Equal(t, want, got)
}

// TestNewClientInsecureSkipVerify verifies the opt-in insecureSkipVerify knob:
// with it set, the client reaches an agent fronted by a self-signed cert; without
// it (the default), the self-signed cert is rejected at the TLS layer.
func TestNewClientInsecureSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	base := "https://" + u.Hostname()

	insecure := NewClient(pkgmodel.APIConfig{URL: base, Port: port, InsecureSkipVerify: true}, nil, nil)
	_, err = insecure.Stats()
	require.NoError(t, err, "insecureSkipVerify client should reach the self-signed server")

	secure := NewClient(pkgmodel.APIConfig{URL: base, Port: port}, nil, nil)
	_, err = secure.Stats()
	require.Error(t, err, "default client should reject the self-signed cert")
	require.Contains(t, strings.ToLower(err.Error()), "certificate")
}
