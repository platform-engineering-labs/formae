// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"errors"
	"os"
	"path/filepath"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func WalkForProjectFile(path string) string {
	currentDir, _ := filepath.Abs(path)

	for {
		filePath := filepath.Join(currentDir, ProjectFile)
		_, err := os.Stat(filePath)

		if err == nil {
			return currentDir
		} else if !os.IsNotExist(err) {
			return ""
		}

		parentDir := filepath.Dir(currentDir)

		if parentDir == currentDir {
			return ""
		}
		currentDir = parentDir
	}
}

func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !errors.Is(err, os.ErrNotExist)
}

func addSchemaContextProperties(cmd pkgmodel.Command, mode pkgmodel.FormaApplyMode, props map[string]string) {
	if props == nil {
		return
	}

	props["$cmd"] = string(cmd)
	props["$mode"] = string(mode)
}
