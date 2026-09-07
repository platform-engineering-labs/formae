// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPlugin satisfies OidcCredentialPlugin without minting anything; the
// runWithSteps tests below stub out configure/startNode/announce, so the
// real IdentityToken method is never exercised.
type stubPlugin struct{}

func (stubPlugin) IdentityToken(ctx context.Context, req *OidcIdentityTokenRequest) (*OidcIdentityTokenResult, error) {
	return nil, nil
}

// fakeNode satisfies the node seam for tests that need startNode to
// succeed.
type fakeNode struct {
	sent []OidcCredentialPluginAnnouncement
}

func (n *fakeNode) Send(to gen.ProcessID, message any) error {
	if a, ok := message.(OidcCredentialPluginAnnouncement); ok {
		n.sent = append(n.sent, a)
	}
	return nil
}

func (n *fakeNode) Stop() {}

func manifestFixture() *Manifest {
	return &Manifest{
		Name:             "fai",
		Version:          "0.1.0",
		Type:             PluginTypeOidcCredential,
		Namespaces:       []string{"aws", "gcp"},
		MinFormaeVersion: "0.90.0",
	}
}

func envFixture() runEnv {
	return runEnv{
		agentNode:    "agent@localhost",
		pluginNode:   "fai-plugin@localhost",
		cookie:       "test-cookie",
		spawnToken:   "test-spawn-token",
		pluginConfig: nil,
	}
}

func TestRun_ConfigureErrorAbortsBeforeNodeAndAnnounce(t *testing.T) {
	calls := []string{}
	steps := runSteps{
		configure: func(p OidcCredentialPlugin, raw json.RawMessage) error {
			calls = append(calls, "configure")
			return errors.New("bad config")
		},
		startNode: func(cfg nodeConfig) (node, error) {
			calls = append(calls, "node")
			return nil, nil
		},
		announce: func(n node, a OidcCredentialPluginAnnouncement) error {
			calls = append(calls, "announce")
			return nil
		},
	}

	err := runWithSteps(stubPlugin{}, manifestFixture(), envFixture(), steps)
	require.Error(t, err)
	require.Equal(t, []string{"configure"}, calls)
}

func TestRun_ConfigureCalledWithNilWhenPayloadAbsent(t *testing.T) {
	t.Setenv("FORMAE_AGENT_NODE", "agent@localhost")
	t.Setenv("FORMAE_PLUGIN_NODE", "fai-plugin@localhost")
	t.Setenv("FORMAE_NETWORK_COOKIE", "test-cookie")
	t.Setenv("FORMAE_SPAWN_TOKEN", "test-spawn-token")
	require.NoError(t, os.Unsetenv("FORMAE_PLUGIN_CONFIG"))

	env, err := readEnv()
	require.NoError(t, err)
	require.Nil(t, env.pluginConfig, "FORMAE_PLUGIN_CONFIG absent must read back as nil, not empty-but-present")

	calls := []string{}
	var gotRaw json.RawMessage
	rawWasSet := false
	fn := &fakeNode{}

	steps := runSteps{
		configure: func(p OidcCredentialPlugin, raw json.RawMessage) error {
			calls = append(calls, "configure")
			gotRaw = raw
			rawWasSet = true
			return nil
		},
		startNode: func(cfg nodeConfig) (node, error) {
			calls = append(calls, "node")
			return fn, nil
		},
		announce: func(n node, a OidcCredentialPluginAnnouncement) error {
			calls = append(calls, "announce")
			return n.Send(gen.ProcessID{Name: "PluginCoordinator", Node: gen.Atom(env.agentNode)}, a)
		},
	}

	err = runWithSteps(stubPlugin{}, manifestFixture(), env, steps)
	require.NoError(t, err)

	require.True(t, rawWasSet, "configure must be called even when FORMAE_PLUGIN_CONFIG is absent")
	assert.Nil(t, gotRaw)
	assert.Equal(t, []string{"configure", "node", "announce"}, calls)

	require.Len(t, fn.sent, 1)
	sent := fn.sent[0]
	assert.Equal(t, "fai", sent.Name)
	assert.Equal(t, "0.1.0", sent.Version)
	assert.Equal(t, []string{"AWS", "GCP"}, sent.Namespaces, "announcement namespaces must be the manifest's normalized (uppercased) namespaces")
	assert.Equal(t, "test-spawn-token", sent.SpawnToken, "SpawnToken must come verbatim from FORMAE_SPAWN_TOKEN")
}

func TestRequireOidcCredentialManifest(t *testing.T) {
	require.NoError(t, requireOidcCredentialManifest(&Manifest{Type: PluginTypeOidcCredential}))

	err := requireOidcCredentialManifest(&Manifest{Type: "resource"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oidc-credential")
}

func TestReadEnv_RequiresCoreErgoVars(t *testing.T) {
	t.Setenv("FORMAE_AGENT_NODE", "")
	t.Setenv("FORMAE_PLUGIN_NODE", "")
	t.Setenv("FORMAE_NETWORK_COOKIE", "")

	_, err := readEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FORMAE_AGENT_NODE")
}

func TestReadEnv_DecodesBase64PluginConfig(t *testing.T) {
	t.Setenv("FORMAE_AGENT_NODE", "agent@localhost")
	t.Setenv("FORMAE_PLUGIN_NODE", "fai-plugin@localhost")
	t.Setenv("FORMAE_NETWORK_COOKIE", "test-cookie")
	t.Setenv("FORMAE_PLUGIN_CONFIG", "eyJmb28iOiJiYXIifQ==") // base64(`{"foo":"bar"}`)

	env, err := readEnv()
	require.NoError(t, err)
	require.NotNil(t, env.pluginConfig)
	assert.JSONEq(t, `{"foo":"bar"}`, string(env.pluginConfig))
}
