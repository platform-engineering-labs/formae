// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCanonicalOrigin_Accepted covers the shapes that canonicalise, including
// the canonicalisation itself: the scheme and host are lowercased and a
// redundant :443 is dropped, so two spellings of the same origin compare
// equal as strings.
func TestCanonicalOrigin_Accepted(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain https origin", raw: "https://cloud.formae.io", want: "https://cloud.formae.io"},
		{name: "trailing slash is not a path", raw: "https://cloud.formae.io/", want: "https://cloud.formae.io"},
		{name: "host is lowercased", raw: "https://Cloud.Formae.IO", want: "https://cloud.formae.io"},
		{name: "scheme is lowercased", raw: "HTTPS://cloud.formae.io", want: "https://cloud.formae.io"},
		{name: "redundant 443 removed", raw: "https://cloud.formae.io:443", want: "https://cloud.formae.io"},
		{name: "redundant 443 removed with trailing slash", raw: "https://cloud.formae.io:443/", want: "https://cloud.formae.io"},
		{name: "non-default port kept", raw: "https://cloud.formae.io:8443", want: "https://cloud.formae.io:8443"},
		{name: "http localhost", raw: "http://localhost", want: "http://localhost"},
		{name: "http localhost with port", raw: "http://localhost:49684", want: "http://localhost:49684"},
		{name: "http loopback v4", raw: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "http loopback v6", raw: "http://[::1]:8080", want: "http://[::1]:8080"},
		{name: "https localhost still fine", raw: "https://localhost:8443", want: "https://localhost:8443"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalOrigin(tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestCanonicalOrigin_Rejected covers every shape that must not become an
// origin. An origin names where formae sends a bearer token and which records
// a ledger entry belongs to, so anything ambiguous is rejected rather than
// trimmed into shape.
func TestCanonicalOrigin_Rejected(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "relative path", raw: "/api/v1"},
		{name: "bare host without scheme", raw: "cloud.formae.io"},
		{name: "scheme-relative", raw: "//cloud.formae.io"},
		{name: "not a URL at all", raw: "not a url"},
		{name: "control characters", raw: "https://cloud.formae.io\n"},
		{name: "http for a non-loopback host", raw: "http://cloud.formae.io"},
		{name: "http for a host merely containing localhost", raw: "http://localhost.evil.example"},
		{name: "http for a near-loopback address", raw: "http://127.0.0.2"},
		{name: "unsupported scheme", raw: "ftp://cloud.formae.io"},
		{name: "file scheme", raw: "file:///etc/passwd"},
		{name: "no host", raw: "https://"},
		{name: "opaque", raw: "https:cloud.formae.io"},
		{name: "path", raw: "https://cloud.formae.io/api"},
		{name: "deep path", raw: "https://cloud.formae.io/api/v1/me"},
		{name: "userinfo", raw: "https://user@cloud.formae.io"},
		{name: "userinfo with password", raw: "https://user:pass@cloud.formae.io"},
		{name: "query", raw: "https://cloud.formae.io?token=abc"},
		{name: "fragment", raw: "https://cloud.formae.io#frag"},
		{name: "path on a loopback origin", raw: "http://localhost:49684/api"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalOrigin(tc.raw)
			assert.Error(t, err)
			assert.Empty(t, got)
		})
	}
}

// TestCanonicalOrigin_IsIdempotent verifies that canonicalising an already
// canonical origin returns it unchanged. Origins are stored in the ledger and
// compared as strings, so a second pass must not shift the value.
func TestCanonicalOrigin_IsIdempotent(t *testing.T) {
	for _, raw := range []string{
		"https://Cloud.Formae.IO:443/",
		"https://cloud.formae.io:8443",
		"http://127.0.0.1:8080",
	} {
		once, err := canonicalOrigin(raw)
		require.NoError(t, err)
		twice, err := canonicalOrigin(once)
		require.NoError(t, err)
		assert.Equal(t, once, twice)
	}
}
