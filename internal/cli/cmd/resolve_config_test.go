// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cmd_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

func TestResolveConfigPath_ProfileResolvesToProfilesDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", "prod.pkl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := cmd.ResolveConfigPath("", "prod")
	if err != nil {
		t.Fatalf("ResolveConfigPath: %v", err)
	}
	if got != filepath.Join(dir, "profiles", "prod.pkl") {
		t.Errorf("got %q", got)
	}
}

func TestResolveConfigPath_ProfileTraversalRejected(t *testing.T) {
	t.Setenv("FORMAE_CONFIG_DIR", t.TempDir())
	_, err := cmd.ResolveConfigPath("", "../../etc/passwd")
	if !errors.Is(err, store.ErrInvalidName) {
		t.Errorf("traversal: %v, want ErrInvalidName", err)
	}
}

func TestResolveConfigPath_MissingProfileErrors(t *testing.T) {
	t.Setenv("FORMAE_CONFIG_DIR", t.TempDir())
	_, err := cmd.ResolveConfigPath("", "ghost")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing profile: %v, want ErrNotFound", err)
	}
}

func TestResolveConfigPath_ConfigPassthrough(t *testing.T) {
	got, err := cmd.ResolveConfigPath("/some/explicit.pkl", "")
	if err != nil {
		t.Fatalf("ResolveConfigPath: %v", err)
	}
	if got != "/some/explicit.pkl" {
		t.Errorf("got %q, want /some/explicit.pkl", got)
	}
}

func TestAddConfigFlags_MutuallyExclusive(t *testing.T) {
	c := &cobra.Command{Use: "test", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	cmd.AddConfigFlags(c)
	// Both flags should be registered.
	if c.Flags().Lookup("config") == nil {
		t.Error("--config flag not registered")
	}
	if c.Flags().Lookup("profile") == nil {
		t.Error("--profile flag not registered")
	}
	// Passing both together should produce an error.
	c.SetArgs([]string{"--config", "a.pkl", "--profile", "dev"})
	if err := c.Execute(); err == nil {
		t.Error("expected error when --config and --profile are both set")
	}
}
