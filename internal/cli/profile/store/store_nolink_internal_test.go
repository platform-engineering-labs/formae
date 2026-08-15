// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The config dir lives wherever FORMAE_CONFIG_DIR points, and not every
// filesystem supports hard links — FAT and some FUSE mounts do not. Publishing
// falls back to an exclusive create there, so initialization must still work
// and must still write complete files.
func TestInitializationWorksWithoutHardLinks(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(t *testing.T, root string)
		wantActive string
	}{
		{
			name:       "clean install",
			setup:      func(t *testing.T, root string) {},
			wantActive: "default",
		},
		{
			name: "legacy config file",
			setup: func(t *testing.T, root string) {
				writeTestFile(t, filepath.Join(root, "formae.conf.pkl"), "amends \"formae:/Config.pkl\"\n")
			},
			wantActive: "default",
		},
		{
			name: "orphaned default",
			setup: func(t *testing.T, root string) {
				writeTestFile(t, filepath.Join(root, "profiles", "default.pkl"), "amends \"formae:/Config.pkl\"\n")
			},
			wantActive: "default",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := linkFile
			linkFile = func(string, string) error { return errors.ErrUnsupported }
			t.Cleanup(func() { linkFile = prev })

			root := t.TempDir()
			tc.setup(t, root)

			if _, err := New(root).Resolve(); err != nil {
				t.Fatalf("Resolve without hard links: %v", err)
			}

			active, err := New(root).Active()
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if active != tc.wantActive {
				t.Fatalf("active = %q, want %q", active, tc.wantActive)
			}

			// The fallback writes after creating, so the content has to land.
			body, err := os.ReadFile(New(root).ProfilePath(active))
			if err != nil {
				t.Fatalf("read %s: %v", active, err)
			}
			if !strings.Contains(string(body), "amends") {
				t.Fatalf("profile %s is empty or truncated: %q", active, body)
			}
		})
	}
}

// An existing file is never replaced on the fallback path either: adopting what
// is already there is the whole contract of createIfAbsent.
func TestCreateIfAbsentAdoptsAnExistingFileWithoutHardLinks(t *testing.T) {
	prev := linkFile
	linkFile = func(string, string) error { return errors.ErrUnsupported }
	t.Cleanup(func() { linkFile = prev })

	dir := t.TempDir()
	path := filepath.Join(dir, "active")
	writeTestFile(t, path, "prod\n")

	if err := createIfAbsent(path, []byte("default\n"), 0o600); err != nil {
		t.Fatalf("createIfAbsent: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "prod\n" {
		t.Fatalf("existing file was replaced: %q", body)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("staging left debris behind: %v", entries)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A failure partway through the fallback must take the destination back out.
// Leaving an empty file behind would be worse than failing: the next run sees
// it, reads "already there" as another process's work, and adopts debris that
// nothing repairs — so one transient error would make every config-loading
// command fail until someone deleted the file by hand.
func TestCreateIfAbsentLeavesNoDebrisWhenTheWriteFails(t *testing.T) {
	prevLink, prevCreate := linkFile, createExclusive
	linkFile = func(string, string) error { return errors.ErrUnsupported }
	// Hand back a read-only handle: the destination is created, and writing to
	// it then fails, which is the shape of a disk error partway through.
	createExclusive = func(path string, mode os.FileMode) (*os.File, error) {
		return os.OpenFile(path, os.O_RDONLY|os.O_CREATE|os.O_EXCL, mode)
	}
	t.Cleanup(func() { linkFile, createExclusive = prevLink, prevCreate })

	dir := t.TempDir()
	path := filepath.Join(dir, "active")

	if err := createIfAbsent(path, []byte("default\n"), 0o600); err == nil {
		t.Fatal("expected the write failure to surface")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed create left the destination behind: stat err = %v", err)
	}

	// And the next attempt, on a healthy filesystem, still succeeds.
	createExclusive = prevCreate
	if err := createIfAbsent(path, []byte("default\n"), 0o600); err != nil {
		t.Fatalf("retry after a failed create: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "default\n" {
		t.Fatalf("retry wrote %q", body)
	}
}
