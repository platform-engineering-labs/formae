// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_process_supervisor

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func serverCfgFixture() pkgmodel.ServerConfig {
	return pkgmodel.ServerConfig{
		Nodename:      "formae",
		Hostname:      "localhost",
		Secret:        "cookie",
		ErgoPort:      4370,
		RegistrarPort: 4499,
	}
}

func TestBuildOidcCredentialEnv_CarriesSpawnTokenAndConfig(t *testing.T) {
	cfg := json.RawMessage(`{"issuer":"x"}`)
	env1 := buildOidcCredentialEnv(serverCfgFixture(), "fai", "tok-1", cfg)
	require.Equal(t, "tok-1", env1[gen.Env("FORMAE_SPAWN_TOKEN")])
	require.Equal(t, base64.StdEncoding.EncodeToString(cfg), env1[gen.Env("FORMAE_PLUGIN_CONFIG")])
	require.NotEmpty(t, env1[gen.Env("FORMAE_AGENT_NODE")])
}

func TestBuildOidcCredentialEnv_OmitsConfigWhenAbsent(t *testing.T) {
	env := buildOidcCredentialEnv(serverCfgFixture(), "fai", "tok-1", nil)
	_, present := env[gen.Env("FORMAE_PLUGIN_CONFIG")]
	assert.False(t, present)
}

func TestBuildOidcCredentialEnv_BrokerNodeIsDistinctFromAgentNode(t *testing.T) {
	env := buildOidcCredentialEnv(serverCfgFixture(), "fai", "tok-1", nil)
	assert.Equal(t, "formae@localhost", env[gen.Env("FORMAE_AGENT_NODE")])
	assert.NotEqual(t, env[gen.Env("FORMAE_AGENT_NODE")], env[gen.Env("FORMAE_PLUGIN_NODE")])
	assert.Equal(t, "cookie", env[gen.Env("FORMAE_NETWORK_COOKIE")])
}

func TestMintSpawnToken_UniquePerCall(t *testing.T) {
	require.NotEqual(t, mintSpawnToken(), mintSpawnToken())
}

func TestBrokersToSpawn_SkipsDisabledConfigEntries(t *testing.T) {
	infos := []plugin.OidcCredentialPluginInfo{
		{Name: "fai", Namespaces: []string{"aws"}},
		{Name: "other", Namespaces: []string{"gcp"}},
	}
	configs := []pkgmodel.OidcCredentialPluginUserConfig{
		{Type: "other", Enabled: false},
	}

	spawn := brokersToSpawn(configs, infos)

	require.Len(t, spawn, 1)
	assert.Equal(t, "fai", spawn[0].Name)
}

func TestBrokersToSpawn_SpawnsDiscoveredBrokersWithoutConfig(t *testing.T) {
	infos := []plugin.OidcCredentialPluginInfo{{Name: "fai"}}

	spawn := brokersToSpawn(nil, infos)

	require.Len(t, spawn, 1)
	assert.Equal(t, "fai", spawn[0].Name)
}

func TestOidcCredentialTag_RoundTripsAndDoesNotCollideWithNamespaces(t *testing.T) {
	tag := oidcCredentialTag("fai")

	assert.True(t, isOidcCredentialTag(tag))
	assert.Equal(t, "fai", oidcCredentialTagName(tag))
	assert.False(t, isOidcCredentialTag("fai"))
	assert.False(t, isOidcCredentialTag("AWS"))
}
