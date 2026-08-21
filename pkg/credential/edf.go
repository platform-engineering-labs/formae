// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import (
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
)

// RegisterEDFTypes registers the message types the broker sends across the
// Ergo network boundary. The broker calls this before announcing; the agent
// calls it directly (beside plugin.RegisterSharedEDFTypes, in
// internal/metastructure.NewMetastructureWithDataStoreAndContext) so both
// sides agree on the wire format.
func RegisterEDFTypes() error {
	types := []any{
		OidcCredentialPluginAnnouncement{},
	}

	for _, t := range types {
		if err := edf.RegisterTypeOf(t); err != nil && err != gen.ErrTaken {
			return err
		}
	}
	return nil
}
