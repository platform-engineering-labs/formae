// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"os"
	"path/filepath"
	"strings"
)

func EnsureFileFolderHierarchy(path string) error {
	return EnsureFolderHierarchy(path[:strings.LastIndex(path, "/")])
}

func EnsureFolderHierarchy(path string) error {
	return os.MkdirAll(path, 0755)
}

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
