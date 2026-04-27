// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"log/slog"
	"os"
	"path/filepath"
)

const migratedMarker = ".migrated-v1"

// CleanStaleDevPlugins removes plugins from devDir that also exist in systemDir.
// This is a one-time migration: once run, a marker file is written to devDir to
// prevent re-running on subsequent startups.
func CleanStaleDevPlugins(systemDir, devDir string) error {
	markerPath := filepath.Join(devDir, migratedMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return nil // already migrated
	}

	systemEntries, err := os.ReadDir(systemDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no system dir, nothing to clean
		}
		return err
	}

	systemPlugins := make(map[string]bool)
	for _, entry := range systemEntries {
		if entry.IsDir() {
			systemPlugins[entry.Name()] = true
		}
	}

	devEntries, err := os.ReadDir(devDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range devEntries {
		if !entry.IsDir() {
			continue
		}
		if systemPlugins[entry.Name()] {
			target := filepath.Join(devDir, entry.Name())
			slog.Info("Removing stale dev plugin (now bundled in system dir)", "plugin", entry.Name(), "path", target)
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
	}

	return os.WriteFile(markerPath, []byte("done"), 0644)
}
