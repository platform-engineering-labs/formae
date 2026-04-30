// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package paths resolves the formae configuration directory.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveConfigDir returns the formae config directory, in this order:
//  1. $FORMAE_CONFIG_DIR (test seam)
//  2. $XDG_CONFIG_HOME/formae
//  3. $HOME/.config/formae
func ResolveConfigDir() (string, error) {
	if d := os.Getenv("FORMAE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "formae"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "formae"), nil
}
