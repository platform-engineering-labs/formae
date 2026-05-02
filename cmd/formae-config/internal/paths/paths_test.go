// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package paths_test

import (
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/paths"
)

func TestResolveConfigDir_PrefersFormaeEnv(t *testing.T) {
	t.Setenv("FORMAE_CONFIG_DIR", "/tmp/test-formae")
	t.Setenv("XDG_CONFIG_HOME", "/should-not-be-used")
	t.Setenv("HOME", "/should-not-be-used")

	got, err := paths.ResolveConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/test-formae" {
		t.Errorf("got %q, want /tmp/test-formae", got)
	}
}

func TestResolveConfigDir_FallsBackToXDG(t *testing.T) {
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg-home")
	t.Setenv("HOME", "/should-not-be-used")

	got, err := paths.ResolveConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join("/xdg-home", "formae")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveConfigDir_FallsBackToHome(t *testing.T) {
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/test-home")

	got, err := paths.ResolveConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join("/test-home", ".config", "formae")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
