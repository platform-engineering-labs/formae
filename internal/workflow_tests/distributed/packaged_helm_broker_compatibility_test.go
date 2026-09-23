// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

type packagedBrokerWireCall struct {
	Target  gen.ProcessID
	Request any
	Reply   chan packagedReply
}
type packagedBrokerWireRequester struct{ act.Actor }

func (a *packagedBrokerWireRequester) HandleMessage(_ gen.PID, message any) error {
	call := message.(packagedBrokerWireCall)
	value, err := a.CallWithTimeout(call.Target, call.Request, 10)
	call.Reply <- packagedReply{Value: value, Err: err}
	return nil
}

func TestPackagedHelmBrokerWireCompatibility(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "new-receiver"
		if legacy {
			name = "legacy-receiver"
		}
		t.Run(name, func(t *testing.T) {
			legacyBinary := os.Getenv("FORMAE_PACKAGED_LEGACY_BROKER_BINARY")
			if legacy && legacyBinary == "" {
				t.Skip("set FORMAE_PACKAGED_LEGACY_BROKER_BINARY to a pre-bounded-request broker")
			}
			stage, _ := stagePackagedPlugins(t)
			brokerPath := filepath.Join(stage, "test-oidc-broker", "v0.0.1", "test-oidc-broker")
			if legacy {
				data, err := os.ReadFile(legacyBinary)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(brokerPath, data, 0755))
				t.Logf("legacy broker override: %s SHA256=%x", legacyBinary, sha256.Sum256(data))
			} else if oldSender := os.Getenv("FORMAE_PACKAGED_LEGACY_K8S_BINARY"); oldSender != "" {
				matches, err := filepath.Glob(filepath.Join(stage, "k8s", "v*", "k8s"))
				require.NoError(t, err)
				require.Len(t, matches, 1)
				data, err := os.ReadFile(oldSender)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(matches[0], data, 0755))
				t.Logf("legacy sender override: %s SHA256=%x", oldSender, sha256.Sum256(data))
			}
			f := newPackagedFixture(t, packagedKubeconfig(t))
			a := startPackagedAgent(t, stage, f, 30*time.Second)
			process := ownedPackagedPID(t, brokerPath)
			var target gen.ProcessID
			for _, node := range a.m.Node.Network().Nodes() {
				if strings.Contains(string(node), "-test-oidc-broker-oidccred@") {
					target = gen.ProcessID{Name: credential.ServerActorName, Node: node}
				}
			}
			require.NotEmpty(t, target.Node)
			connection, err := a.m.Node.Network().Node(target.Node)
			require.NoError(t, err)
			pid, err := a.m.Node.Spawn(func() gen.ProcessBehavior { return &packagedBrokerWireRequester{} }, gen.ProcessOptions{})
			require.NoError(t, err)
			call := func(req any) packagedReply {
				replies := make(chan packagedReply, 1)
				require.NoError(t, a.m.Node.Send(pid, packagedBrokerWireCall{Target: target, Request: req, Reply: replies}))
				select {
				case result := <-replies:
					return result
				case <-time.After(12 * time.Second):
					t.Fatal("wire compatibility call exceeded synchronous bound")
					return packagedReply{}
				}
			}
			request := credential.OidcIdentityTokenRequest{Audience: packagedAudience, RequestID: "wire-compatibility"}
			bounded := call(credential.OidcBoundedIdentityTokenRequest{Request: request})
			if legacy {
				require.ErrorIs(t, bounded.Err, gen.ErrTimeout)
				mints, _, _ := f.counts()
				require.Zero(t, mints, "unknown bounded envelope must never fallback to a legacy mint")
			} else {
				require.NoError(t, bounded.Err)
				require.NotNil(t, bounded.Value.(credential.IdentityTokenResponse).Result)
			}
			// An old wire sender remains usable through the exact same peer
			// connection and broker OS process after the bounded request.
			old := call(request)
			require.NoError(t, old.Err)
			require.NotNil(t, old.Value.(credential.IdentityTokenResponse).Result)
			afterConnection, err := a.m.Node.Network().Node(target.Node)
			require.NoError(t, err)
			require.Same(t, connection, afterConnection, "unknown type must not churn the existing connection")
			require.Equal(t, process.Pid, ownedPackagedPID(t, brokerPath).Pid)
			if legacy {
				before, _, _ := f.counts()
				// Exercise the actual new SDK sender too, through packaged Helm.
				result := a.call(t, gen.PID{}, "Create", packagedChartRequest(t, f, "wire-legacy-"+randomSuffix(), 300))
				require.Equal(t, resource.OperationStatusFailure, result.Value.(plugin.TrackedProgress).OperationStatus)
				after, _, _ := f.counts()
				require.Equal(t, before, after, "new SDK must not retry using the legacy envelope")
				assertPackagedReadOnly(t, f)
			} else {
				read := a.call(t, gen.PID{}, "Read", plugin.ReadResource{Namespace: "K8S", ResourceType: packagedHelmType, NativeID: "default/wire-absent-" + randomSuffix(), TargetConfig: f.target(), IsSync: true})
				require.Equal(t, resource.OperationStatusSuccess, read.Value.(plugin.TrackedProgress).OperationStatus)
			}
		})
	}
}
