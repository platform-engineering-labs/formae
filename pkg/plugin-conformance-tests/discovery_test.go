// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTestDataDir(t *testing.T) {
	plugin := t.TempDir()

	t.Run("default", func(t *testing.T) {
		t.Setenv("FORMAE_TEST_TESTDATA_DIR", "")
		got := ResolveTestDataDir(plugin)
		want := filepath.Join(plugin, "testdata")
		if got != want {
			t.Fatalf("default: got %q, want %q", got, want)
		}
	})

	t.Run("relative override", func(t *testing.T) {
		t.Setenv("FORMAE_TEST_TESTDATA_DIR", "testdata/integration")
		got := ResolveTestDataDir(plugin)
		want := filepath.Join(plugin, "testdata/integration")
		if got != want {
			t.Fatalf("relative: got %q, want %q", got, want)
		}
	})

	t.Run("absolute override", func(t *testing.T) {
		abs := filepath.Join(t.TempDir(), "elsewhere")
		t.Setenv("FORMAE_TEST_TESTDATA_DIR", abs)
		got := ResolveTestDataDir(plugin)
		if got != abs {
			t.Fatalf("absolute: got %q, want %q", got, abs)
		}
	})
}

func TestFindPklProjectRoot(t *testing.T) {
	root := t.TempDir()
	testdata := filepath.Join(root, "testdata")
	subdir := filepath.Join(testdata, "integration", "deep")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testdata, "PklProject"), []byte("amends \"pkl:Project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("self contains PklProject", func(t *testing.T) {
		got := FindPklProjectRoot(testdata)
		if got != testdata {
			t.Fatalf("got %q, want %q", got, testdata)
		}
	})

	t.Run("walks up to ancestor", func(t *testing.T) {
		got := FindPklProjectRoot(subdir)
		if got != testdata {
			t.Fatalf("got %q, want %q", got, testdata)
		}
	})

	t.Run("returns empty when none found", func(t *testing.T) {
		isolated := t.TempDir()
		got := FindPklProjectRoot(isolated)
		if got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})
}

func TestDiscoverTestData_HonorsOverride(t *testing.T) {
	plugin := t.TempDir()
	custom := filepath.Join(plugin, "alt-testdata")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "thing.pkl"), []byte("foo = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FORMAE_TEST_TESTDATA_DIR", "alt-testdata")

	cases, err := DiscoverTestData(plugin)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("got %d cases, want 1", len(cases))
	}
	if cases[0].ResourceType != "thing" {
		t.Fatalf("resource type: got %q, want %q", cases[0].ResourceType, "thing")
	}
	wantPath := filepath.Join(custom, "thing.pkl")
	if cases[0].PKLFile != wantPath {
		t.Fatalf("PKLFile: got %q, want %q", cases[0].PKLFile, wantPath)
	}
}
