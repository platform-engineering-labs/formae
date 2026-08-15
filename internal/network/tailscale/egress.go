// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"tailscale.com/net/socks5"

	"github.com/platform-engineering-labs/formae/internal/network"
)

// maxPort is the highest port a listener can bind.
const maxPort = 65535

var _ network.EgressProxy = (*Tailscale)(nil)

// retryPolicy bounds a retry loop: one attempt per entry in backoffs plus the
// first, the waits between them taken from backoffs in order, and never more
// than budget from first attempt to last.
type retryPolicy struct {
	budget   time.Duration
	backoffs []time.Duration
}

// defaultRouteRetry is the budget the plugin waits for a node to accept its
// peers' subnet routes: three attempts, two then four seconds apart, and at
// most thirty seconds in total.
func defaultRouteRetry() retryPolicy {
	return retryPolicy{
		budget:   30 * time.Second,
		backoffs: []time.Duration{2 * time.Second, 4 * time.Second},
	}
}

// orDefault reads a zero-valued policy as the default one.
func (p retryPolicy) orDefault() retryPolicy {
	if p.budget == 0 {
		return defaultRouteRetry()
	}

	return p
}

// attempts is the number of attempts the policy allows.
func (p retryPolicy) attempts() int {
	return len(p.backoffs) + 1
}

// StartEgressProxy binds a SOCKS5 proxy on loopback that dials out over the
// tailnet, so processes on this host can reach addresses only the tailnet
// routes.
//
// It returns a nil proxy and no error when egress is off — no port configured
// — or when ctx is already canceled. Neither case touches the node.
//
// Startup is synchronous and ordered: the node starts, is asked to accept the
// subnet routes its peers advertise, and only then is the port bound. Binding
// last leaves no window in which the port answers a caller the node cannot yet
// route for.
func (t *Tailscale) StartEgressProxy(ctx context.Context, config json.RawMessage) (network.Proxy, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}

	if cfg.EgressProxyPort == 0 {
		return nil, nil
	}

	if ctx.Err() != nil {
		return nil, nil
	}

	if cfg.EgressProxyPort < 0 || cfg.EgressProxyPort > maxPort {
		return nil, fmt.Errorf("tailscale: egress proxy port %d is not a valid port", cfg.EgressProxyPort)
	}

	n, err := t.sharedNode(cfg)
	if err != nil {
		return nil, err
	}

	// A tsnet server keeps the outcome of its first start for the life of the
	// process, so a failed start cannot be retried.
	if err := n.Start(); err != nil {
		return nil, fmt.Errorf("tailscale: error starting the node: %w", err)
	}

	if err := acceptRoutes(ctx, n, t.routeRetry.orDefault()); err != nil {
		return nil, err
	}

	listen := t.listen
	if listen == nil {
		listen = net.Listen
	}

	ln, err := listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.EgressProxyPort))
	if err != nil {
		return nil, fmt.Errorf("tailscale: error binding the egress proxy: %w", err)
	}

	proxy := &egressProxy{
		listener: newTrackingListener(ln),
		dial:     n.Dial,
		done:     make(chan struct{}),
	}

	// Watching from the moment the port is bound leaves no window in which a
	// cancellation could pass over a listener and leave it bound.
	go proxy.stopOnCancel(ctx)

	return proxy, nil
}

// acceptRoutes asks the node to use the subnet routes its peers advertise,
// retrying a failure while the budget allows. Each attempt carries a deadline
// of its own — an equal share of the budget, and never more than what is left
// of it. The node applies no timeout of its own, so one attempt against a node
// that never registers would otherwise consume every retry.
func acceptRoutes(ctx context.Context, n node, policy retryPolicy) error {
	started := time.Now()

	budgetCtx, cancel := context.WithTimeout(ctx, policy.budget)
	defer cancel()

	perAttempt := policy.budget / time.Duration(policy.attempts())

	var err error

	for attempt := range policy.attempts() {
		if attempt > 0 && !wait(budgetCtx, policy.backoffs[attempt-1]) {
			break
		}

		attemptCtx, cancelAttempt := context.WithTimeout(budgetCtx, perAttempt)
		err = n.AcceptRoutes(attemptCtx)
		cancelAttempt()

		if err == nil {
			return nil
		}

		if budgetCtx.Err() != nil {
			break
		}
	}

	return fmt.Errorf("tailscale: gave up accepting subnet routes after %s: %w", time.Since(started), err)
}

// wait blocks for d, and reports false as soon as ctx is done instead.
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// trackingListener is a listener that keeps hold of every connection it has
// accepted and that is still open, so all of them can be closed at once. The
// SOCKS5 server hands each accepted connection to a goroutine of its own and
// keeps no handle on it, so this listener is what makes a stop reach the
// connections in flight.
type trackingListener struct {
	net.Listener

	mu      sync.Mutex
	stopped bool
	conns   map[*trackedConn]struct{}
}

func newTrackingListener(listener net.Listener) *trackingListener {
	return &trackingListener{
		Listener: listener,
		conns:    make(map[*trackedConn]struct{}),
	}
}

func (l *trackingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	tracked := &trackedConn{Conn: conn, listener: l}

	l.mu.Lock()
	stopped := l.stopped
	if !stopped {
		l.conns[tracked] = struct{}{}
	}
	l.mu.Unlock()

	// A connection accepted in the race with a stop is closed rather than
	// tracked: nothing would ever come back to close it.
	if stopped {
		_ = conn.Close()

		return nil, net.ErrClosed
	}

	return tracked, nil
}

// closeConns closes every connection still open, and refuses to track any
// accepted after this point.
func (l *trackingListener) closeConns() {
	l.mu.Lock()
	l.stopped = true
	open := make([]*trackedConn, 0, len(l.conns))

	for conn := range l.conns {
		open = append(open, conn)
	}
	l.mu.Unlock()

	for _, conn := range open {
		_ = conn.Close()
	}
}

// forget drops a connection that is no longer open.
func (l *trackingListener) forget(conn *trackedConn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.conns, conn)
}

// liveConns is the number of accepted connections still open.
func (l *trackingListener) liveConns() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.conns)
}

// trackedConn is an accepted connection that leaves its listener's set as soon
// as it closes, so the set holds the live connections rather than a history of
// every connection the proxy ever carried.
type trackedConn struct {
	net.Conn

	listener *trackingListener
	once     sync.Once
}

func (c *trackedConn) Close() error {
	var err error

	c.once.Do(func() {
		c.listener.forget(c)
		err = c.Conn.Close()
	})

	return err
}

// egressProxy is a running SOCKS5 proxy bound on loopback, tunnelling every
// connection it accepts through the tailnet node it was started with.
type egressProxy struct {
	listener *trackingListener
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)

	stopOnce sync.Once
	done     chan struct{}
}

var _ network.Proxy = (*egressProxy)(nil)

// Serve answers SOCKS5 requests until the proxy is stopped. It reports no
// error: a proxy that stops serving is reported where it happens, the way the
// API server reports its own.
func (p *egressProxy) Serve() {
	// Idempotent teardown: a no-op if the proxy was already stopped, and the
	// path that tears it down if the accept loop returned on its own.
	defer p.Stop(false)

	server := &socks5.Server{
		Dialer: p.dialTailnet,
		Logf: func(format string, args ...any) {
			slog.Debug(fmt.Sprintf(format, args...))
		},
	}

	if err := server.Serve(p.listener); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Error("Egress proxy stopped serving", "error", err)
	}
}

// dialTailnet opens the connection a proxied request asked for, over the
// tailnet. Only TCP is carried: a SOCKS5 UDP association is refused here, so
// the node is never asked to relay a datagram.
func (p *egressProxy) dialTailnet(ctx context.Context, network, addr string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return p.dial(ctx, network, addr)
	default:
		return nil, fmt.Errorf("tailscale: the egress proxy carries no %s traffic", network)
	}
}

// Stop closes the port and then every connection still tunnelling through it.
// It is idempotent, and takes the same route whether or not the stop is
// forced: a proxied connection has no work of its own to finish.
//
// It never closes the node, which also carries the agent's inbound listener.
func (p *egressProxy) Stop(_ bool) {
	p.stopOnce.Do(func() {
		close(p.done)
		_ = p.listener.Close()
		p.listener.closeConns()
	})
}

// stopOnCancel stops the proxy when ctx is canceled, and returns when the
// proxy is stopped by any other route.
func (p *egressProxy) stopOnCancel(ctx context.Context) {
	select {
	case <-ctx.Done():
		p.Stop(false)
	case <-p.done:
	}
}
