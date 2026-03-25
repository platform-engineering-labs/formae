// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package ppm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading dst: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("permission mismatch: got %o, want %o", info.Mode().Perm(), 0644)
	}
}

func TestCopyDir(t *testing.T) {
	tmp := t.TempDir()

	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")

	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("bbb"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(srcDir, dstDir); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "a.txt"))
	if err != nil {
		t.Fatalf("reading a.txt: %v", err)
	}
	if string(got) != "aaa" {
		t.Errorf("a.txt content: got %q, want %q", got, "aaa")
	}

	got, err = os.ReadFile(filepath.Join(dstDir, "sub", "b.txt"))
	if err != nil {
		t.Fatalf("reading sub/b.txt: %v", err)
	}
	if string(got) != "bbb" {
		t.Errorf("sub/b.txt content: got %q, want %q", got, "bbb")
	}
}

func TestInstallExecutables(t *testing.T) {
	tmp := t.TempDir()

	prefix := filepath.Join(tmp, "prefix")
	homeDir := filepath.Join(tmp, "home")

	pluginsDir := filepath.Join(prefix, "formae", "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}
	copyTestExecutable(t, filepath.Join(pluginsDir, "my-plugin"))

	if err := os.WriteFile(filepath.Join(pluginsDir, "libfoo.so"), []byte("not a plugin"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pluginsDir, "readme.txt"), []byte("docs"), 0644); err != nil {
		t.Fatal(err)
	}

	var logged []string
	copier, err := NewPluginCopier(
		WithHomeDir(homeDir),
		WithLogger(func(format string, args ...any) { logged = append(logged, format) }),
	)
	if err != nil {
		t.Fatalf("NewPluginCopier: %v", err)
	}

	if err := copier.InstallExecutables(prefix, "1.2.3"); err != nil {
		t.Fatalf("InstallExecutables: %v", err)
	}

	destPath := filepath.Join(homeDir, ".pel", "formae", "plugins", "my-plugin", "v1.2.3", "my-plugin")
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("expected executable at %s: %v", destPath, err)
	}

	if _, err := os.Stat(filepath.Join(pluginsDir, "my-plugin")); !os.IsNotExist(err) {
		t.Errorf("expected source executable to be removed, got err=%v", err)
	}

	soDestDir := filepath.Join(homeDir, ".pel", "formae", "plugins", "libfoo.so")
	if _, err := os.Stat(soDestDir); !os.IsNotExist(err) {
		t.Errorf("expected .so file to be skipped, but found at %s", soDestDir)
	}

	txtDestDir := filepath.Join(homeDir, ".pel", "formae", "plugins", "readme.txt")
	if _, err := os.Stat(txtDestDir); !os.IsNotExist(err) {
		t.Errorf("expected non-executable to be skipped, but found at %s", txtDestDir)
	}

	if len(logged) == 0 {
		t.Error("expected log output for installed plugin")
	}
}

func TestInstallExecutables_EmptyDir(t *testing.T) {
	tmp := t.TempDir()

	prefix := filepath.Join(tmp, "prefix")
	homeDir := filepath.Join(tmp, "home")

	copier, err := NewPluginCopier(WithHomeDir(homeDir), WithLogger(func(string, ...any) {}))
	if err != nil {
		t.Fatalf("NewPluginCopier: %v", err)
	}

	if err := copier.InstallExecutables(prefix, "1.0.0"); err != nil {
		t.Fatalf("expected no error for missing dir, got: %v", err)
	}
}

func TestInstallResourcePlugins(t *testing.T) {
	tmp := t.TempDir()

	prefix := filepath.Join(tmp, "prefix")
	homeDir := filepath.Join(tmp, "home")

	rpDir := filepath.Join(prefix, "formae", "resource-plugins", "acme-widgets", "v2.0.0")
	if err := os.MkdirAll(rpDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rpDir, "formae-plugin.pkl"), []byte("manifest"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rpDir, "plugin-binary"), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}

	copier, err := NewPluginCopier(
		WithHomeDir(homeDir),
		WithLogger(func(string, ...any) {}),
	)
	if err != nil {
		t.Fatalf("NewPluginCopier: %v", err)
	}

	if err := copier.InstallResourcePlugins(prefix); err != nil {
		t.Fatalf("InstallResourcePlugins: %v", err)
	}

	destManifest := filepath.Join(homeDir, ".pel", "formae", "plugins", "acme-widgets", "v2.0.0", "formae-plugin.pkl")
	got, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatalf("expected manifest at %s: %v", destManifest, err)
	}
	if string(got) != "manifest" {
		t.Errorf("manifest content: got %q, want %q", got, "manifest")
	}

	destBin := filepath.Join(homeDir, ".pel", "formae", "plugins", "acme-widgets", "v2.0.0", "plugin-binary")
	if _, err := os.Stat(destBin); err != nil {
		t.Errorf("expected binary at %s: %v", destBin, err)
	}

	srcRP := filepath.Join(prefix, "formae", "resource-plugins")
	if _, err := os.Stat(srcRP); !os.IsNotExist(err) {
		t.Errorf("expected resource-plugins dir to be removed, got err=%v", err)
	}
}

func TestInstallResourcePlugins_EmptyDir(t *testing.T) {
	tmp := t.TempDir()

	prefix := filepath.Join(tmp, "prefix")
	homeDir := filepath.Join(tmp, "home")

	copier, err := NewPluginCopier(WithHomeDir(homeDir), WithLogger(func(string, ...any) {}))
	if err != nil {
		t.Fatalf("NewPluginCopier: %v", err)
	}

	if err := copier.InstallResourcePlugins(prefix); err != nil {
		t.Fatalf("expected no error for missing dir, got: %v", err)
	}
}

func copyTestExecutable(t *testing.T, dst string) {
	t.Helper()

	src, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no 'true' binary found on PATH: %v", err)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0755); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
}
