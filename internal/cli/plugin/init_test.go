// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePluginInitOptions(t *testing.T) {
	t.Run("no-input with all required flags succeeds", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
	})

	t.Run("no-input missing name returns error", func(t *testing.T) {
		opts := &PluginInitOptions{
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "--name")
	})

	t.Run("no-input missing multiple flags lists all missing", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:    "myplugin",
			NoInput: true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "--namespace")
		assert.Contains(t, err.Error(), "--description")
		assert.Contains(t, err.Error(), "--author")
		assert.Contains(t, err.Error(), "--module-path")
	})

	t.Run("no-input applies default license", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "Apache-2.0", opts.License)
	})

	t.Run("no-input applies default output dir from name", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "./myplugin", opts.OutputDir)
	})

	t.Run("no-input preserves provided license", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			License:     "MIT",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "MIT", opts.License)
	})

	t.Run("no-input preserves provided output dir", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			OutputDir:   "./custom/path",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "./custom/path", opts.OutputDir)
	})

	t.Run("interactive mode does not require flags", func(t *testing.T) {
		opts := &PluginInitOptions{
			NoInput: false,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
	})

	t.Run("invalid plugin name returns error", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "Invalid_Name",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "plugin name")
	})

	t.Run("invalid namespace returns error", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "invalid_ns",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "namespace")
	})
}

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
