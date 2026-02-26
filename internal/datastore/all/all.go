// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package all imports all datastore implementations to trigger their init()
// registration with the datastore extension registry.
package all

import (
	// Import for init() side effects — registers SQLite, Postgres, and Aurora
	// factories with datastore.DefaultRegistry.
	_ "github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
)
