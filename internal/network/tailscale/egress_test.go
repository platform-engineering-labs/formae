// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindCall records one call to the plugin's listen seam.
type bindCall struct {
	network string
	addr    string
}

// listenRecorder stands in for net.Listen: it records the address the proxy
// asked for and binds loopback on an arbitrary free port instead, so no test
// has to guess at a port that is free.
type listenRecorder struct {
	err error

	mu        sync.Mutex
	calls     []bindCall
	listeners []net.Listener
}

func (r *listenRecorder) listen(network, addr string) (net.Listener, error) {
	r.mu.Lock()
	r.calls = append(r.calls, bindCall{network: network, addr: addr})
	err := r.err
	r.mu.Unlock()

	if err != nil {
		return nil, err
	}

	ln, err := net.Listen(network, "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.listeners = append(r.listeners, ln)
	r.mu.Unlock()

	return ln, nil
}

func (r *listenRecorder) binds() []bindCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bindCall(nil), r.calls...)
}

// addr returns the address the single bound listener actually listens on.
func (r *listenRecorder) addr(t *testing.T) string {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.listeners, 1)

	return r.listeners[0].Addr().String()
}

// fastRetry keeps the shape of the route retry budget — three attempts, each
// bounded, inside one overall budget — with waits short enough for a test.
func fastRetry() retryPolicy {
	return retryPolicy{
		budget:   300 * time.Millisecond,
		backoffs: []time.Duration{time.Millisecond, time.Millisecond},
	}
}

// newEgressPlugin returns a plugin wired to a fake node factory, a recording
// listen seam, and a route retry budget that costs no real time.
func newEgressPlugin() (*Tailscale, *nodeFactory, *listenRecorder) {
	factory := &nodeFactory{}
	binder := &listenRecorder{}

	plugin := &Tailscale{
		newNode:    factory.newNode,
		listen:     binder.listen,
		routeRetry: fastRetry(),
	}

	return plugin, factory, binder
}

func TestStartEgressProxyWithoutAPortDoesNothing(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "port absent", cfg: Config{AuthKey: authKey}},
		{name: "port zero", cfg: Config{AuthKey: authKey, EgressProxyPort: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, factory, binder := newEgressPlugin()

			proxy, err := plugin.StartEgressProxy(context.Background(), configJSON(t, tt.cfg))

			require.NoError(t, err)
			assert.Nil(t, proxy)
			assert.Zero(t, factory.count())
			assert.Empty(t, binder.binds())
		})
	}
}

func TestStartEgressProxyWithACanceledContextDoesNothing(t *testing.T) {
	plugin, factory, binder := newEgressPlugin()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	proxy, err := plugin.StartEgressProxy(ctx, configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))

	require.NoError(t, err)
	assert.Nil(t, proxy)
	assert.Zero(t, factory.count())
	assert.Empty(t, binder.binds())
}

func TestStartEgressProxyRejectsAPortOutsideTheValidRange(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{name: "negative", port: -1},
		{name: "above the port range", port: 65536},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, factory, binder := newEgressPlugin()

			proxy, err := plugin.StartEgressProxy(context.Background(),
				configJSON(t, Config{AuthKey: authKey, EgressProxyPort: tt.port}))

			require.Error(t, err)
			assert.Nil(t, proxy)
			assert.Contains(t, err.Error(), "egress proxy port")
			assert.Zero(t, factory.count())
			assert.Empty(t, binder.binds())
		})
	}
}

func TestStartEgressProxyReturnsANodeStartFailure(t *testing.T) {
	plugin, factory, binder := newEgressPlugin()
	factory.startErr = errors.New("state directory is locked")

	proxy, err := plugin.StartEgressProxy(context.Background(),
		configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))

	require.Error(t, err)
	assert.Nil(t, proxy)
	assert.Contains(t, err.Error(), "starting the node")
	assert.Contains(t, err.Error(), "state directory is locked")

	node := factory.only(t)
	assert.Equal(t, 1, node.starts())
	assert.Zero(t, node.routes())
	assert.Empty(t, binder.binds())
}

func TestStartEgressProxyGivesUpWhenRoutesKeepFailing(t *testing.T) {
	plugin, factory, binder := newEgressPlugin()
	factory.acceptRoutes = func(context.Context) error {
		return errors.New("the local node is not running")
	}

	proxy, err := plugin.StartEgressProxy(context.Background(),
		configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))

	require.Error(t, err)
	assert.Nil(t, proxy)
	assert.Contains(t, err.Error(), "subnet routes")
	assert.Contains(t, err.Error(), "the local node is not running")
	assert.Equal(t, 3, factory.only(t).routes())
	assert.Empty(t, binder.binds())
}

func TestStartEgressProxyBoundsEachRouteAttempt(t *testing.T) {
	plugin, factory, binder := newEgressPlugin()
	// A node that never reaches the running state: every attempt runs until
	// its own deadline expires.
	factory.acceptRoutes = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	started := time.Now()
	proxy, err := plugin.StartEgressProxy(context.Background(),
		configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))

	require.Error(t, err)
	assert.Nil(t, proxy)
	assert.Equal(t, 3, factory.only(t).routes())
	assert.Less(t, time.Since(started), 2*fastRetry().budget)
	assert.Empty(t, binder.binds())
}

func TestStartEgressProxyRetriesRoutesUntilTheySucceed(t *testing.T) {
	plugin, factory, binder := newEgressPlugin()

	var attempts atomic.Int32
	factory.acceptRoutes = func(context.Context) error {
		if attempts.Add(1) == 1 {
			return errors.New("the local node is not running")
		}

		return nil
	}

	proxy, err := plugin.StartEgressProxy(context.Background(),
		configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))

	require.NoError(t, err)
	require.NotNil(t, proxy)
	t.Cleanup(func() { proxy.Stop(false) })

	assert.Equal(t, 2, factory.only(t).routes())
	assert.Len(t, binder.binds(), 1)
}

func TestStartEgressProxyBindsLoopbackOnTheConfiguredPort(t *testing.T) {
	plugin, factory, binder := newEgressPlugin()

	proxy, err := plugin.StartEgressProxy(context.Background(),
		configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))

	require.NoError(t, err)
	require.NotNil(t, proxy)
	t.Cleanup(func() { proxy.Stop(false) })

	assert.Equal(t, []bindCall{{network: "tcp", addr: "127.0.0.1:1080"}}, binder.binds())

	node := factory.only(t)
	assert.Equal(t, 1, node.starts())
	assert.Equal(t, 1, node.routes())
}

func TestStartEgressProxyReusesTheNodeAcrossEgressPorts(t *testing.T) {
	plugin, factory, _ := newEgressPlugin()
	config := Config{AuthKey: authKey, Hostname: "agent"}

	_, err := plugin.Client(configJSON(t, config))
	require.NoError(t, err)

	config.EgressProxyPort = 1080
	first, err := plugin.StartEgressProxy(context.Background(), configJSON(t, config))
	require.NoError(t, err)
	require.NotNil(t, first)
	t.Cleanup(func() { first.Stop(false) })

	config.EgressProxyPort = 1081
	second, err := plugin.StartEgressProxy(context.Background(), configJSON(t, config))
	require.NoError(t, err)
	require.NotNil(t, second)
	t.Cleanup(func() { second.Stop(false) })

	assert.Equal(t, 1, factory.count())
}

func TestEgressProxyTunnelsConnectRequests(t *testing.T) {
	tests := []struct {
		name string
		host string
		port uint16
		want string
	}{
		{name: "domain name", host: "grafana.internal", port: 80, want: "grafana.internal:80"},
		{name: "IPv4 address", host: "10.0.0.5", port: 443, want: "10.0.0.5:443"},
		{name: "IPv6 address", host: "fd7a:115c:a1e0::1", port: 80, want: "[fd7a:115c:a1e0::1]:80"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := startTestProxy(t)

			conn := socksGreet(t, proxy.addr)
			socksRequest(t, conn, socksConnect, tt.host, tt.port)

			_, err := conn.Write([]byte("ping"))
			require.NoError(t, err)

			echoed := make([]byte, len("ping"))
			_, err = io.ReadFull(conn, echoed)
			require.NoError(t, err)
			assert.Equal(t, "ping", string(echoed))

			assert.Equal(t, []dialCall{{network: "tcp", addr: tt.want}}, proxy.node.dials())
		})
	}
}

func TestEgressProxyDialsOnlyOverTCP(t *testing.T) {
	tests := []struct {
		name    string
		network string
		refused bool
	}{
		{name: "tcp", network: "tcp"},
		{name: "tcp4", network: "tcp4"},
		{name: "tcp6", network: "tcp6"},
		{name: "udp", network: "udp", refused: true},
		{name: "udp4", network: "udp4", refused: true},
		{name: "udp6", network: "udp6", refused: true},
		{name: "unix", network: "unix", refused: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := startTestProxy(t)

			conn, err := proxy.proxy.dialTailnet(context.Background(), tt.network, "grafana.internal:80")

			if tt.refused {
				require.Error(t, err)
				assert.Nil(t, conn)
				assert.Contains(t, err.Error(), tt.network)
				assert.Empty(t, proxy.node.dials(), "the node was asked to dial a refused network")

				return
			}

			require.NoError(t, err)
			require.NotNil(t, conn)
			t.Cleanup(func() { _ = conn.Close() })

			assert.Equal(t, []dialCall{{network: tt.network, addr: "grafana.internal:80"}}, proxy.node.dials())
		})
	}
}

func TestEgressProxyRelaysNoDatagrams(t *testing.T) {
	proxy := startTestProxy(t)

	conn := socksGreet(t, proxy.addr)
	bound := socksRequest(t, conn, socksAssociate, "0.0.0.0", 0)

	datagrams, err := net.Dial("udp", bound)
	require.NoError(t, err)
	t.Cleanup(func() { _ = datagrams.Close() })

	packet := append([]byte{0, 0, 0}, socksAddress("10.0.0.5", 53)...)
	packet = append(packet, "ping"...)
	_, err = datagrams.Write(packet)
	require.NoError(t, err)

	require.NoError(t, datagrams.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	_, err = datagrams.Read(make([]byte, 64))
	require.Error(t, err, "the proxy answered a datagram")

	assert.Empty(t, proxy.node.dials(), "the node was asked to dial for a datagram")
	assert.Zero(t, proxy.echo.accepts(), "a datagram reached the destination")
}

func TestEgressProxyStopsWhenTheContextIsCanceled(t *testing.T) {
	proxy := startTestProxy(t)

	// A tunnel proves the proxy was serving before the cancellation.
	conn := socksGreet(t, proxy.addr)
	socksRequest(t, conn, socksConnect, "grafana.internal", 80)

	proxy.cancel()

	proxy.requireServeReturned(t)
	requireClosed(t, conn)

	_, err := net.Dial("tcp", proxy.addr)
	assert.Error(t, err, "the proxy is still accepting connections")
}

func TestEgressProxyStopIsIdempotent(t *testing.T) {
	proxy := startTestProxy(t)

	conn := socksGreet(t, proxy.addr)
	socksRequest(t, conn, socksConnect, "grafana.internal", 80)

	proxy.proxy.Stop(false)
	proxy.proxy.Stop(true)
	proxy.proxy.Stop(false)

	proxy.requireServeReturned(t)
	requireClosed(t, conn)
}

func TestEgressProxyStopClosesAnInFlightTunnel(t *testing.T) {
	proxy := startTestProxy(t)

	conn := socksGreet(t, proxy.addr)
	socksRequest(t, conn, socksConnect, "grafana.internal", 80)

	_, err := conn.Write([]byte("ping"))
	require.NoError(t, err)

	echoed := make([]byte, len("ping"))
	_, err = io.ReadFull(conn, echoed)
	require.NoError(t, err)
	require.Equal(t, "ping", string(echoed))

	proxy.proxy.Stop(false)

	requireClosed(t, conn)
}

func TestEgressProxyForgetsConnectionsAsTheyClose(t *testing.T) {
	proxy := startTestProxy(t)

	const tunnels = 3

	for range tunnels {
		conn := socksGreet(t, proxy.addr)
		socksRequest(t, conn, socksConnect, "grafana.internal", 80)

		assert.Eventually(t, func() bool { return proxy.proxy.listener.liveConns() == 1 },
			2*time.Second, 10*time.Millisecond, "the proxy is not tracking the open connection")

		require.NoError(t, conn.Close())

		assert.Eventually(t, func() bool { return proxy.proxy.listener.liveConns() == 0 },
			2*time.Second, 10*time.Millisecond, "the proxy kept hold of a closed connection")
	}

	assert.Equal(t, tunnels, proxy.echo.accepts())
}

func TestEgressProxyStopNeverClosesTheNode(t *testing.T) {
	proxy := startTestProxy(t)

	proxy.proxy.Stop(false)
	proxy.requireServeReturned(t)

	assert.Zero(t, proxy.node.closes())

	// The node still serves the inbound listener the agent rides on.
	ln, err := proxy.plugin.Listen(configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}), 8080)
	require.NoError(t, err)
	require.NotNil(t, ln)
	assert.NoError(t, ln.Close())
}

// requireClosed fails the test unless conn was closed by its peer, rather than
// still open and merely idle.
func requireClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	_, err := conn.Read(make([]byte, 1))
	require.Error(t, err)

	var timeout net.Error
	require.False(t, errors.As(err, &timeout) && timeout.Timeout(), "the connection is still open")
}

// The SOCKS5 wire constants the test client needs, from RFC 1928.
const (
	socksVersion   = 5
	socksNoAuth    = 0
	socksConnect   = 1
	socksAssociate = 3

	atypIPv4   = 1
	atypDomain = 3
	atypIPv6   = 4
)

// socksGreet opens a connection to the proxy and completes the SOCKS5
// greeting, leaving the connection ready for a request.
func socksGreet(t *testing.T, proxyAddr string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// No exchange with a proxy on loopback should take seconds; a deadline
	// keeps a test that stops being served from hanging the suite.
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = conn.Write([]byte{socksVersion, 1, socksNoAuth})
	require.NoError(t, err)

	reply := make([]byte, 2)
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err)
	require.Equal(t, []byte{socksVersion, socksNoAuth}, reply)

	return conn
}

// socksAddress renders a destination the way a SOCKS5 client does, taking the
// address type from the host.
func socksAddress(host string, port uint16) []byte {
	var packet []byte

	switch ip := net.ParseIP(host); {
	case ip == nil:
		packet = append([]byte{atypDomain, byte(len(host))}, host...)
	case ip.To4() != nil:
		packet = append([]byte{atypIPv4}, ip.To4()...)
	default:
		packet = append([]byte{atypIPv6}, ip.To16()...)
	}

	return binary.BigEndian.AppendUint16(packet, port)
}

// socksRequest sends a SOCKS5 request over an already-greeted connection and
// returns the address the proxy reports it bound for the request.
func socksRequest(t *testing.T, conn net.Conn, command byte, host string, port uint16) string {
	t.Helper()

	request := append([]byte{socksVersion, command, 0}, socksAddress(host, port)...)
	_, err := conn.Write(request)
	require.NoError(t, err)

	header := make([]byte, 4)
	_, err = io.ReadFull(conn, header)
	require.NoError(t, err)
	require.Equal(t, byte(socksVersion), header[0])
	require.Zero(t, header[1], "the proxy refused the request")

	var boundHost string

	switch header[3] {
	case atypIPv4, atypIPv6:
		size := 4
		if header[3] == atypIPv6 {
			size = 16
		}

		ip := make([]byte, size)
		_, err = io.ReadFull(conn, ip)
		require.NoError(t, err)
		boundHost = net.IP(ip).String()
	case atypDomain:
		size := make([]byte, 1)
		_, err = io.ReadFull(conn, size)
		require.NoError(t, err)

		name := make([]byte, size[0])
		_, err = io.ReadFull(conn, name)
		require.NoError(t, err)
		boundHost = string(name)
	default:
		t.Fatalf("unexpected address type %d in the reply", header[3])
	}

	boundPort := make([]byte, 2)
	_, err = io.ReadFull(conn, boundPort)
	require.NoError(t, err)

	return net.JoinHostPort(boundHost, strconv.Itoa(int(binary.BigEndian.Uint16(boundPort))))
}

// echoServer is the destination the fake node's Dial is served from: it echoes
// back what it is sent, so a tunnel can be exercised in both directions
// without leaving the test process.
type echoServer struct {
	listener net.Listener

	mu       sync.Mutex
	accepted int
}

func newEchoServer(t *testing.T) *echoServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	echo := &echoServer{listener: listener}
	go echo.serve()

	return echo
}

func (e *echoServer) serve() {
	for {
		conn, err := e.listener.Accept()
		if err != nil {
			return
		}

		e.mu.Lock()
		e.accepted++
		e.mu.Unlock()

		go func() {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func (e *echoServer) addr() string {
	return e.listener.Addr().String()
}

func (e *echoServer) accepts() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.accepted
}

// testProxy is an egress proxy that is serving on loopback, together with the
// fake node and the destination behind it.
type testProxy struct {
	proxy  *egressProxy
	plugin *Tailscale
	addr   string
	node   *fakeNode
	echo   *echoServer
	cancel context.CancelFunc
	served chan struct{}
}

// startTestProxy starts an egress proxy over a fake node whose Dial is served
// by an in-process echo, and serves it until the test ends.
func startTestProxy(t *testing.T) *testProxy {
	t.Helper()

	echo := newEchoServer(t)
	plugin, factory, binder := newEgressPlugin()
	factory.dialTo = echo.addr()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started, err := plugin.StartEgressProxy(ctx, configJSON(t, Config{AuthKey: authKey, EgressProxyPort: 1080}))
	require.NoError(t, err)
	require.NotNil(t, started)

	proxy := &testProxy{
		proxy:  started.(*egressProxy),
		plugin: plugin,
		addr:   binder.addr(t),
		node:   factory.only(t),
		echo:   echo,
		cancel: cancel,
		served: make(chan struct{}),
	}

	go func() {
		defer close(proxy.served)
		proxy.proxy.Serve()
	}()

	t.Cleanup(func() {
		proxy.proxy.Stop(false)
		proxy.requireServeReturned(t)
	})

	return proxy
}

// requireServeReturned fails the test unless Serve has returned, or returns as
// soon as it does.
func (p *testProxy) requireServeReturned(t *testing.T) {
	t.Helper()

	select {
	case <-p.served:
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy kept serving after it was stopped")
	}
}
