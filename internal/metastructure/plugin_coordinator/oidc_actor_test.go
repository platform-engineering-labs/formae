// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_coordinator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/testplugin/fakeaws"
	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

type oidcCallbackResult struct {
	info      plugin.OidcOperationInfo
	available bool
	err       error
}
type oidcK8SPlugin struct {
	*fakeaws.FakeAWS
	callbacks chan oidcCallbackResult
}

func (p *oidcK8SPlugin) Namespace() string { return "K8S" }
func (p *oidcK8SPlugin) capture(ctx context.Context) {
	info, ok := plugin.OidcOperationMetadata(ctx)
	// Mint synchronously on the actual operation process through Ergo.
	_, err := plugin.NewOidcTokenSource().IdentityToken(ctx, "urn:formae:kubernetes:00000000-0000-4000-8000-000000000001")
	p.callbacks <- oidcCallbackResult{info, ok, err}
}
func (p *oidcK8SPlugin) Create(ctx context.Context, _ *resource.CreateRequest) (*resource.CreateResult, error) {
	p.capture(ctx)
	return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusInProgress, NativeID: "resource", RequestID: "request"}}, nil
}
func (p *oidcK8SPlugin) Status(ctx context.Context, _ *resource.StatusRequest) (*resource.StatusResult, error) {
	p.capture(ctx)
	return &resource.StatusResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: "resource", RequestID: "request"}}, nil
}

type oidcFixtureActor struct{ act.Actor }
type oidcFixtureOperation struct {
	pid     gen.PID
	request any
}

func (a *oidcFixtureActor) HandleMessage(_ gen.PID, _ any) error { return nil }

func (a *oidcFixtureActor) HandleCall(_ gen.PID, _ gen.Ref, request any) (any, error) {
	if operation, ok := request.(oidcFixtureOperation); ok {
		return a.Call(operation.pid, operation.request)
	}
	if _, ok := request.(credential.OidcBoundedIdentityTokenRequest); ok {
		return credential.IdentityTokenResponse{Result: &credential.OidcIdentityTokenResult{Token: "fixture-token", ExpiresAt: time.Now().Add(time.Minute)}}, nil
	}
	// This actor is registered as the real supervisor: coordinator provenance
	// validation uses Ergo's actual ProcessPID registry, not a unit stub.
	token := request.(string)
	if err := a.Send(actornames.PluginCoordinator, messages.RegisterOidcCredentialLaunch{Name: "fai", Version: "discovered-v1", NodeName: a.Node().Name(), SpawnToken: token, ConfigIdentity: "stable-config", Namespaces: []string{"K8S"}}); err != nil {
		return nil, err
	}
	if err := a.Send(actornames.PluginCoordinator, oidcAnnouncement("fai", []string{"K8S"}, token)); err != nil {
		return nil, err
	}
	return a.Call(actornames.PluginCoordinator, messages.SpawnPluginOperator{Namespace: "K8S", ResourceURI: "formae://resource/test", Operation: "create", OperationID: token})
}
func TestOidcRealActors_K8SCallbackAndRestartRedrive(t *testing.T) {
	p := &oidcK8SPlugin{FakeAWS: fakeaws.NewFakeAWS(), callbacks: make(chan oidcCallbackResult, 8)}
	cfg := model.RetryConfig{MaxRetries: 3, StatusCheckInterval: 10 * time.Millisecond, RetryDelay: 2 * time.Second}
	opts := gen.NodeOptions{Env: map[gen.Env]any{"RetryConfig": cfg, "TestResourcePlugin": plugin.FullResourcePlugin(p)}}
	opts.Network.Mode = gen.NetworkModeDisabled
	opts.Log.DefaultLogger.DisableBanner = true
	node, err := ergo.StartNode(gen.Atom("oidc-"+uuid.NewString()+"@localhost"), opts)
	require.NoError(t, err)
	defer node.Stop()
	_, err = node.SpawnRegister(gen.Atom(credential.ServerActorName), func() gen.ProcessBehavior { return &oidcFixtureActor{} }, gen.ProcessOptions{})
	require.NoError(t, err)
	_, err = node.SpawnRegister("PluginProcessSupervisor", func() gen.ProcessBehavior { return &oidcFixtureActor{} }, gen.ProcessOptions{})
	require.NoError(t, err)
	_, err = node.SpawnRegister(actornames.RateLimiter, func() gen.ProcessBehavior { return &oidcFixtureActor{} }, gen.ProcessOptions{})
	require.NoError(t, err)
	_, err = node.SpawnRegister(actornames.PluginCoordinator, NewPluginCoordinator, gen.ProcessOptions{})
	require.NoError(t, err)
	callback := func() plugin.OidcOperationInfo {
		t.Helper()
		select {
		case got := <-p.callbacks:
			require.NoError(t, got.err)
			require.True(t, got.available)
			return got.info
		case <-time.After(3 * time.Second):
			t.Fatal("callback did not arrive")
			return plugin.OidcOperationInfo{}
		}
	}
	spawn := func(token string) gen.PID {
		t.Helper()
		result, err := node.Call(gen.Atom("PluginProcessSupervisor"), token)
		require.NoError(t, err)
		spawned, ok := result.(messages.SpawnPluginOperatorResult)
		require.True(t, ok, fmt.Sprintf("unexpected %T", result))
		require.Empty(t, spawned.Error)
		return spawned.PID
	}
	pid := spawn("launch-1")
	_, err = node.Call(gen.Atom("PluginProcessSupervisor"), oidcFixtureOperation{pid, plugin.CreateResource{Namespace: "K8S", ResourceType: "K8S::Fixture"}})
	require.NoError(t, err)
	first := callback()
	require.NotEmpty(t, first.BindingID)
	require.Equal(t, 10*time.Millisecond, first.PollInterval)
	require.Equal(t, 60*time.Second, first.CallTimeout)
	require.Equal(t, 2*time.Second, first.RetryDelay)
	require.Equal(t, 30*time.Second, first.ThrottleMaxDelay)
	require.Equal(t, first, callback(), "status on same operator retains metadata")
	pid = spawn("launch-2")
	_, err = node.Call(gen.Atom("PluginProcessSupervisor"), oidcFixtureOperation{pid, plugin.ResumeWaitingForResource{Namespace: "K8S", ResourceOperation: resource.OperationCreate, PreviousAttempts: 1, Request: plugin.PluginOperatorCheckStatus{Namespace: "K8S", ResourceType: "K8S::Fixture", RequestID: "request", NativeID: "resource", ResourceOperation: resource.OperationCreate}}})
	require.NoError(t, err)
	require.Equal(t, first, callback(), "identical restart permits new operator to rejoin")
}
