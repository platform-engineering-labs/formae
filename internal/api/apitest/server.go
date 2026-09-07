// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apitest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// NewTestServer starts an httptest.Server backed by the given HTTP handler and
// returns its base URL (e.g. "http://127.0.0.1:PORT"). The server is shut down
// automatically via t.Cleanup.
//
// Callers build the handler using api.NewServer and pass srv.Handler():
//
//	fake := &apitest.FakeMetastructure{...}
//	srv := api.NewServer(ctx, fake, nil, nil, nil, nil)
//	baseURL := apitest.NewTestServer(t, srv.Handler())
func NewTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return ts.URL
}
