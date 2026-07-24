//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupOmarchyHome builds a ~/.config/omarchy/current/theme -> themes/<name>
// layout under a temp HOME and returns (home, root). This matches the
// omarchyThemeDir() contract pinned by TestResolve_OmarchyRoutesToOmarchyResolver
// (Phase A): "current" is a stable directory; "theme" is the symlink that
// omarchy-theme-set atomically repoints.
func setupOmarchyHome(t *testing.T, initial string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, ".config", "omarchy")
	themes := filepath.Join(root, "themes")
	current := filepath.Join(root, "current")
	if err := os.MkdirAll(filepath.Join(themes, initial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeColors := func(name, accent string) {
		body := "background=\"#000000\"\nforeground=\"#ffffff\"\naccent=\"" + accent + "\"\ncolor1=\"#ff0000\"\ncolor2=\"#00ff00\"\ncolor3=\"#ffff00\"\ncolor8=\"#888888\"\n"
		if err := os.WriteFile(filepath.Join(themes, name, "colors.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeColors(initial, "#111111")
	// current/theme -> themes/<initial>
	if err := os.Symlink(filepath.Join(themes, initial), filepath.Join(current, "theme")); err != nil {
		t.Fatal(err)
	}
	return home, root
}

func drainOne(t *testing.T, w *OmarchyWatcher) *Theme {
	t.Helper()
	type res struct{ th *Theme }
	ch := make(chan res, 1)
	go func() {
		msg := w.WaitCmd()()
		at, _ := msg.(ApplyThemeMsg)
		ch <- res{at.Theme}
	}()
	select {
	case r := <-ch:
		return r.th
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not fire within 3s")
		return nil
	}
}

func TestOmarchyWatcher_InPlaceEdit(t *testing.T) {
	_, root := setupOmarchyHome(t, "alpha")
	w, err := NewOmarchyWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Edit the live colors.toml in place (current/theme/colors.toml is the
	// watched file; resolve the theme symlink to find its target directory).
	target, _ := filepath.EvalSymlinks(filepath.Join(root, "current", "theme"))
	if err := os.WriteFile(filepath.Join(target, "colors.toml"),
		[]byte("background=\"#010203\"\nforeground=\"#ffffff\"\naccent=\"#abcdef\"\ncolor1=\"#ff0000\"\ncolor2=\"#00ff00\"\ncolor3=\"#ffff00\"\ncolor8=\"#888888\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th := drainOne(t, w)
	if th == nil || th.Name != "omarchy" {
		t.Fatalf("expected an omarchy theme, got %+v", th)
	}
	if th.Palette.PrimaryAccent.Dark != "#abcdef" {
		t.Errorf("PrimaryAccent.Dark = %q, want #abcdef after edit", th.Palette.PrimaryAccent.Dark)
	}
}

func TestOmarchyWatcher_SymlinkSwap(t *testing.T) {
	_, root := setupOmarchyHome(t, "alpha")
	// Add a second theme "beta" with a distinct accent.
	themes := filepath.Join(root, "themes")
	_ = os.MkdirAll(filepath.Join(themes, "beta"), 0o755)
	_ = os.WriteFile(filepath.Join(themes, "beta", "colors.toml"),
		[]byte("background=\"#000000\"\nforeground=\"#ffffff\"\naccent=\"#beef00\"\ncolor1=\"#ff0000\"\ncolor2=\"#00ff00\"\ncolor3=\"#ffff00\"\ncolor8=\"#888888\"\n"), 0o644)

	w, err := NewOmarchyWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Atomic symlink swap: current/theme -> themes/beta (staged rename, like
	// omarchy-theme-set).
	tmp := filepath.Join(root, "current", "theme.tmp")
	_ = os.Symlink(filepath.Join(themes, "beta"), tmp)
	if err := os.Rename(tmp, filepath.Join(root, "current", "theme")); err != nil {
		t.Fatal(err)
	}
	th := drainOne(t, w)
	if th == nil || th.Palette.PrimaryAccent.Dark != "#beef00" {
		t.Fatalf("after symlink swap, PrimaryAccent = %v, want #beef00", th)
	}
}
