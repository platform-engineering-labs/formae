// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package all imports all schema implementations to trigger their init() registrations.
package all

import (
	_ "github.com/platform-engineering-labs/formae/internal/schema/json"
	_ "github.com/platform-engineering-labs/formae/internal/schema/pkl"
	_ "github.com/platform-engineering-labs/formae/internal/schema/yaml"
)
