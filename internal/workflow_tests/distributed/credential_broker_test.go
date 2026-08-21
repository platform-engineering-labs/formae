// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/registrar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/testplugin/fakeaws"
	"github.com/platform-engineering-labs/formae/pkg/credential"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// The audience the resource plugin asks for, and the prefix the in-test
// broker builds its answer from. The broker returns prefix+audience, so the
// assertion proves the token the plugin received is the one this broker
// minted for the audience the plugin asked about.
const (
	oidcTestAudience    = "sts.amazonaws.com"
	oidcTestTokenPrefix = "wf-test-jwt."
)

// brokerNamespace is the namespace the in-test broker announces. It matches
// FakeAWS's namespace, so the coordinator pairs the broker with the operator
// serving the applied resource.
const brokerNamespace = "FakeAWS"

// cannedBroker is the OidcCredentialPlugin the in-test broker serves: it mints
// a deterministic, unsigned token derived from the requested audience.
type cannedBroker struct{}

func (cannedBroker) IdentityToken(_ context.Context, req *credential.OidcIdentityTokenRequest) (*credential.OidcIdentityTokenResult, error) {
	return &credential.OidcIdentityTokenResult{
		Token:     oidcTestTokenPrefix + req.Audience,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

// oidcRecordingPlugin is a resource plugin that asks its OIDC token source for
// a token on every Create and records what came back, so the test can assert
// on a value produced inside an asynchronous apply.
//
// It exists instead of teaching FakeAWS about OidcAware because the in-process
// test-plugin injection path (Metastructure.TestResourcePlugin) hands the
// plugin straight to the coordinator's local spawn and never runs SDK setup,
// so an OidcAware FakeAWS would be left with a nil token source.
type oidcRecordingPlugin struct {
	*fakeaws.FakeAWS

	source plugin.OidcTokenSource

	mu     sync.Mutex
	called bool
	token  string
	err    error
}

var (
	_ plugin.FullResourcePlugin = (*oidcRecordingPlugin)(nil)
	_ plugin.OidcAware          = (*oidcRecordingPlugin)(nil)
)

func newOidcRecordingPlugin() *oidcRecordingPlugin {
	p := &oidcRecordingPlugin{FakeAWS: fakeaws.NewFakeAWS()}

	// In production the SDK installs the context-resolving token source on
	// every OidcAware plugin during setup (pkg/plugin/sdk.configureWrapped).
	// The injection path this test uses bypasses SDK setup entirely, so the
	// test performs that same installation here.
	p.SetOidcTokenSource(plugin.NewOidcTokenSource())

	return p
}

func (p *oidcRecordingPlugin) SetOidcTokenSource(src plugin.OidcTokenSource) {
	p.source = src
}

func (p *oidcRecordingPlugin) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	token, err := p.source.IdentityToken(ctx, oidcTestAudience)

	p.mu.Lock()
	p.called, p.token, p.err = true, token, err
	p.mu.Unlock()

	return p.FakeAWS.Create(ctx, request)
}

// recorded reports whether Create ran and what its token request produced.
func (p *oidcRecordingPlugin) recorded() (bool, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.called, p.token, p.err
}

// TestOidcCredentialBroker_AnnouncementAndTokenOverTheWire drives the whole
// oidc-credential chain over a real Ergo network connection: a second node
// announces itself as a broker to the running agent's PluginCoordinator, the
// coordinator pairs that broker with the resource plugin's namespace and
// injects the pairing into the PluginOperator's environment, the operator puts
// a broker client on the operation context, and the SDK token source the
// plugin holds resolves that client and calls the broker for a token.
//
// What it pins: the announcement crossing a real network connection and
// round-tripping the EDF codec, and the pairing -> operator environment -> SDK
// context source path delivering the broker's token to a plugin mid-apply.
//
// What it deliberately does NOT pin: that each peer registers the EDF types
// independently. credential.RegisterEDFTypes writes a process-global registry,
// and both nodes here live in one process, so they share a single
// registration. That per-peer property belongs to the hermetic e2e suite,
// where the broker is a separate OS process.
func TestOidcCredentialBroker_AnnouncementAndTokenOverTheWire(t *testing.T) {
	cfg := newNetworkedTestConfig(t)
	defer cleanupTestDatabase(t, cfg)

	logCapture := setupTestLogger()

	testPlugin := newOidcRecordingPlugin()
	m := newMetastructureWithTestPlugin(t, cfg, testPlugin)
	defer m.Stop(true)

	brokerNode := startTestBrokerNode(t, cfg)
	defer brokerNode.Stop()

	announceBroker(t, brokerNode, cfg)

	require.True(t,
		logCapture.WaitForLog("Oidc credential broker registered", 10*time.Second),
		"the coordinator should register the broker announced over the network")

	applyBucketForma(t, m)
	awaitCommandSuccess(t, m)

	called, token, err := testPlugin.recorded()
	require.True(t, called, "the plugin's Create should have run")
	require.NoError(t, err, "the plugin should have received a token from the paired broker")
	assert.Equal(t, oidcTestTokenPrefix+oidcTestAudience, token)
}

// TestOidcCredentialBroker_NoBrokerAnnounced runs the same apply against a
// fresh agent that no broker has ever announced to. The plugin's token source
// finds no broker client on the operation context and fails with
// ErrNoOidcBroker, which the plugin records: ApplyForma is asynchronous, so
// the recorded value - not ApplyForma's return - is where the outcome shows up.
func TestOidcCredentialBroker_NoBrokerAnnounced(t *testing.T) {
	cfg := newNetworkedTestConfig(t)
	defer cleanupTestDatabase(t, cfg)

	setupTestLogger()

	testPlugin := newOidcRecordingPlugin()
	m := newMetastructureWithTestPlugin(t, cfg, testPlugin)
	defer m.Stop(true)

	applyBucketForma(t, m)
	awaitCommandSuccess(t, m)

	called, token, err := testPlugin.recorded()
	require.True(t, called, "the plugin's Create should have run")
	assert.Empty(t, token)
	assert.ErrorIs(t, err, plugin.ErrNoOidcBroker)
}

// newNetworkedTestConfig builds a distributed test config whose agent actually
// listens on the network: the metastructure only creates a registrar and an
// acceptor when ErgoPort is nonzero, and without those the broker node has no
// way to reach the agent. Ports are picked free at runtime so concurrent test
// binaries do not collide.
func newNetworkedTestConfig(t *testing.T) *pkgmodel.Config {
	t.Helper()

	cfg := newDistributedTestConfig(t)
	cfg.Agent.Server.Nodename = "oidc-agent-" + util.RandomString(10)
	cfg.Agent.Server.ErgoPort = pickFreePort(t)
	cfg.Agent.Server.RegistrarPort = pickFreePort(t)

	return cfg
}

// newMetastructureWithTestPlugin starts an agent serving the given in-process
// resource plugin and no external plugins.
func newMetastructureWithTestPlugin(t *testing.T, cfg *pkgmodel.Config, p plugin.FullResourcePlugin) *metastructure.Metastructure {
	t.Helper()

	db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	require.NoError(t, err, "failed to create datastore")

	m, err := metastructure.NewMetastructureWithDataStoreAndContext(
		context.Background(), cfg, nil, nil, db, "test",
	)
	require.NoError(t, err, "failed to create metastructure")

	m.TestResourcePlugin = p
	require.NoError(t, m.Start(), "failed to start metastructure")

	return m
}

// startTestBrokerNode brings up a second Ergo node serving credential's
// CredentialActor, mirroring the node options credential.Run's startNodeStep
// builds for a broker subprocess: network mode on, the agent's cookie, an
// auto-selected acceptor port, and the agent's registrar so the two nodes can
// resolve each other by name.
func startTestBrokerNode(t *testing.T, cfg *pkgmodel.Config) gen.Node {
	t.Helper()

	require.NoError(t, credential.RegisterEDFTypes(), "failed to register credential EDF types")

	options := gen.NodeOptions{}
	options.Network.Mode = gen.NetworkModeEnabled
	options.Network.Cookie = cfg.Agent.Server.Secret
	options.Network.Acceptors = []gen.AcceptorOptions{{Host: "localhost", Port: 0}}
	options.Network.Registrar = registrar.Create(registrar.Options{
		Port: uint16(cfg.Agent.Server.RegistrarPort),
	})
	options.Security.ExposeEnvRemoteSpawn = true
	options.Log.DefaultLogger.Disable = true
	options.Log.DefaultLogger.DisableBanner = true
	options.Env = map[gen.Env]any{gen.Env("Plugin"): cannedBroker{}}

	nodeName := cfg.Agent.Server.Nodename + "-oidccred@" + cfg.Agent.Server.Hostname
	node, err := ergo.StartNode(gen.Atom(nodeName), options)
	require.NoError(t, err, "failed to start the broker node")

	_, err = node.SpawnRegister(
		gen.Atom(credential.ServerActorName),
		func() gen.ProcessBehavior { return &credential.CredentialActor{} },
		gen.ProcessOptions{},
	)
	require.NoError(t, err, "failed to spawn the credential serving actor")

	return node
}

// announceBroker sends the real announcement message from the broker node to
// the agent's PluginCoordinator, over the network connection between them.
func announceBroker(t *testing.T, node gen.Node, cfg *pkgmodel.Config) {
	t.Helper()

	agentNode := cfg.Agent.Server.Nodename + "@" + cfg.Agent.Server.Hostname
	target := gen.ProcessID{Name: actornames.PluginCoordinator, Node: gen.Atom(agentNode)}

	err := node.Send(target, credential.OidcCredentialPluginAnnouncement{
		Name:       "wf-test-broker",
		Version:    "0.0.1",
		Namespaces: []string{brokerNamespace},
		SpawnToken: util.RandomString(16),
	})
	require.NoError(t, err, "failed to announce the broker to the PluginCoordinator")
}

// applyBucketForma submits a one-resource apply against the in-process plugin.
func applyBucketForma(t *testing.T, m *metastructure.Metastructure) {
	t.Helper()

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "oidc-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "oidc-bucket",
				Type:       "FakeAWS::S3::Bucket",
				Stack:      "oidc-stack",
				Target:     "oidc-target",
				Properties: json.RawMessage(`{"BucketName":"oidc-test-bucket"}`),
				Managed:    true,
			},
		},
		Targets: []pkgmodel.Target{{Label: "oidc-target", Namespace: brokerNamespace}},
	}

	_, err := m.ApplyForma(
		forma,
		&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
		"test-client-id", "", "",
	)
	require.NoError(t, err, "ApplyForma should not return an error")
}

// awaitCommandSuccess blocks until the single submitted command settles
// successfully, which is what makes the plugin's recorded token safe to read.
func awaitCommandSuccess(t *testing.T, m *metastructure.Metastructure) {
	t.Helper()

	require.Eventually(t, func() bool {
		commands, err := m.Datastore.LoadFormaCommands()
		if err != nil {
			return false
		}
		return len(commands) == 1 && commands[0].State == forma_command.CommandStateSuccess
	}, 15*time.Second, 100*time.Millisecond, "the apply command should complete successfully")
}

// pickFreePort asks the OS for a free TCP port by listening on :0 and handing
// back the port it was given.
func pickFreePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err, "failed to pick a free port")
	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port
}
