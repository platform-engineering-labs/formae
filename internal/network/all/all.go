// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package all imports all network plugin implementations to trigger their
// init()-based registration with the default registry.
package all

import (
	_ "github.com/platform-engineering-labs/formae/internal/network/tailscale"
)
