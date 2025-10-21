// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"encoding/json"
	"net"
	"net/http"
)

const Network Type = "network"

type NetworkPlugin interface {
	Client(config json.RawMessage) (*http.Client, error)
	Listen(config json.RawMessage, port int) (net.Listener, error)
}
