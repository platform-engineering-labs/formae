// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import "path/filepath"

// SystemPluginDir returns the system plugin directory derived from the binary
// path. The binary is expected at <install-root>/bin/<name>, so the system
// plugins live at <install-root>/formae/plugins.
func SystemPluginDir(binaryPath string) string {
	installRoot := filepath.Dir(filepath.Dir(binaryPath))
	return filepath.Join(installRoot, "formae", "plugins")
}
