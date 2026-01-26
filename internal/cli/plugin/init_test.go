// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOutputDir(t *testing.T) {
	t.Run("non-existent directory is valid", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nonexistent")
		err := validateOutputDir(dir)
		if err != nil {
			t.Errorf("expected no error for non-existent directory, got: %v", err)
		}
	})

	t.Run("empty existing directory is valid", func(t *testing.T) {
		dir := t.TempDir() // Creates an empty directory
		err := validateOutputDir(dir)
		if err != nil {
			t.Errorf("expected no error for empty directory, got: %v", err)
		}
	})

	t.Run("current directory when empty is valid", func(t *testing.T) {
		// Create a temp directory and change to it
		dir := t.TempDir()
		oldWd, _ := os.Getwd()
		_ = os.Chdir(dir)
		defer func() { _ = os.Chdir(oldWd) }()

		err := validateOutputDir(".")
		if err != nil {
			t.Errorf("expected no error for empty current directory, got: %v", err)
		}
	})

	t.Run("non-empty directory is invalid", func(t *testing.T) {
		dir := t.TempDir()
		// Create a file in the directory
		f, _ := os.Create(filepath.Join(dir, "somefile.txt"))
		_ = f.Close()

		err := validateOutputDir(dir)
		if err == nil {
			t.Error("expected error for non-empty directory, got nil")
		}
	})

	t.Run("current directory when non-empty is invalid", func(t *testing.T) {
		dir := t.TempDir()
		// Create a file in the directory
		f, _ := os.Create(filepath.Join(dir, "somefile.txt"))
		_ = f.Close()

		oldWd, _ := os.Getwd()
		_ = os.Chdir(dir)
		defer func() { _ = os.Chdir(oldWd) }()

		err := validateOutputDir(".")
		if err == nil {
			t.Error("expected error for non-empty current directory, got nil")
		}
	})
}
