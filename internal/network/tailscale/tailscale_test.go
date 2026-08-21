// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authKey and otherAuthKey stand in for real tailnet auth keys. No error
// message, log line or assertion may ever carry one of these values.
const (
	authKey      = "tskey-auth-first-secret"
	otherAuthKey = "tskey-auth-second-secret"
)

// listenCall records one Listen/ListenTLS call made on a fake node.
type listenCall struct {
	tls     bool
	network string
	addr    string
}

// dialCall records one Dial call made on a fake node.
type dialCall struct {
	network string
	addr    string
}

// fakeNode stands in for the tsnet-backed node so tests never join a tailnet.
type fakeNode struct {
	cfg        Config
	httpClient *http.Client

	// startErr, acceptRoutes and dialTo drive the egress paths. The zero
	// values give a node that starts, accepts routes, and cannot dial.
	startErr     error
	acceptRoutes func(ctx context.Context) error
	dialTo       string

	mu          sync.Mutex
	listenCalls []listenCall
	dialCalls   []dialCall
	startCalls  int
	routeCalls  int
	closeCalls  int
}

func (n *fakeNode) Listen(network, addr string) (net.Listener, error) {
	n.record(listenCall{tls: false, network: network, addr: addr})
	return newStubListener(), nil
}

func (n *fakeNode) ListenTLS(network, addr string) (net.Listener, error) {
	n.record(listenCall{tls: true, network: network, addr: addr})
	return newStubListener(), nil
}

func (n *fakeNode) HTTPClient() *http.Client { return n.httpClient }

// Dial records the call and serves it from the address the node was built
// with, so a tunnel through the proxy stays inside the test process.
func (n *fakeNode) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	n.mu.Lock()
	n.dialCalls = append(n.dialCalls, dialCall{network: network, addr: addr})
	dialTo := n.dialTo
	n.mu.Unlock()

	if dialTo == "" {
		return nil, fmt.Errorf("fake node has no destination for %s", addr)
	}

	var dialer net.Dialer

	return dialer.DialContext(ctx, "tcp", dialTo)
}

func (n *fakeNode) Start() error {
	n.mu.Lock()
	n.startCalls++
	n.mu.Unlock()

	return n.startErr
}

func (n *fakeNode) AcceptRoutes(ctx context.Context) error {
	n.mu.Lock()
	n.routeCalls++
	acceptRoutes := n.acceptRoutes
	n.mu.Unlock()

	if acceptRoutes == nil {
		return nil
	}

	return acceptRoutes(ctx)
}

func (n *fakeNode) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeCalls++

	return nil
}

func (n *fakeNode) record(call listenCall) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.listenCalls = append(n.listenCalls, call)
}

func (n *fakeNode) calls() []listenCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]listenCall(nil), n.listenCalls...)
}

func (n *fakeNode) dials() []dialCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]dialCall(nil), n.dialCalls...)
}

func (n *fakeNode) starts() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.startCalls
}

func (n *fakeNode) routes() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.routeCalls
}

func (n *fakeNode) closes() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closeCalls
}

// stubListener satisfies net.Listener without binding anything: Accept blocks
// until Close, the way a real listener's would.
type stubListener struct {
	closed chan struct{}
	once   sync.Once
}

func newStubListener() *stubListener {
	return &stubListener{closed: make(chan struct{})}
}

func (l *stubListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *stubListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *stubListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// nodeFactory hands out fake nodes and records how many were constructed. The
// behaviour fields are handed to every node it builds.
type nodeFactory struct {
	startErr     error
	acceptRoutes func(ctx context.Context) error
	dialTo       string

	mu    sync.Mutex
	nodes []*fakeNode
}

func (f *nodeFactory) newNode(cfg *Config) node {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := &fakeNode{
		cfg:          *cfg,
		httpClient:   &http.Client{},
		startErr:     f.startErr,
		acceptRoutes: f.acceptRoutes,
		dialTo:       f.dialTo,
	}
	f.nodes = append(f.nodes, n)

	return n
}

func (f *nodeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.nodes)
}

func (f *nodeFactory) only(t *testing.T) *fakeNode {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.nodes, 1)

	return f.nodes[0]
}

// newTestPlugin returns a plugin wired to a fresh fake node factory.
func newTestPlugin() (*Tailscale, *nodeFactory) {
	factory := &nodeFactory{}
	return &Tailscale{newNode: factory.newNode}, factory
}

func configJSON(t *testing.T, cfg Config) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	return raw
}

func TestName(t *testing.T) {
	plugin, _ := newTestPlugin()

	assert.Equal(t, "tailscale", plugin.Name())
}

func TestClientAndListenShareOneNode(t *testing.T) {
	plugin, factory := newTestPlugin()
	config := configJSON(t, Config{AuthKey: authKey, Hostname: "agent"})

	client, err := plugin.Client(config)
	require.NoError(t, err)

	ln, err := plugin.Listen(config, 8080)
	require.NoError(t, err)
	require.NotNil(t, ln)

	assert.Equal(t, 1, factory.count())
	assert.Same(t, factory.only(t).httpClient, client)
}

func TestConcurrentFirstCallsConstructOneNode(t *testing.T) {
	plugin, factory := newTestPlugin()
	config := configJSON(t, Config{AuthKey: authKey, Hostname: "agent"})

	const callers = 16

	var wg sync.WaitGroup
	wg.Add(callers)

	for i := range callers {
		go func() {
			defer wg.Done()

			var err error
			if i%2 == 0 {
				_, err = plugin.Client(config)
			} else {
				_, err = plugin.Listen(config, 8080)
			}
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, factory.count())
}

func TestSecondCallWithDifferentHostnameErrors(t *testing.T) {
	plugin, factory := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "agent"}))
	require.NoError(t, err)

	_, err = plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "cli"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hostname")
	assert.NotContains(t, err.Error(), "advertiseTags")
	assert.NotContains(t, err.Error(), "agent")
	assert.NotContains(t, err.Error(), "cli")
	assert.Equal(t, 1, factory.count())
}

func TestSecondCallWithDifferentAdvertiseTagsErrors(t *testing.T) {
	plugin, factory := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "agent", AdvertiseTags: []string{"tag:agent"}}))
	require.NoError(t, err)

	_, err = plugin.Listen(configJSON(t, Config{AuthKey: authKey, Hostname: "agent", AdvertiseTags: []string{"tag:cli"}}), 8080)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "advertiseTags")
	assert.NotContains(t, err.Error(), "hostname")
	assert.NotContains(t, err.Error(), "tag:agent")
	assert.NotContains(t, err.Error(), "tag:cli")
	assert.Equal(t, 1, factory.count())
}

func TestSecondCallWithDifferentHostnameAndTagsNamesBothFields(t *testing.T) {
	plugin, _ := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "agent", AdvertiseTags: []string{"tag:agent"}}))
	require.NoError(t, err)

	_, err = plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "cli", AdvertiseTags: []string{"tag:cli"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hostname")
	assert.Contains(t, err.Error(), "advertiseTags")
}

func TestSecondCallWithDifferentAuthKeyReusesNode(t *testing.T) {
	plugin, factory := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "agent"}))
	require.NoError(t, err)

	_, err = plugin.Listen(configJSON(t, Config{AuthKey: otherAuthKey, Hostname: "agent"}), 8080)
	require.NoError(t, err)

	assert.Equal(t, 1, factory.count())
	assert.Equal(t, authKey, factory.only(t).cfg.AuthKey)
}

func TestMismatchErrorsNeverCarryAuthKeys(t *testing.T) {
	plugin, _ := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey, Hostname: "agent", AdvertiseTags: []string{"tag:agent"}}))
	require.NoError(t, err)

	// Every mismatch shape, each with a second auth key in play.
	mismatches := []Config{
		{AuthKey: otherAuthKey, Hostname: "cli", AdvertiseTags: []string{"tag:agent"}},
		{AuthKey: otherAuthKey, Hostname: "agent", AdvertiseTags: []string{"tag:cli"}},
		{AuthKey: otherAuthKey, Hostname: "cli", AdvertiseTags: []string{"tag:cli"}},
	}

	for _, cfg := range mismatches {
		_, clientErr := plugin.Client(configJSON(t, cfg))
		require.Error(t, clientErr)

		_, listenErr := plugin.Listen(configJSON(t, cfg), 8080)
		require.Error(t, listenErr)

		for _, err := range []error{clientErr, listenErr} {
			assert.NotContains(t, err.Error(), authKey)
			assert.NotContains(t, err.Error(), otherAuthKey)
			assert.NotContains(t, err.Error(), "tskey")
		}
	}
}

func TestAdvertiseTagOrderIsNotAMismatch(t *testing.T) {
	plugin, factory := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey, AdvertiseTags: []string{"tag:agent", "tag:cli"}}))
	require.NoError(t, err)

	_, err = plugin.Client(configJSON(t, Config{AuthKey: authKey, AdvertiseTags: []string{"tag:cli", "tag:agent"}}))
	require.NoError(t, err)

	assert.Equal(t, 1, factory.count())
}

func TestHostnameDefaultsToFormae(t *testing.T) {
	plugin, factory := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{AuthKey: authKey}))
	require.NoError(t, err)

	assert.Equal(t, "formae", factory.only(t).cfg.Hostname)

	// The default and the explicit spelling describe the same node.
	_, err = plugin.Listen(configJSON(t, Config{AuthKey: authKey, Hostname: "formae"}), 8080)
	require.NoError(t, err)

	assert.Equal(t, 1, factory.count())
}

func TestNodeIsBuiltFromConfig(t *testing.T) {
	plugin, factory := newTestPlugin()

	_, err := plugin.Client(configJSON(t, Config{
		AuthKey:       authKey,
		Hostname:      "agent",
		AdvertiseTags: []string{"tag:agent"},
	}))
	require.NoError(t, err)

	got := factory.only(t).cfg
	assert.Equal(t, "agent", got.Hostname)
	assert.Equal(t, authKey, got.AuthKey)
	assert.Equal(t, []string{"tag:agent"}, got.AdvertiseTags)
}

func TestListenDelegatesByTLS(t *testing.T) {
	tests := []struct {
		name string
		tls  bool
		port int
		want listenCall
	}{
		{
			name: "tls listens over TLS",
			tls:  true,
			port: 8443,
			want: listenCall{tls: true, network: "tcp", addr: ":8443"},
		},
		{
			name: "plaintext listens directly",
			tls:  false,
			port: 8080,
			want: listenCall{tls: false, network: "tcp", addr: ":8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, factory := newTestPlugin()

			ln, err := plugin.Listen(configJSON(t, Config{AuthKey: authKey, Tls: tt.tls}), tt.port)
			require.NoError(t, err)
			require.NotNil(t, ln)

			assert.Equal(t, []listenCall{tt.want}, factory.only(t).calls())
		})
	}
}

func TestMissingAuthKeyErrorsOnEveryEntryPoint(t *testing.T) {
	tests := []struct {
		name string
		call func(*Tailscale, json.RawMessage) error
	}{
		{
			name: "Client",
			call: func(plugin *Tailscale, config json.RawMessage) error {
				_, err := plugin.Client(config)
				return err
			},
		},
		{
			name: "Listen",
			call: func(plugin *Tailscale, config json.RawMessage) error {
				_, err := plugin.Listen(config, 8080)
				return err
			},
		},
		{
			name: "StartEgressProxy",
			call: func(plugin *Tailscale, config json.RawMessage) error {
				_, err := plugin.StartEgressProxy(context.Background(), config)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, factory := newTestPlugin()

			err := tt.call(plugin, configJSON(t, Config{Hostname: "agent"}))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "auth key")
			assert.Zero(t, factory.count())
		})
	}
}

func TestInvalidConfigErrorsOnEveryEntryPoint(t *testing.T) {
	tests := []struct {
		name string
		call func(*Tailscale, json.RawMessage) error
	}{
		{
			name: "Client",
			call: func(plugin *Tailscale, config json.RawMessage) error {
				_, err := plugin.Client(config)
				return err
			},
		},
		{
			name: "Listen",
			call: func(plugin *Tailscale, config json.RawMessage) error {
				_, err := plugin.Listen(config, 8080)
				return err
			},
		},
		{
			name: "StartEgressProxy",
			call: func(plugin *Tailscale, config json.RawMessage) error {
				_, err := plugin.StartEgressProxy(context.Background(), config)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, factory := newTestPlugin()

			err := tt.call(plugin, json.RawMessage(`not json`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "error parsing config")
			assert.Zero(t, factory.count())
		})
	}
}
