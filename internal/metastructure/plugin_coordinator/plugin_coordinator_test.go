// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_coordinator

import (
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/testplugin/fakeaws"
	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginCoordinator_CaseInsensitiveNamespaceLookup(t *testing.T) {
	c := &PluginCoordinator{
		plugins: map[string]*RegisteredPlugin{
			"Azure": {
				Namespace:            "Azure",
				MaxRequestsPerSecond: 10,
			},
			"aws": {
				Namespace:            "aws",
				MaxRequestsPerSecond: 20,
			},
		},
	}

	plugin, ok := c.findPluginByNamespace("azure")
	require.True(t, ok)
	assert.Equal(t, "Azure", plugin.Namespace)

	plugin, ok = c.findPluginByNamespace("AZURE")
	require.True(t, ok)
	assert.Equal(t, "Azure", plugin.Namespace)

	plugin, ok = c.findPluginByNamespace("AWS")
	require.True(t, ok)
	assert.Equal(t, "aws", plugin.Namespace)

	plugin, ok = c.findPluginByNamespace("aws")
	require.True(t, ok)
	assert.Equal(t, "aws", plugin.Namespace)

	_, ok = c.findPluginByNamespace("NonExistent")
	assert.False(t, ok)
}

func TestPluginCoordinator_GetPluginInfo_CaseInsensitive(t *testing.T) {
	c := &PluginCoordinator{
		plugins: map[string]*RegisteredPlugin{
			"AZURE": {
				Namespace:            "AZURE",
				MaxRequestsPerSecond: 10,
				SupportedResources: []plugin.ResourceDescriptor{
					{Type: "AZURE::Compute::VirtualMachine", Discoverable: true},
				},
			},
		},
	}

	resp := c.getPluginInfo(messages.GetPluginInfo{Namespace: "azure"})
	assert.True(t, resp.Found)
	assert.Equal(t, "azure", resp.Namespace)
	assert.Len(t, resp.SupportedResources, 1)

	resp = c.getPluginInfo(messages.GetPluginInfo{Namespace: "AZURE"})
	assert.True(t, resp.Found)
	assert.Equal(t, "AZURE", resp.Namespace)

	resp = c.getPluginInfo(messages.GetPluginInfo{Namespace: "Azure"})
	assert.True(t, resp.Found)
	assert.Equal(t, "Azure", resp.Namespace)
}

// newCoordinatorForTest spawns a PluginCoordinator in the ergo unit-test harness.
// HandleMessage touches c.Log()/c.Send(), so it needs a live (test) process — a
// bare &PluginCoordinator{} would nil-panic. RetryConfig env is required by Init.
func newCoordinatorForTest(t *testing.T) (*unit.TestActor, gen.PID) {
	t.Helper()
	listener, err := unit.Spawn(t, NewPluginCoordinator, unit.WithEnv(map[gen.Env]any{
		gen.Env("RetryConfig"): model.RetryConfig{},
	}))
	require.NoError(t, err)
	return listener, gen.PID{Node: "test", ID: 100}
}

func announcement(namespace string, schemas map[string]model.Schema) messages.PluginAnnouncement {
	return messages.PluginAnnouncement{
		Name:                 namespace,
		Namespace:            namespace,
		Version:              "1.0.0",
		MaxRequestsPerSecond: 10,
		Capabilities: plugin.PluginCapabilities{
			ResourceSchemas: schemas,
		},
	}
}

// TestPluginCoordinator_RejectsBadFieldHintFormatWithoutCrashing verifies that a
// PluginAnnouncement carrying a schema with an unknown
// FieldHint.Format must be rejected (not registered) WITHOUT terminating the
// coordinator actor. A non-nil HandleMessage return would crash the actor,
// dropping every registered plugin — this asserts ShouldNotTerminate. (Against
// the buggy `return fmt.Errorf(...)` version the actor terminates, so this
// test fails — which is the point.)
func TestPluginCoordinator_RejectsBadFieldHintFormatWithoutCrashing(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	badSchema := map[string]model.Schema{
		"Grafana::Dashboard": {
			Identifier: "uid",
			Fields:     []string{"configJson"},
			Hints: map[string]model.FieldHint{
				"configJson": {Format: "jsn"}, // typo: unknown format
			},
		},
	}

	listener.SendMessage(sender, announcement("grafana", badSchema))

	// The actor must survive a bad-metadata announcement.
	require.False(t, listener.IsTerminated(),
		"coordinator must not terminate on a plugin's bad FieldHint.Format (reason: %v)", listener.TerminationReason())

	// And the offending plugin must NOT be registered.
	c := listener.Behavior().(*PluginCoordinator)
	_, ok := c.plugins["grafana"]
	assert.False(t, ok, "plugin with unknown FieldHint.Format must be rejected, not registered")
}

// TestPluginCoordinator_RegistersValidFieldHintFormat is the happy path: a known
// format (or no format) registers normally.
func TestPluginCoordinator_RegistersValidFieldHintFormat(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	goodSchema := map[string]model.Schema{
		"Grafana::Dashboard": {
			Identifier: "uid",
			Fields:     []string{"configJson"},
			Hints: map[string]model.FieldHint{
				"configJson": {Format: "json"}, // registered canonicalizer
			},
		},
	}

	listener.SendMessage(sender, announcement("grafana", goodSchema))

	require.False(t, listener.IsTerminated(),
		"coordinator must not terminate on a valid announcement (reason: %v)", listener.TerminationReason())

	c := listener.Behavior().(*PluginCoordinator)
	reg, ok := c.plugins["grafana"]
	require.True(t, ok, "plugin with valid FieldHint.Format must be registered")
	assert.Equal(t, "grafana", reg.Namespace)
}

// globalRetryConfig is the node-wide retry config the coordinator falls back to.
func globalRetryConfig() model.RetryConfig {
	return model.RetryConfig{StatusCheckInterval: 20 * time.Second, MaxRetries: 9, RetryDelay: 10 * time.Second}
}

// perPluginRetryConfig is a per-plugin retry override, differing from the global
// config in every term the operator's polling cadence is built from.
func perPluginRetryConfig() model.RetryConfig {
	return model.RetryConfig{StatusCheckInterval: 90 * time.Second, MaxRetries: 3, RetryDelay: 30 * time.Second}
}

func spawnPluginOperatorRequest(namespace string, requestedBy gen.PID) messages.SpawnPluginOperator {
	return messages.SpawnPluginOperator{
		Namespace:   namespace,
		ResourceURI: "formae://stack/default/" + namespace + "/resource",
		Operation:   "create",
		OperationID: "operation-1",
		RequestedBy: requestedBy,
	}
}

func spawnResult(t *testing.T, result *unit.CallResult) messages.SpawnPluginOperatorResult {
	t.Helper()
	require.NoError(t, result.Error)
	spawned, ok := result.Response.(messages.SpawnPluginOperatorResult)
	require.True(t, ok, "expected a SpawnPluginOperatorResult, got %T", result.Response)
	require.Empty(t, spawned.Error)
	return spawned
}

// TestPluginCoordinator_SpawnReportsResolvedRetryConfig asserts the spawn result
// reports the retry config the operator was spawned with — the per-plugin
// override where one is configured, the global config otherwise — so the
// requesting ResourceUpdater can size its watchdog from the operator's own
// cadence rather than the node-wide default.
func TestPluginCoordinator_SpawnReportsResolvedRetryConfig(t *testing.T) {
	perPlugin := perPluginRetryConfig()
	tests := []struct {
		name        string
		userConfigs []model.ResourcePluginUserConfig
		want        model.RetryConfig
	}{
		{
			name:        "per-plugin override",
			userConfigs: []model.ResourcePluginUserConfig{{Type: "grafana", Enabled: true, Retry: &perPlugin}},
			want:        perPlugin,
		},
		{
			name:        "no per-plugin override",
			userConfigs: nil,
			want:        globalRetryConfig(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := unit.Spawn(t, NewPluginCoordinator, unit.WithEnv(map[gen.Env]any{
				gen.Env("RetryConfig"):           globalRetryConfig(),
				gen.Env("ResourcePluginConfigs"): tc.userConfigs,
			}))
			require.NoError(t, err)
			sender := gen.PID{Node: "plugin-node", ID: 100}
			listener.SendMessage(sender, announcement("grafana", nil))

			spawned := spawnResult(t, listener.Call(sender, spawnPluginOperatorRequest("grafana", sender)))

			require.NotNil(t, spawned.RetryConfig, "the spawn result must report the config the operator was given")
			assert.Equal(t, tc.want, *spawned.RetryConfig)
		})
	}
}

// TestPluginCoordinator_LocalSpawnEnvMatchesReportedConfig asserts a spawned
// operator is handed the same retry config the spawn result reports, plus the
// plugin call deadline the agent sizes its watchdog window from. It drives the
// local path, which is the observable one; the remote path spawns with the same
// environment.
func TestPluginCoordinator_LocalSpawnEnvMatchesReportedConfig(t *testing.T) {
	listener, err := unit.Spawn(t, NewPluginCoordinator, unit.WithEnv(map[gen.Env]any{
		gen.Env("RetryConfig"):        globalRetryConfig(),
		gen.Env("TestResourcePlugin"): plugin.FullResourcePlugin(fakeaws.NewFakeAWS()),
	}))
	require.NoError(t, err)
	sender := gen.PID{Node: "test", ID: 100}

	spawned := spawnResult(t, listener.Call(sender, spawnPluginOperatorRequest("FakeAWS", sender)))

	require.NotNil(t, spawned.RetryConfig)
	assert.Equal(t, globalRetryConfig(), *spawned.RetryConfig)

	var env map[gen.Env]any
	for _, event := range listener.Events() {
		if spawnEvent, ok := event.(unit.SpawnEvent); ok {
			env = spawnEvent.Options.Env
		}
	}
	require.NotNil(t, env, "the coordinator must have spawned a plugin operator")
	assert.Equal(t, *spawned.RetryConfig, env[gen.Env("RetryConfig")],
		"the operator must poll on the config the result reports")
	assert.Equal(t, resource_update.PluginCallTimeout, env[gen.Env("PluginCallTimeout")],
		"the operator must bound its plugin calls by the deadline the watchdog window is built from")
}

// oidcAnnouncement builds an OidcCredentialPluginAnnouncement for the given
// broker name/namespaces/spawn token, mirroring what a broker sends the
// coordinator on startup.
func oidcAnnouncement(name string, namespaces []string, spawnToken string) messages.OidcCredentialPluginAnnouncement {
	return messages.OidcCredentialPluginAnnouncement{
		Name:       name,
		Version:    "1.0.0",
		Namespaces: namespaces,
		SpawnToken: spawnToken,
	}
}

// TestPluginCoordinator_OidcBroker_PairsEveryNamespaceUppercased asserts that
// announcing multiple namespaces registers one entry per namespace, keyed by
// the uppercased namespace regardless of the casing the broker announced.
func TestPluginCoordinator_OidcBroker_PairsEveryNamespaceUppercased(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws", "Gcp"}, "tok-1"))

	c := listener.Behavior().(*PluginCoordinator)
	require.Len(t, c.oidcCredentialBrokers, 2)

	for _, key := range []string{"AWS", "GCP"} {
		broker, ok := c.oidcCredentialBrokers[key]
		require.True(t, ok, "expected an entry for namespace %s", key)
		assert.Equal(t, "fai", broker.Name)
		assert.Equal(t, sender.Node, broker.NodeName)
		assert.Equal(t, "tok-1", broker.SpawnToken)
	}
}

// TestPluginCoordinator_OidcBroker_SameBrokerSupersedes asserts that a second
// announcement from the same broker Name always overwrites the prior
// registration for a namespace it already serves, regardless of the tokens
// involved (e.g. a restarted broker announcing a fresh spawn token).
func TestPluginCoordinator_OidcBroker_SameBrokerSupersedes(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws"}, "tok-1"))
	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws"}, "tok-2"))

	c := listener.Behavior().(*PluginCoordinator)
	broker, ok := c.oidcCredentialBrokers["AWS"]
	require.True(t, ok)
	assert.Equal(t, "fai", broker.Name)
	assert.Equal(t, "tok-2", broker.SpawnToken, "the later announcement must supersede the earlier one")
}

// TestPluginCoordinator_OidcBroker_DifferentBrokerRejected asserts that a
// different broker Name announcing a namespace already served by another
// broker is rejected: the first registration stands untouched.
func TestPluginCoordinator_OidcBroker_DifferentBrokerRejected(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws"}, "tok-1"))
	listener.SendMessage(sender, oidcAnnouncement("other", []string{"aws"}, "tok-2"))

	require.False(t, listener.IsTerminated(),
		"coordinator must not terminate on a conflicting broker announcement (reason: %v)", listener.TerminationReason())

	c := listener.Behavior().(*PluginCoordinator)
	broker, ok := c.oidcCredentialBrokers["AWS"]
	require.True(t, ok)
	assert.Equal(t, "fai", broker.Name, "the first broker to serve the namespace must stand")
	assert.Equal(t, "tok-1", broker.SpawnToken)
}

// TestPluginCoordinator_OidcBroker_EqualTokenDeletes asserts that an
// unregister message carrying the exact SpawnToken the entry was registered
// with removes that namespace's entry.
func TestPluginCoordinator_OidcBroker_EqualTokenDeletes(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws"}, "tok-1"))
	listener.SendMessage(sender, messages.UnregisterOidcCredentialPlugin{
		Name:       "fai",
		SpawnToken: "tok-1",
		Reason:     "shutdown",
	})

	c := listener.Behavior().(*PluginCoordinator)
	_, ok := c.oidcCredentialBrokers["AWS"]
	assert.False(t, ok, "an unregister with the matching spawn token must delete the entry")
}

// TestPluginCoordinator_OidcBroker_SupersededTokenNoop asserts that an
// unregister carrying a stale (superseded) SpawnToken is a no-op: the current
// registration, made under a newer token, is left in place.
func TestPluginCoordinator_OidcBroker_SupersededTokenNoop(t *testing.T) {
	listener, sender := newCoordinatorForTest(t)

	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws"}, "tok-1"))
	listener.SendMessage(sender, oidcAnnouncement("fai", []string{"aws"}, "tok-2"))
	listener.SendMessage(sender, messages.UnregisterOidcCredentialPlugin{
		Name:       "fai",
		SpawnToken: "tok-1", // stale: superseded by tok-2 above
		Reason:     "crashed",
	})

	c := listener.Behavior().(*PluginCoordinator)
	broker, ok := c.oidcCredentialBrokers["AWS"]
	require.True(t, ok, "a stale-token unregister must not remove the current registration")
	assert.Equal(t, "tok-2", broker.SpawnToken)
}

// TestPluginCoordinator_OidcBroker_OperatorEnvCarriesPairAtomically asserts
// that spawning a PluginOperator for a namespace with a registered broker
// injects both OidcCredentialBrokerNode and OidcCredentialBrokerName into the
// spawn environment, and that a namespace with no registered broker gets
// neither key - never just one.
func TestPluginCoordinator_OidcBroker_OperatorEnvCarriesPairAtomically(t *testing.T) {
	listener, err := unit.Spawn(t, NewPluginCoordinator, unit.WithEnv(map[gen.Env]any{
		gen.Env("RetryConfig"):        globalRetryConfig(),
		gen.Env("TestResourcePlugin"): plugin.FullResourcePlugin(fakeaws.NewFakeAWS()),
	}))
	require.NoError(t, err)
	sender := gen.PID{Node: "test", ID: 100}
	brokerNode := gen.PID{Node: "fai-oidccred@localhost", ID: 1}

	listener.SendMessage(brokerNode, oidcAnnouncement("fai", []string{"FakeAWS"}, "tok-1"))

	spawnResult(t, listener.Call(sender, spawnPluginOperatorRequest("FakeAWS", sender)))

	var env map[gen.Env]any
	for _, event := range listener.Events() {
		if spawnEvent, ok := event.(unit.SpawnEvent); ok {
			env = spawnEvent.Options.Env
		}
	}
	require.NotNil(t, env, "the coordinator must have spawned a plugin operator")

	node, hasNode := env[gen.Env("OidcCredentialBrokerNode")]
	name, hasName := env[gen.Env("OidcCredentialBrokerName")]
	require.True(t, hasNode, "a namespace with a registered broker must carry OidcCredentialBrokerNode")
	require.True(t, hasName, "a namespace with a registered broker must carry OidcCredentialBrokerName")
	assert.Equal(t, string(brokerNode.Node), node)
	assert.Equal(t, credential.ServerActorName, name)
}

// TestPluginCoordinator_OidcBroker_OperatorEnvOmitsBothWhenUnpaired asserts
// that a namespace with no registered broker carries neither
// OidcCredentialBrokerNode nor OidcCredentialBrokerName.
func TestPluginCoordinator_OidcBroker_OperatorEnvOmitsBothWhenUnpaired(t *testing.T) {
	listener, err := unit.Spawn(t, NewPluginCoordinator, unit.WithEnv(map[gen.Env]any{
		gen.Env("RetryConfig"):        globalRetryConfig(),
		gen.Env("TestResourcePlugin"): plugin.FullResourcePlugin(fakeaws.NewFakeAWS()),
	}))
	require.NoError(t, err)
	sender := gen.PID{Node: "test", ID: 100}

	spawnResult(t, listener.Call(sender, spawnPluginOperatorRequest("FakeAWS", sender)))

	var env map[gen.Env]any
	for _, event := range listener.Events() {
		if spawnEvent, ok := event.(unit.SpawnEvent); ok {
			env = spawnEvent.Options.Env
		}
	}
	require.NotNil(t, env, "the coordinator must have spawned a plugin operator")

	_, hasNode := env[gen.Env("OidcCredentialBrokerNode")]
	_, hasName := env[gen.Env("OidcCredentialBrokerName")]
	assert.False(t, hasNode)
	assert.False(t, hasName)
}
