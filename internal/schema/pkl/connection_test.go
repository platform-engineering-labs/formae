//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func loadConfig(t *testing.T, path string) (*pkgmodel.Config, error) {
	t.Helper()
	return PKL{}.FormaeConfig(path)
}

func TestHostedConnectionTranslates(t *testing.T) {
	cfg, err := loadConfig(t, "testdata/config/connection_hosted.pkl")
	require.NoError(t, err)

	hosted, ok := cfg.Cli.Connection.(*pkgmodel.HostedConnection)
	require.True(t, ok, "expected *HostedConnection, got %T", cfg.Cli.Connection)
	assert.Equal(t, "https://cloud.formae.ai", hosted.Endpoint)
	assert.Equal(t, "3f2b8c14-0000-4000-8000-000000000000", hosted.Installation)
	assert.JSONEq(t, `{"type":"oidc","issuer":"https://auth.formae.ai"}`, string(hosted.Auth))
}

func TestAbsentConnectionResolvesToClassicDefaults(t *testing.T) {
	cfg, err := loadConfig(t, "testdata/config/connection_absent.pkl")
	require.NoError(t, err)

	classic, ok := cfg.Cli.Connection.(*pkgmodel.ClassicConnection)
	require.True(t, ok, "expected *ClassicConnection, got %T", cfg.Cli.Connection)
	assert.Equal(t, "http://localhost", classic.URL)
	assert.Equal(t, 49684, classic.Port)
	assert.Empty(t, cfg.Warnings)
}

func TestLegacyAPISynthesisesClassicAndWarns(t *testing.T) {
	cfg, err := loadConfig(t, "testdata/config/legacy_api_nested.pkl")
	require.NoError(t, err)

	classic, ok := cfg.Cli.Connection.(*pkgmodel.ClassicConnection)
	require.True(t, ok, "expected *ClassicConnection, got %T", cfg.Cli.Connection)
	assert.Equal(t, "http://agent.example", classic.URL)
	assert.Equal(t, 8080, classic.Port)
	require.Len(t, cfg.Warnings, 1)
	assert.Contains(t, cfg.Warnings[0], "cli.api is deprecated")
}

func TestLegacyAuthSynthesisesClassicAndWarns(t *testing.T) {
	cfg, err := loadConfig(t, "testdata/config/legacy_auth_only.pkl")
	require.NoError(t, err)

	classic, ok := cfg.Cli.Connection.(*pkgmodel.ClassicConnection)
	require.True(t, ok, "expected *ClassicConnection, got %T", cfg.Cli.Connection)
	assert.JSONEq(t, `{"type":"basic","username":"u","password":"p"}`, string(classic.Auth))
	require.Len(t, cfg.Warnings, 1)
	assert.Contains(t, cfg.Warnings[0], "cli.auth is deprecated")
}

// The legacy fields carry a credential, so a configuration that sets one
// alongside an explicit connection is ambiguous about which endpoint that
// credential is for. Every legacy source is rejected rather than ranked.
func TestConnectionWithALegacyFieldIsALoadError(t *testing.T) {
	cases := []struct{ name, fixture, wants string }{
		{"cli.api", "testdata/config/connection_and_legacy_api.pkl", "cli.api"},
		{"cli.auth", "testdata/config/connection_and_legacy_auth.pkl", "cli.auth"},
		{"plugins.authentication", "testdata/config/connection_and_plugins_auth.pkl", "plugins.authentication"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig(t, tc.fixture)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cli.connection")
			assert.Contains(t, err.Error(), tc.wants)
		})
	}
}

func TestLegacyFieldsAlsoPopulateTheCompatibilityView(t *testing.T) {
	cfg, err := loadConfig(t, "testdata/config/connection_hosted.pkl")
	require.NoError(t, err)

	assert.NotNil(t, cfg.Cli.AuthConfig())
	assert.Equal(t, cfg.Cli.AuthConfig(), cfg.Cli.Auth,
		"the legacy view is derived from the connection while callers migrate")
}

func TestHostedEndpointMustBeHTTPS(t *testing.T) {
	_, err := loadConfig(t, "testdata/config/hosted_bad_endpoint.pkl")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "https")
}

func TestCanonicalHostedEndpoint(t *testing.T) {
	cases := []struct{ name, in, want, wantErr string }{
		{name: "plain", in: "https://cloud.formae.ai", want: "https://cloud.formae.ai"},
		{name: "trailing slash", in: "https://cloud.formae.ai/", want: "https://cloud.formae.ai"},
		{name: "explicit 443", in: "https://cloud.formae.ai:443", want: "https://cloud.formae.ai"},
		{name: "other port kept", in: "https://cloud.formae.ai:8443", want: "https://cloud.formae.ai:8443"},
		{name: "uppercase", in: "HTTPS://Cloud.Formae.AI", want: "https://cloud.formae.ai"},
		{name: "http", in: "http://cloud.formae.ai", wantErr: "https"},
		{name: "userinfo", in: "https://u:p@cloud.formae.ai", wantErr: "credentials"},
		{name: "query", in: "https://cloud.formae.ai?a=b", wantErr: "query"},
		{name: "fragment", in: "https://cloud.formae.ai#f", wantErr: "fragment"},
		{name: "path", in: "https://cloud.formae.ai/api", wantErr: "path"},
		{name: "relative", in: "cloud.formae.ai", wantErr: "https"},
		{name: "empty", in: "", wantErr: "https"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalHostedEndpoint(tc.in)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateInstallation(t *testing.T) {
	assert.NoError(t, validateInstallation("3f2b8c14-0000-4000-8000-000000000000"))
	for _, bad := range []string{
		"", "3F2B8C14-0000-4000-8000-000000000000", "3f2b8c14", "not-a-uuid",
		" 3f2b8c14-0000-4000-8000-000000000000",
	} {
		assert.Error(t, validateInstallation(bad), "expected %q to be rejected", bad)
	}
}
