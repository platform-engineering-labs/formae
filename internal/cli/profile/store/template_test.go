// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The embedded stub must parse on a clean machine with ZERO plugins installed
// (clean-install bootstrap writes it). We force an empty plugin dir.
func TestStubTemplate_ParsesWithEmptyPluginDir(t *testing.T) {
	t.Setenv("FORMAE_PLUGIN_DIR", t.TempDir()) // empty: no plugin wrappers
	dir := t.TempDir()
	path := filepath.Join(dir, "default.pkl")
	require.NoError(t, os.WriteFile(path, []byte(store.StubTemplate), 0o644))

	cfg, err := pkl.PKL{}.FormaeConfig(path)
	require.NoError(t, err, "stub must parse with no plugins installed")
	assert.Equal(t, "http://localhost", cfg.Cli.API.URL)
	assert.Equal(t, 49684, cfg.Cli.API.Port)
}
