// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The control-plane client's own suite lives with it in internal/cli/cloudapi.
// These are the few fixtures the gate tests still drive it through: a valid
// installation record, a server that serves some, and a capture of what that
// server was actually asked.

// rawInstallation is an installation record as it appears in the response,
// built as raw JSON so tests can express shapes the Go type cannot hold.
type rawInstallation map[string]any

// validInstallation returns a record that every validation rule accepts.
func validInstallation(id string) rawInstallation {
	return rawInstallation{
		"installationId":   id,
		"installationName": "prod",
		"tenantName":       "acme",
		"orgName":          "acme-inc",
		"endpoint":         testOrigin,
		"issuerPath":       "/auth/acme",
		"state":            "active",
	}
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

// capture records what a test server was actually asked, so a test can assert
// on requests that must never happen as well as on ones that must.
type capture struct {
	mu       sync.Mutex
	requests int
	header   http.Header
	method   string
	path     string
}

func (c *capture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	c.header = r.Header.Clone()
	c.method = r.Method
	c.path = r.URL.Path
}

func (c *capture) snapshot() (int, http.Header, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests, c.header, c.method, c.path
}

// serveInstallations starts a server answering with a 200 and the given records.
func serveInstallations(t *testing.T, records ...any) (*httptest.Server, *capture) {
	t.Helper()
	if records == nil {
		records = []any{}
	}
	body := marshalJSON(t, map[string]any{"results": records})
	seen := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}
