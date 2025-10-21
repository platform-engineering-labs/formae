// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"encoding/json"
	"net/http"
)

const Authentication Type = "authentication"

type AuthenticationPlugin interface {
	Handler(config json.RawMessage) (func(http.Handler) http.Handler, error)
	Authorization(config json.RawMessage) (http.Header, error)
}
