// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_process_supervisor

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Executed in the actual meta.Port child. Observe the config bytes at the same
// boundary credential.readEnv consumes, with its empty-env / base64 semantics.
func TestOidcChildConfigHelper(t *testing.T) {
	output := os.Getenv("FORMAE_TEST_OIDC_CHILD_OUTPUT")
	if output == "" {
		return
	}
	var raw []byte
	if encoded := os.Getenv("FORMAE_PLUGIN_CONFIG"); encoded != "" {
		var err error
		raw, err = base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
	}
	require.NoError(t, os.WriteFile(output, raw, 0600))
}

type oidcConfigPortActor struct {
	act.Actor
	env        map[gen.Env]string
	executable string
	terminated chan struct{}
}

// Start after initialization so the child cannot send port messages while Ergo
// is still publishing the parent actor state.
func (a *oidcConfigPortActor) HandleCall(_ gen.PID, _ gen.Ref, _ any) (any, error) {
	port, err := meta.CreatePort(meta.PortOptions{Cmd: a.executable, Args: []string{"-test.run=^TestOidcChildConfigHelper$"}, EnableEnvOS: true, Env: a.env})
	if err != nil {
		return nil, err
	}
	_, err = a.SpawnMeta(port, gen.MetaOptions{})
	return true, err
}
func (a *oidcConfigPortActor) HandleMessage(_ gen.PID, message any) error {
	if _, ok := message.(meta.MessagePortTerminate); ok {
		close(a.terminated)
	}
	return nil
}

func TestOidcChildConfig_ExplicitPayloadOverridesInheritedIdentity(t *testing.T) {
	t.Setenv("FORMAE_PLUGIN_CONFIG", base64.StdEncoding.EncodeToString([]byte(`{"subject":"inherited-identity"}`)))
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, configured := range []json.RawMessage{nil, json.RawMessage(`{"subject":"configured-identity"}`)} {
		name := "absent"
		if configured != nil {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "consumed-config")
			env := buildOidcCredentialEnv(serverCfgFixture(), "fai", "launch", configured)
			env["FORMAE_TEST_OIDC_CHILD_OUTPUT"] = output
			opts := gen.NodeOptions{}
			opts.Network.Mode = gen.NetworkModeDisabled
			opts.Log.DefaultLogger.DisableBanner = true
			node, err := ergo.StartNode(gen.Atom("oidc-env-"+uuid.NewString()+"@localhost"), opts)
			require.NoError(t, err)
			defer node.Stop()
			terminated := make(chan struct{})
			pid, err := node.Spawn(func() gen.ProcessBehavior {
				return &oidcConfigPortActor{env: env, executable: executable, terminated: terminated}
			}, gen.ProcessOptions{})
			require.NoError(t, err)
			_, err = node.Call(pid, nil)
			require.NoError(t, err)
			select {
			case <-terminated:
			case <-time.After(5 * time.Second):
				t.Fatal("config observation child did not terminate")
			}
			consumed, err := os.ReadFile(output)
			require.NoError(t, err)
			require.Equal(t, string(configured), string(consumed), "child must consume precisely the configured payload, never inherited identity")
			trusted, err := oidcConfigIdentity(configured)
			require.NoError(t, err)
			effective, err := oidcConfigIdentity(consumed)
			require.NoError(t, err)
			require.Equal(t, trusted, effective, "trusted digest must describe actual child configuration")
		})
	}
}
