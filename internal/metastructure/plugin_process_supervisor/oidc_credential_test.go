// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_process_supervisor

import (
	"encoding/base64"
	"encoding/json"
	"ergo.services/ergo/testing/unit"
	"fmt"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"strings"
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

func TestBuildOidcCredentialEnv_ClearsConfigWhenAbsent(t *testing.T) {
	env := buildOidcCredentialEnv(serverCfgFixture(), "fai", "tok-1", nil)
	value, present := env[gen.Env("FORMAE_PLUGIN_CONFIG")]
	require.True(t, present)
	assert.Empty(t, value)
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

func TestOidcConfigIdentity_CanonicalAndSensitiveToConfiguration(t *testing.T) {
	first, err := oidcConfigIdentity(json.RawMessage(`{"subject":"agent","signingKeyArn":"arn:stable","nested":{"a":1,"b":2}}`))
	require.NoError(t, err)
	same, err := oidcConfigIdentity(json.RawMessage(`{ "nested": {"b":2,"a":1}, "signingKeyArn":"arn:stable", "subject":"agent" }`))
	require.NoError(t, err)
	require.Equal(t, first, same, "key refresh behind stable secret ARN does not change identity")
	changed, err := oidcConfigIdentity(json.RawMessage(`{"subject":"new-agent","signingKeyArn":"arn:stable","nested":{"a":1,"b":2}}`))
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	large, err := oidcConfigIdentity(json.RawMessage(`{"number":9007199254740992}`))
	require.NoError(t, err)
	next, err := oidcConfigIdentity(json.RawMessage(`{"number":9007199254740993}`))
	require.NoError(t, err)
	require.NotEqual(t, large, next, "canonicalization retains integer precision")
	_, err = oidcConfigIdentity(json.RawMessage(`{"bad":`))
	require.Error(t, err)
}

func TestOidcLaunch_RegistersBeforeSpawnAndLogsSendFailure(t *testing.T) {
	for _, sendError := range []error{nil, gen.ErrProcessUnknown} {
		t.Run(fmt.Sprint(sendError), func(t *testing.T) {
			actor, err := unit.Spawn(t, NewPluginProcessSupervisor, unit.WithEnv(map[gen.Env]any{"ServerConfig": serverCfgFixture()}))
			require.NoError(t, err)
			p := actor.Behavior().(*PluginProcessSupervisor)
			transport := &launchTestProcess{Process: p.Process, sendError: sendError, log: &launchTestLog{Log: p.Log()}}
			p.Process = transport
			entry := &oidcBrokerEntry{name: "fai", version: "discovered-version", binaryPath: "/bin/true", namespaces: []string{"k8s", "Aws", "K8S"}}
			require.NoError(t, p.spawnOidcCredentialBroker(entry))
			require.True(t, transport.spawned)
			require.Len(t, transport.registrations, 1)
			reg := transport.registrations[0]
			require.Equal(t, "discovered-version", reg.Version)
			require.Equal(t, entry.spawnToken, reg.SpawnToken)
			require.Equal(t, entry.nodeName, reg.NodeName)
			require.NotEmpty(t, reg.ConfigIdentity)
			require.Equal(t, []string{"AWS", "K8S"}, reg.Namespaces)
			entry.namespaces = []string{"K8S", "aws"}
			require.NoError(t, p.spawnOidcCredentialBroker(entry))
			require.Equal(t, reg.Namespaces, transport.registrations[1].Namespaces)
			require.Equal(t, reg.ConfigIdentity, transport.registrations[1].ConfigIdentity)
			if sendError != nil {
				require.Contains(t, strings.Join(transport.log.errors, " "), "register")
				t.Log(strings.Join(transport.log.errors, "; "))
			} else {
				require.Empty(t, transport.log.errors)
			}
		})
	}
}

type launchTestProcess struct {
	gen.Process
	sendError     error
	registrations []messages.RegisterOidcCredentialLaunch
	spawned       bool
	log           *launchTestLog
}

func (p *launchTestProcess) Send(to any, msg any) error {
	if reg, ok := msg.(messages.RegisterOidcCredentialLaunch); ok {
		p.registrations = append(p.registrations, reg)
		return p.sendError
	}
	return p.Process.Send(to, msg)
}
func (p *launchTestProcess) SpawnMeta(behavior gen.MetaBehavior, opts gen.MetaOptions) (gen.Alias, error) {
	if len(p.registrations) == 0 {
		return gen.Alias{}, fmt.Errorf("spawn preceded launch registration")
	}
	p.spawned = true
	return p.Process.SpawnMeta(behavior, opts)
}
func (p *launchTestProcess) Log() gen.Log { return p.log }

type launchTestLog struct {
	gen.Log
	errors []string
}

func (l *launchTestLog) Error(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

func TestOidcLaunch_UsesDiscoveredVersionAndNamespaces(t *testing.T) {
	actor, err := unit.Spawn(t, NewPluginProcessSupervisor, unit.WithEnv(map[gen.Env]any{
		"ServerConfig":          serverCfgFixture(),
		"OidcCredentialPlugins": []plugin.OidcCredentialPluginInfo{{Name: "fai", Version: "manifest-v2", Namespaces: []string{"k8s", "AWS"}, BinaryPath: "/bin/true"}},
	}))
	require.NoError(t, err)
	registered := false
	for _, event := range actor.Events() {
		switch event := event.(type) {
		case unit.SendEvent:
			if reg, ok := event.Message.(messages.RegisterOidcCredentialLaunch); ok {
				require.Equal(t, "manifest-v2", reg.Version)
				require.Equal(t, []string{"AWS", "K8S"}, reg.Namespaces)
				registered = true
			}
		case unit.SpawnMetaEvent:
			require.True(t, registered, "discovered identity must be registered before child start")
		}
	}
	require.True(t, registered)
}
