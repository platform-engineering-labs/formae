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
