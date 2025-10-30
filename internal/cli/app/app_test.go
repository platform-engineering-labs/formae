// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/config"
)

// On Linux dynamically loaded modules are mapped to shared address space.
// on MacOS/mach this is not the case
// this will catch a dependency mismatch in loading conflicting plugins
// as it will go unnoticed locally
func TestAppPluginLoading(t *testing.T) {
	assert.NoError(t, config.Config.EnsureDataDirectory())
	assert.NotNil(t, NewApp())
}
