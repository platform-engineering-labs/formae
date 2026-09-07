// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cmd_test

import (
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/config"
)

// ConfigDirectory must now honor FORMAE_CONFIG_DIR (previously it hard-coded
// ~/.config/formae and ignored the env).
func TestConfigDirectory_HonorsFormaeEnv(t *testing.T) {
	dir := t.TempDir()
	// Populate so ResolveConfigDir treats it as user config / or FORMAE_CONFIG_DIR wins outright.
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	got := config.Config.ConfigDirectory()
	if got != filepath.Clean(dir) {
		t.Errorf("ConfigDirectory() = %q, want %q", got, dir)
	}
}
