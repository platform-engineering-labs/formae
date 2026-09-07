// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/config"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// On Linux dynamically loaded modules are mapped to shared address space.
// on MacOS/mach this is not the case
// this will catch a dependency mismatch in loading conflicting plugins
// as it will go unnoticed locally
func TestAppPluginLoading(t *testing.T) {
	assert.NoError(t, config.Config.EnsureDataDirectory())
	assert.NotNil(t, NewApp())
}

func TestParseIncludeSpec(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantNamespace string
		wantIsLocal   bool
	}{
		{
			name:          "simple namespace defaults to remote",
			input:         "aws",
			wantNamespace: "aws",
			wantIsLocal:   false,
		},
		{
			name:          "namespace with @local suffix",
			input:         "ovh@local",
			wantNamespace: "ovh",
			wantIsLocal:   true,
		},
		{
			name:          "@local suffix is case-insensitive (uppercase)",
			input:         "myplugin@LOCAL",
			wantNamespace: "myplugin",
			wantIsLocal:   true,
		},
		{
			name:          "@local suffix is case-insensitive (mixed case)",
			input:         "myplugin@Local",
			wantNamespace: "myplugin",
			wantIsLocal:   true,
		},
		{
			name:          "namespace is lowercased",
			input:         "AWS",
			wantNamespace: "aws",
			wantIsLocal:   false,
		},
		{
			name:          "both namespace and @local are lowercased",
			input:         "MyPlugin@LOCAL",
			wantNamespace: "myplugin",
			wantIsLocal:   true,
		},
		{
			name:          "namespace with @ but not @local is treated as remote",
			input:         "plugin@0.1.0",
			wantNamespace: "plugin@0.1.0",
			wantIsLocal:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace, isLocal := parseIncludeSpec(tt.input)
			assert.Equal(t, tt.wantNamespace, namespace)
			assert.Equal(t, tt.wantIsLocal, isLocal)
		})
	}
}

// TestAuthClient_NoAuthBlockReturnsTypedError verifies that AuthClient
// returns a *NoAuthPluginError — rather than a generic error or a nil
// client — when the active profile carries no cli.auth block, so callers
// like `formae login` can distinguish "nothing to discover" from a plugin
// that failed to start.
func TestAuthClient_NoAuthBlockReturnsTypedError(t *testing.T) {
	a := &App{Config: &pkgmodel.Config{}}

	client, err := a.AuthClient()

	assert.Nil(t, client)
	require.Error(t, err)
	var noAuth *NoAuthPluginError
	require.ErrorAs(t, err, &noAuth)
	assert.Equal(t, "no auth plugin configured for the active profile", err.Error())
}
