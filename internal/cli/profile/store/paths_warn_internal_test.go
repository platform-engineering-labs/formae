// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigDir_BothPopulatedEmitsWarning(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	legacy := filepath.Join(home, ".config", "formae")
	mkProfile := func(dir string) {
		p := filepath.Join(dir, "profiles", "default.pkl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkProfile(legacy)
	mkProfile(filepath.Join(xdg, "formae"))

	var buf bytes.Buffer
	old := warnTo
	warnTo = &buf
	defer func() { warnTo = old }()

	got, err := ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != legacy {
		t.Errorf("got %q, want legacy %q", got, legacy)
	}
	if !strings.Contains(buf.String(), "both") || !strings.Contains(buf.String(), "FORMAE_CONFIG_DIR") {
		t.Errorf("expected both-populated warning, got %q", buf.String())
	}
}

// On Linux, XDG_CONFIG_HOME is commonly set to $HOME/.config, making the xdg
// candidate the very same directory as legacy. That is a single populated dir,
// not a competing pair, so it must not emit the both-populated warning.
func TestResolveConfigDir_XDGEqualsLegacyNoWarning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	legacy := filepath.Join(home, ".config", "formae")
	p := filepath.Join(legacy, "profiles", "default.pkl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	old := warnTo
	warnTo = &buf
	defer func() { warnTo = old }()

	got, err := ResolveConfigDir()
	if err != nil {
		t.Fatalf("ResolveConfigDir: %v", err)
	}
	if got != legacy {
		t.Errorf("got %q, want legacy %q", got, legacy)
	}
	if buf.String() != "" {
		t.Errorf("expected no warning, got %q", buf.String())
	}
}
