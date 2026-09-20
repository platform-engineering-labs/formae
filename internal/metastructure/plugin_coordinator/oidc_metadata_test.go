// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_coordinator

import (
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestOidcBinding_TrustedLaunchAndIdentity(t *testing.T) {
	actor, sender := newCoordinatorForTest(t)
	supervisor := gen.PID{Node: actor.Node().Name(), ID: 101}
	cProcess := actor.Behavior().(*PluginCoordinator)
	cProcess.Process = oidcTestProcess{Process: cProcess.Process, node: oidcTestNode{Node: actor.Node(), supervisor: supervisor}}
	c := actor.Behavior().(*PluginCoordinator)
	cfg := model.RetryConfig{StatusCheckInterval: 7 * time.Second, RetryDelay: 11 * time.Second, MaxRetries: 3}
	registration := messages.RegisterOidcCredentialLaunch{Name: "fai", Version: "1.0.0", NodeName: sender.Node, SpawnToken: "first", ConfigIdentity: "canonical-config", Namespaces: []string{"K8S", "AWS"}}
	announce := func(token string) { actor.SendMessage(sender, oidcAnnouncement("fai", []string{"k8s", "AWS"}, token)) }
	binding := func() string {
		id, _ := c.pluginOperatorEnv(cfg, gen.PID{}, "K8S")["OidcOperationBindingID"].(string)
		return id
	}
	// A broker cannot promote its own announcement into trusted metadata.
	actor.SendMessage(sender, registration)
	announce("first")
	require.Empty(t, binding())
	actor.SendMessage(supervisor, registration)
	announce("first")
	first := binding()
	require.NotEmpty(t, first)
	announce("first")
	require.Equal(t, first, binding(), "duplicates and signing-key refresh do not change config")
	env := c.pluginOperatorEnv(cfg, gen.PID{}, "k8s")
	require.Equal(t, 7*time.Second, env["OidcOperationPollInterval"])
	require.Equal(t, 60*time.Second, env["OidcOperationCallTimeout"])
	require.Equal(t, 11*time.Second, env["OidcOperationRetryDelay"])
	require.Equal(t, 30*time.Second, env["OidcOperationThrottleMaxDelay"])
	require.NotEqual(t, first, c.pluginOperatorEnv(cfg, gen.PID{}, "AWS")["OidcOperationBindingID"])
	for key, value := range env {
		if key == "RetryConfig" || key == "RequestedBy" {
			continue
		}
		switch value.(type) {
		case string, time.Duration:
		default:
			t.Fatalf("new EDF env %s has unregistered type %T", key, value)
		}
	}
	registration.Namespaces = []string{"AWS", "K8S"}
	registration.SpawnToken = "restart"
	actor.SendMessage(supervisor, registration)
	announce("restart")
	require.Equal(t, first, binding(), "identical restart permits re-drive")
	actor.SendMessage(supervisor, messages.UnregisterOidcCredentialPlugin{Name: "fai", SpawnToken: "first"})
	require.Equal(t, first, binding())
	registration.ConfigIdentity = "changed-subject"
	registration.SpawnToken = "reconfigure"
	actor.SendMessage(supervisor, registration)
	announce("reconfigure")
	require.NotEqual(t, first, binding())
	changed := binding()
	registration.Version = "2.0.0"
	registration.SpawnToken = "upgrade"
	actor.SendMessage(supervisor, registration)
	// Version comes from supervisor discovery; announcement still says 1.0.0.
	announce("upgrade")
	require.NotEqual(t, changed, binding())
	require.NotEmpty(t, binding())
	actor.SendMessage(supervisor, messages.UnregisterOidcCredentialPlugin{Name: "fai", SpawnToken: "upgrade"})
	require.Empty(t, binding())
	announce("upgrade")
	require.Empty(t, binding(), "removed launch cannot re-establish metadata")
	actor.SendMessage(supervisor, messages.UnregisterOidcCredentialPlugin{Name: "fai", SpawnToken: "upgrade"})
	registration.Name = "replacement"
	registration.SpawnToken = "replacement"
	actor.SendMessage(supervisor, registration)
	actor.SendMessage(sender, oidcAnnouncement("replacement", []string{"K8S"}, "replacement"))
	require.NotEmpty(t, binding())
	require.NotEqual(t, first, binding(), "re-pairing changes binding")
}

func TestOidcBinding_AnnouncementMustMatchLaunch(t *testing.T) {
	for _, mismatch := range []string{"token", "node", "name", "namespace"} {
		t.Run(mismatch, func(t *testing.T) {
			actor, sender := newCoordinatorForTest(t)
			supervisor := gen.PID{Node: actor.Node().Name(), ID: 101}
			cProcess := actor.Behavior().(*PluginCoordinator)
			cProcess.Process = oidcTestProcess{Process: cProcess.Process, node: oidcTestNode{Node: actor.Node(), supervisor: supervisor}}
			reg := messages.RegisterOidcCredentialLaunch{Name: "fai", Version: "1", NodeName: sender.Node, SpawnToken: "launch", ConfigIdentity: "cfg", Namespaces: []string{"K8S"}}
			switch mismatch {
			case "token":
				reg.SpawnToken = "other"
			case "node":
				reg.NodeName = "other@localhost"
			case "name":
				reg.Name = "other"
			case "namespace":
				reg.Namespaces = []string{"AWS"}
			}
			actor.SendMessage(supervisor, reg)
			actor.SendMessage(sender, oidcAnnouncement("fai", []string{"k8s"}, "launch"))
			env := actor.Behavior().(*PluginCoordinator).pluginOperatorEnv(model.RetryConfig{}, gen.PID{}, "K8S")
			require.Contains(t, env, gen.Env("OidcCredentialBrokerNode"))
			require.NotContains(t, env, gen.Env("OidcOperationBindingID"))
		})
	}
}

// Ergo's unit node does not implement ProcessPID. Supply only that lookup;
// message delivery, actor initialization, env generation and logging stay real.
type oidcTestNode struct {
	gen.Node
	supervisor gen.PID
}

func (n oidcTestNode) ProcessPID(name gen.Atom) (gen.PID, error) {
	if name == "PluginProcessSupervisor" {
		return n.supervisor, nil
	}
	return gen.PID{}, gen.ErrProcessUnknown
}

type oidcTestProcess struct {
	gen.Process
	node gen.Node
}

func (p oidcTestProcess) Node() gen.Node { return p.node }
