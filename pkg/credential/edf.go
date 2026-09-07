// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import (
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
)

// RegisterEDFTypes registers the message types that cross the Ergo network
// boundary between the agent and an oidc-credential broker: the broker's
// announcement, and both halves of the IdentityToken call.
//
// Three processes have to agree on these: the broker (which calls this from
// startNodeStep), the agent, and the resource plugin that actually issues the
// IdentityToken call. The latter two get it through
// plugin.RegisterSharedEDFTypes, which calls this. Each type carries its own
// MarshalEDF/UnmarshalEDF (msgpack_edf.go), so their nested types need no
// separate registration.
func RegisterEDFTypes() error {
	types := []any{
		OidcCredentialPluginAnnouncement{},
		OidcIdentityTokenRequest{},
		IdentityTokenResponse{},
	}

	for _, t := range types {
		if err := edf.RegisterTypeOf(t); err != nil && err != gen.ErrTaken {
			return err
		}
	}
	return nil
}
