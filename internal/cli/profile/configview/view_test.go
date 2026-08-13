//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package configview

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func hostedConfig() *pkgmodel.Config {
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
	}
}

func TestHostedConnectionIsTagged(t *testing.T) {
	v := From("prod", hostedConfig(), Redacted)

	assert.Equal(t, SchemaVersion, v["schemaVersion"])
	assert.Equal(t, "prod", v["profile"])

	conn := v["cli"].(map[string]any)["connection"].(map[string]any)
	assert.Equal(t, "hosted", conn["mode"])
	assert.Equal(t, "https://cloud.formae.ai", conn["endpoint"])
	assert.Equal(t, "3f2b8c14-0000-4000-8000-000000000000", conn["installation"])
	assert.NotContains(t, conn, "url")
}

func TestClassicConnectionIsTaggedAndCarriesNoInstallation(t *testing.T) {
	cfg := hostedConfig()
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
	conn := From("prod", hostedConfig(), Redacted)["cli"].(map[string]any)["connection"].(map[string]any)

	assert.Equal(t, map[string]any{"type": "oidc"}, conn["auth"])
}

func TestTypedSecretsAreMasked(t *testing.T) {
	v := From("prod", hostedConfig(), Redacted)
	agent := v["agent"].(map[string]any)
	pg := agent["datastore"].(map[string]any)["postgres"].(map[string]any)

	assert.Equal(t, Redacted, agent["server"].(map[string]any)["secret"])
	assert.Equal(t, Redacted, pg["password"])
	assert.Equal(t, "db.internal", pg["host"], "non-secret neighbours stay readable")
	assert.NotContains(t, pg, "connectionParams",
		"connection parameters are appended to the connection string and can carry a password")
}

func TestMaskIsCallerChosen(t *testing.T) {
	v := From("prod", hostedConfig(), HumanMask)

	assert.Equal(t, HumanMask, v["agent"].(map[string]any)["server"].(map[string]any)["secret"])
}

func TestDurationsRenderAsStrings(t *testing.T) {
	v := From("prod", hostedConfig(), Redacted)

	assert.Equal(t, "30s", v["agent"].(map[string]any)["retry"].(map[string]any)["retryDelay"])
}

func TestNoSecretSurvivesSerialisation(t *testing.T) {
	out, err := json.Marshal(From("prod", hostedConfig(), Redacted))
	require.NoError(t, err)

	assert.Contains(t, string(out), `"schemaVersion":1`)
	for _, secret := range []string{"hunter2", "s3cret", "alsosecret", "formae-cli"} {
		assert.NotContains(t, string(out), secret)
	}
}
