//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package configview

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func mustParseURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return *u
}

func hostedConfig(t *testing.T) *pkgmodel.Config {
	t.Helper()
	return &pkgmodel.Config{
		Cli: pkgmodel.CliConfig{
			Connection: &pkgmodel.HostedConnection{
				Endpoint:     "https://cloud.formae.ai",
				Installation: "3f2b8c14-0000-4000-8000-000000000000",
				Auth:         json.RawMessage(`{"type":"oidc","clientId":"formae-cli"}`),
			},
			Theme: "quiet",
		},
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{Port: 49684, Secret: "s3cret"},
			Retry:  pkgmodel.RetryConfig{RetryDelay: 30 * time.Second},
			Datastore: pkgmodel.DatastoreConfig{
				DatastoreType: pkgmodel.PostgresDatastore,
				Postgres: pkgmodel.PostgresConfig{
					Host:             "db.internal",
					Password:         "hunter2",
					ConnectionParams: "sslmode=require&password=alsosecret",
				},
			},
		},
		Artifacts: pkgmodel.ArtifactConfig{
			URL: mustParseURL(t, "https://bob:artifactpw@hub.example/repos/x#stable"),
			Repositories: []pkgmodel.Repository{
				{
					URI:  mustParseURL(t, "https://alice:repopw@hub.example/repos/y"),
					Type: pkgmodel.RepositoryTypeFormaePlugin,
				},
			},
		},
	}
}

func TestHostedConnectionIsTagged(t *testing.T) {
	v := From("prod", hostedConfig(t), Redacted)

	assert.Equal(t, SchemaVersion, v["schemaVersion"])
	assert.Equal(t, "prod", v["profile"])

	conn := v["cli"].(map[string]any)["connection"].(map[string]any)
	assert.Equal(t, "hosted", conn["mode"])
	assert.Equal(t, "https://cloud.formae.ai", conn["endpoint"])
	assert.Equal(t, "3f2b8c14-0000-4000-8000-000000000000", conn["installation"])
	assert.NotContains(t, conn, "url")
}

func TestClassicConnectionIsTaggedAndCarriesNoInstallation(t *testing.T) {
	cfg := hostedConfig(t)
	cfg.Cli.Connection = &pkgmodel.ClassicConnection{URL: "http://localhost", Port: 49684}

	conn := From("dev", cfg, Redacted)["cli"].(map[string]any)["connection"].(map[string]any)

	assert.Equal(t, "classic", conn["mode"])
	assert.Equal(t, "http://localhost", conn["url"])
	assert.Equal(t, 49684, conn["port"])
	assert.NotContains(t, conn, "installation")
}

// An opaque auth block is emitted as its discriminator alone: a third-party
// plugin's field names are unknowable, so anything else risks printing a
// credential under a name no allowlist anticipated.
func TestOpaqueAuthEmitsTypeOnly(t *testing.T) {
	conn := From("prod", hostedConfig(t), Redacted)["cli"].(map[string]any)["connection"].(map[string]any)

	assert.Equal(t, map[string]any{"type": "oidc"}, conn["auth"])
}

func TestTypedSecretsAreMasked(t *testing.T) {
	v := From("prod", hostedConfig(t), Redacted)
	agent := v["agent"].(map[string]any)
	pg := agent["datastore"].(map[string]any)["postgres"].(map[string]any)

	assert.Equal(t, Redacted, agent["server"].(map[string]any)["secret"])
	assert.Equal(t, Redacted, pg["password"])
	assert.Equal(t, "db.internal", pg["host"], "non-secret neighbours stay readable")
	assert.NotContains(t, pg, "connectionParams",
		"connection parameters are appended to the connection string and can carry a password")
}

func TestMaskIsCallerChosen(t *testing.T) {
	v := From("prod", hostedConfig(t), HumanMask)

	assert.Equal(t, HumanMask, v["agent"].(map[string]any)["server"].(map[string]any)["secret"])
}

func TestDurationsRenderAsStrings(t *testing.T) {
	v := From("prod", hostedConfig(t), Redacted)

	assert.Equal(t, "30s", v["agent"].(map[string]any)["retry"].(map[string]any)["retryDelay"])
}

func TestNoSecretSurvivesSerialisation(t *testing.T) {
	out, err := json.Marshal(From("prod", hostedConfig(t), Redacted))
	require.NoError(t, err)

	assert.Contains(t, string(out), `"schemaVersion":1`)
	for _, secret := range []string{"hunter2", "s3cret", "alsosecret", "formae-cli", "artifactpw", "repopw"} {
		assert.NotContains(t, string(out), secret)
	}
}

func TestNetworkSectionIsOmittedWhenUnset(t *testing.T) {
	v := From("prod", hostedConfig(t), Redacted)

	assert.NotContains(t, v, "network")
}

func TestNetworkSectionMasksTheTailscaleAuthKey(t *testing.T) {
	cfg := hostedConfig(t)
	cfg.Network = &pkgmodel.NetworkConfig{
		Type: "tailscale",
		Tailscale: &pkgmodel.TailscaleConfig{
			TLS:           true,
			Hostname:      "agent",
			AdvertiseTags: []string{"tag:formae"},
			AuthKey:       "tskey-auth-secret",
		},
	}

	v := From("prod", cfg, Redacted)

	network := v["network"].(map[string]any)
	assert.Equal(t, "tailscale", network["type"])
	tailscale := network["tailscale"].(map[string]any)
	assert.Equal(t, "agent", tailscale["hostname"])
	assert.Equal(t, Redacted, tailscale["authKey"])
	assert.NotContains(t, fmt.Sprintf("%v", v), "tskey-auth-secret")
}

// An artifacts URL or repository URI that embeds credentials is redacted:
// url.URL.String() prints embedded userinfo verbatim, so the view must use
// Redacted() instead.
func TestArtifactURLCredentialsAreRedacted(t *testing.T) {
	v := From("prod", hostedConfig(t), Redacted)
	artifacts := v["artifacts"].(map[string]any)

	assert.Equal(t, "https://bob:xxxxx@hub.example/repos/x#stable", artifacts["url"])

	repos := artifacts["repositories"].([]any)
	require.Len(t, repos, 1)
	assert.Equal(t, "https://alice:xxxxx@hub.example/repos/y", repos[0].(map[string]any)["uri"])
}
