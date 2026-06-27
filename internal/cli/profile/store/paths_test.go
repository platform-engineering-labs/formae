// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

func TestResolveConfigDir_FormaeEnvWins_Absolutized(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/should-not-be-used")
	// Relative value must resolve independent of cwd → absolutized.
	t.Setenv("FORMAE_CONFIG_DIR", "relative-cfg")
	got, err := store.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("got %q, want absolute path", got)
	}
	wantSuffix := string(os.PathSeparator) + "relative-cfg"
	if filepath.Base(got) != "relative-cfg" {
		t.Errorf("got %q, want a path ending in %q", got, wantSuffix)
	}
}

func TestResolveConfigDir_PopulatedLegacyBeatsPopulatedXDG(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// Populate both: legacy ~/.config/formae and $XDG/formae each get a profile.
	legacy := filepath.Join(home, ".config", "formae")
	writeFile(t, legacy, filepath.Join("profiles", "default.pkl"), "x")
	writeFile(t, filepath.Join(xdg, "formae"), filepath.Join("profiles", "default.pkl"), "x")

	got, err := store.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != legacy {
		t.Errorf("got %q, want legacy %q (legacy must win when both populated)", got, legacy)
	}
}

func TestResolveConfigDir_PopulatedLegacyBeatsEmptyXDG(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	legacy := filepath.Join(home, ".config", "formae")
	writeFile(t, legacy, filepath.Join("profiles", "default.pkl"), "x")
	// $XDG/formae exists but is empty-ish: a bare empty profiles/ dir.
	if err := os.MkdirAll(filepath.Join(xdg, "formae", "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := store.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != legacy {
		t.Errorf("got %q, want legacy %q (empty profiles/ is not user config)", got, legacy)
	}
}

func TestResolveConfigDir_XDGWhenLegacyUnpopulated(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	xdgFormae := filepath.Join(xdg, "formae")
	writeFile(t, xdgFormae, filepath.Join("profiles", "default.pkl"), "x")
	got, err := store.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != xdgFormae {
		t.Errorf("got %q, want xdg %q", got, xdgFormae)
	}
}

func TestResolveConfigDir_FreshMachineHonorsXDG(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, err := store.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != filepath.Join(xdg, "formae") {
		t.Errorf("got %q, want %q", got, filepath.Join(xdg, "formae"))
	}
}

func TestResolveConfigDir_FreshMachineNoXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	got, err := store.ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != filepath.Join(home, ".config", "formae") {
		t.Errorf("got %q, want %q", got, filepath.Join(home, ".config", "formae"))
	}
}
