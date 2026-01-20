// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandHomePath expands ~ to the user's home directory.
func ExpandHomePath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("./", path[1:])
		}

		return filepath.Join(home, path[1:])
	}

	return path
}
