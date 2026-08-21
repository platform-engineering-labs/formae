// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"errors"
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

// TestResolvePlatform_Defaults verifies that with no flag and no env var set
// for either half, resolvePlatform returns the built-in defaults.
func TestResolvePlatform_Defaults(t *testing.T) {
	p, err := resolvePlatform("", "")
	require.NoError(t, err)
	assert.Equal(t, platform{Origin: DefaultCloudURL, Issuer: DefaultCloudIssuer}, p)
}

// TestResolvePlatform_PrecedencePerHalf pins flag-beats-env-beats-default,
// checked independently for each half. Whichever half is not under test is
// carried by a flag so the pair stays complete and the case under test is
// isolated to a single half's own precedence.
func TestResolvePlatform_PrecedencePerHalf(t *testing.T) {
	tests := []struct {
		name       string
		cloudFlag  string
		cloudEnv   string
		issuerFlag string
		issuerEnv  string
		wantOrigin string
		wantIssuer string
	}{
		{
			name:       "origin flag beats origin env",
			cloudFlag:  "https://flag.example.com",
			cloudEnv:   "https://env.example.com",
			issuerFlag: "https://issuer.example.com",
			wantOrigin: "https://flag.example.com",
			wantIssuer: "https://issuer.example.com",
		},
		{
			name:       "origin env beats origin default",
			cloudEnv:   "https://env.example.com",
			issuerFlag: "https://issuer.example.com",
			wantOrigin: "https://env.example.com",
			wantIssuer: "https://issuer.example.com",
		},
		{
			name:       "issuer flag beats issuer env",
			cloudFlag:  "https://cloud.example.com",
			issuerFlag: "https://flag.example.com",
			issuerEnv:  "https://env.example.com",
			wantOrigin: "https://cloud.example.com",
			wantIssuer: "https://flag.example.com",
		},
		{
			name:       "issuer env beats issuer default",
			cloudFlag:  "https://cloud.example.com",
			issuerEnv:  "https://env.example.com",
			wantOrigin: "https://cloud.example.com",
			wantIssuer: "https://env.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cloudEnv != "" {
				t.Setenv("FORMAE_CLOUD_URL", tc.cloudEnv)
			}
			if tc.issuerEnv != "" {
				t.Setenv("FORMAE_CLOUD_ISSUER", tc.issuerEnv)
			}

			p, err := resolvePlatform(tc.cloudFlag, tc.issuerFlag)
			require.NoError(t, err)
			assert.Equal(t, tc.wantOrigin, p.Origin)
			assert.Equal(t, tc.wantIssuer, p.Issuer)
		})
	}
}

// TestResolvePlatform_HalfSetIsError covers every way exactly one half can be
// set — via flag or via env var, for either half. A custom control plane
// paired with the default issuer (or vice versa) is exactly the mismatch the
// sync gate exists to catch, so it must not resolve.
func TestResolvePlatform_HalfSetIsError(t *testing.T) {
	tests := []struct {
		name       string
		cloudFlag  string
		issuerFlag string
		cloudEnv   string
		issuerEnv  string
	}{
		{name: "only --cloud flag", cloudFlag: "https://cloud.example.com"},
		{name: "only --cloud-issuer flag", issuerFlag: "https://issuer.example.com"},
		{name: "only FORMAE_CLOUD_URL env", cloudEnv: "https://cloud.example.com"},
		{name: "only FORMAE_CLOUD_ISSUER env", issuerEnv: "https://issuer.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cloudEnv != "" {
				t.Setenv("FORMAE_CLOUD_URL", tc.cloudEnv)
			}
			if tc.issuerEnv != "" {
				t.Setenv("FORMAE_CLOUD_ISSUER", tc.issuerEnv)
			}

			p, err := resolvePlatform(tc.cloudFlag, tc.issuerFlag)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errPlatformHalfSet))
			assert.Equal(t, platform{}, p)
		})
	}
}

// TestResolvePlatform_MixedSourcesPairIsComplete pins the case the half-set
// rule must not catch: one half from a flag and the other from an env var is
// a complete pair, not a half-set one, and resolves.
func TestResolvePlatform_MixedSourcesPairIsComplete(t *testing.T) {
	t.Setenv("FORMAE_CLOUD_ISSUER", "https://issuer.example.com")

	p, err := resolvePlatform("https://cloud.example.com", "")
	require.NoError(t, err)
	assert.Equal(t, platform{Origin: "https://cloud.example.com", Issuer: "https://issuer.example.com"}, p)
}

// TestResolvePlatform_CanonicalisesBothHalves asserts that both halves come
// back canonicalised, proving they are routed through canonicalOrigin rather
// than used as typed.
func TestResolvePlatform_CanonicalisesBothHalves(t *testing.T) {
	p, err := resolvePlatform("HTTPS://Cloud.Example.com:443/", "HTTPS://Issuer.Example.com:443/")
	require.NoError(t, err)
	assert.Equal(t, platform{Origin: "https://cloud.example.com", Issuer: "https://issuer.example.com"}, p)
}

// TestResolvePlatform_InvalidOverrideIsError asserts that a syntactically
// invalid override is an error rather than a silent fallback to the default,
// for each half. One rejected shape per half is enough: canonicalOrigin's own
// rules are covered exhaustively in TestCanonicalOrigin_Rejected.
func TestResolvePlatform_InvalidOverrideIsError(t *testing.T) {
	t.Run("invalid origin", func(t *testing.T) {
		p, err := resolvePlatform("not a url", "https://issuer.example.com")
		require.Error(t, err)
		assert.Equal(t, platform{}, p)
	})

	t.Run("invalid issuer", func(t *testing.T) {
		p, err := resolvePlatform("https://cloud.example.com", "not a url")
		require.Error(t, err)
		assert.Equal(t, platform{}, p)
	})
}

// TestResolvePlatform_EmptyEnvCountsAsSet pins the judgment call that an env
// var present but empty (FORMAE_CLOUD_URL="") counts as set, not unset.
// os.Getenv cannot distinguish absent from empty; treating a present-but-empty
// override as unset would let an explicitly set variable be silently ignored,
// which the pairing rule exists to prevent. So a present empty override
// either pairs with a set counterpart and fails canonicalisation (never a
// silent default), or is missing its counterpart and is a half-set error.
func TestResolvePlatform_EmptyEnvCountsAsSet(t *testing.T) {
	t.Run("paired with a set issuer, fails canonicalisation rather than defaulting", func(t *testing.T) {
		t.Setenv("FORMAE_CLOUD_URL", "")

		p, err := resolvePlatform("", "https://issuer.example.com")
		require.Error(t, err)
		assert.False(t, errors.Is(err, errPlatformHalfSet))
		assert.Equal(t, platform{}, p)
	})

	t.Run("alone, is a half-set error", func(t *testing.T) {
		t.Setenv("FORMAE_CLOUD_URL", "")

		p, err := resolvePlatform("", "")
		require.Error(t, err)
		assert.True(t, errors.Is(err, errPlatformHalfSet))
		assert.Equal(t, platform{}, p)
	})
}
