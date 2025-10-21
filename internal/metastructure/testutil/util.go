// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func PluginOverridesContext(overrides *plugin.ResourcePluginOverrides) (context.Context, context.CancelFunc) {
	if overrides != nil {
		return context.WithCancel(context.WithValue(context.Background(), plugin.ResourcePluginOverridesContextKey, overrides))
	} else {
		return context.WithCancel(context.Background())
	}
}

func FindProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root (no go.mod found)")
		}
		dir = parent
	}
}

func RunTestFromProjectRoot(t *testing.T, testFunc func(t *testing.T)) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current dir: %v", err)
	}
	defer func() {
		if err = os.Chdir(originalDir); err != nil {
			t.Logf("Warning: Failed to change back to original directory: %v", err)
		}
	}()

	root, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("Failed to find project root: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Failed to change to project root: %v", err)
	}

	testFunc(t)
}
