// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/paths"
	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
)

// openStore resolves the formae config dir and returns a profiles.Store.
func openStore() (*profiles.Store, error) {
	root, err := paths.ResolveConfigDir()
	if err != nil {
		return nil, err
	}
	return profiles.New(root), nil
}
