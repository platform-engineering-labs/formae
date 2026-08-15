// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// node is the slice of *tsnet.Server the plugin depends on. Depending on an
// interface rather than the concrete server lets tests drive the plugin
// without ever joining a tailnet.
//
// AcceptRoutes sits on the interface instead of the LocalClient it is built
// from because LocalClient returns a concrete *local.Client, which a fake
// cannot supply. Start stays separate from AcceptRoutes because tsnet caches
// a failed start for the life of the process, so a start failure is permanent
// while a route failure is not.
type node interface {
	Listen(network, addr string) (net.Listener, error)
	ListenTLS(network, addr string) (net.Listener, error)
	HTTPClient() *http.Client
	Dial(ctx context.Context, network, addr string) (net.Conn, error)
	Start() error
	AcceptRoutes(ctx context.Context) error
	Close() error
}

// tsnetNode is the real node: a tsnet server plus the route-accepting
// behaviour the plugin needs on top of it.
type tsnetNode struct {
	*tsnet.Server
}

// newTsnetNode builds a tsnet server from an already-resolved config.
func newTsnetNode(cfg *Config) node {
	srv := new(tsnet.Server)
	srv.Hostname = cfg.Hostname
	srv.AuthKey = cfg.AuthKey
	srv.AdvertiseTags = cfg.AdvertiseTags

	return &tsnetNode{Server: srv}
}

// AcceptRoutes asks the node to use the subnet routes its peers advertise, so
// traffic dialled through it can reach addresses behind a subnet router.
func (n *tsnetNode) AcceptRoutes(ctx context.Context) error {
	client, err := n.LocalClient() // starts the node if it is not running yet
	if err != nil {
		return fmt.Errorf("tailscale: error connecting to the local node: %w", err)
	}

	if err := awaitRunning(ctx, client); err != nil {
		return err
	}

	_, err = client.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:       ipn.Prefs{RouteAll: true},
		RouteAllSet: true,
	})
	if err != nil {
		return fmt.Errorf("tailscale: error accepting subnet routes: %w", err)
	}

	return nil
}

// awaitRunning blocks until the node's backend reaches the running state, so
// that prefs edited afterwards land on a registered node.
//
// This is tsnet's own Up without two of its steps. Up clears the node's
// persisted serve config as a side effect, which would silently discard
// configuration the node is serving; and it fetches a status only to return
// it, which nothing here needs. What is left is the IPN bus watch Up waits on.
func awaitRunning(ctx context.Context, client *local.Client) error {
	// Ask for the current state up front so an already-running node returns
	// immediately, and omit private keys, which this watcher has no use for.
	watcher, err := client.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyNoPrivateKeys)
	if err != nil {
		return fmt.Errorf("tailscale: error watching the local node: %w", err)
	}
	defer watcher.Close()

	for {
		notification, err := watcher.Next()
		if err != nil {
			return fmt.Errorf("tailscale: error waiting for the node to start: %w", err)
		}

		if notification.ErrMessage != nil {
			return fmt.Errorf("tailscale: node failed to start: %s", *notification.ErrMessage)
		}

		if state := notification.State; state != nil && *state == ipn.Running {
			return nil
		}
	}
}
