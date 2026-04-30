// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profiles_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
)

// writeFile writes data to root/relpath, creating parent directories as needed.
func writeFile(t *testing.T, root, relpath, data string) {
	t.Helper()
	full := filepath.Join(root, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"default", "local-dev", "load_test", "prod", "a", "AB-12_cd"}
	for _, n := range good {
		if err := profiles.ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "with space", "has/slash", ".dotfile", "name.pkl", "with$"}
	for _, n := range bad {
		if err := profiles.ValidateName(n); !errors.Is(err, profiles.ErrInvalidName) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestStorePaths(t *testing.T) {
	s := profiles.New("/root")
	if got, want := s.ConfigPath(), filepath.Join("/root", "formae.conf.pkl"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
	if got, want := s.ProfilePath("local-dev"), filepath.Join("/root", "profiles", "local-dev.pkl"); got != want {
		t.Errorf("ProfilePath = %q, want %q", got, want)
	}
}

func TestInit_MigratesRegularFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "amends \"formae:/Config.pkl\"\n")
	s := profiles.New(root)

	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Original path is now a symlink.
	info, err := os.Lstat(s.ConfigPath())
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink at %s", s.ConfigPath())
	}
	// Symlink target is relative.
	target, err := os.Readlink(s.ConfigPath())
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("symlink target %q is absolute, want relative", target)
	}
	// Profile file exists with original contents.
	got, err := os.ReadFile(s.ProfilePath("default"))
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if string(got) != "amends \"formae:/Config.pkl\"\n" {
		t.Errorf("profile contents = %q", string(got))
	}
}

func TestInit_IdempotentOnSymlink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	err := s.Init("default")
	if !errors.Is(err, profiles.ErrAlreadyInitialized) {
		t.Errorf("second Init: %v, want ErrAlreadyInitialized", err)
	}
}

func TestInit_ErrorsWhenConfigMissing(t *testing.T) {
	root := t.TempDir()
	s := profiles.New(root)

	err := s.Init("default")
	if !errors.Is(err, profiles.ErrNoConfigFile) {
		t.Errorf("Init with missing config: %v, want ErrNoConfigFile", err)
	}
}

func TestInit_RejectsInvalidName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)

	err := s.Init("bad name")
	if !errors.Is(err, profiles.ErrInvalidName) {
		t.Errorf("Init bad name: %v, want ErrInvalidName", err)
	}
}

func TestActive_AfterInit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := s.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if got != "default" {
		t.Errorf("Active = %q, want default", got)
	}
}

func TestActive_NotInitialized(t *testing.T) {
	root := t.TempDir()
	s := profiles.New(root)

	_, err := s.Active()
	if !errors.Is(err, profiles.ErrNotInitialized) {
		t.Errorf("Active uninitialized: %v, want ErrNotInitialized", err)
	}

	// Also: regular file (not a symlink) is treated as not-initialized.
	writeFile(t, root, "formae.conf.pkl", "x")
	_, err = s.Active()
	if !errors.Is(err, profiles.ErrNotInitialized) {
		t.Errorf("Active on regular file: %v, want ErrNotInitialized", err)
	}
}

func TestList_ReturnsSortedNames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Plant a few additional profiles.
	for _, n := range []string{"prod", "load-test", "local-dev"} {
		writeFile(t, root, filepath.Join("profiles", n+".pkl"), "x")
	}
	// Plant a non-pkl file to confirm filtering.
	writeFile(t, root, filepath.Join("profiles", "README.md"), "x")

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"default", "load-test", "local-dev", "prod"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestList_NotInitialized(t *testing.T) {
	root := t.TempDir()
	s := profiles.New(root)

	_, err := s.List()
	if !errors.Is(err, profiles.ErrNotInitialized) {
		t.Errorf("List uninitialized: %v, want ErrNotInitialized", err)
	}
}

func TestList_FiltersNonRegularEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Plant a stray symlink with a .pkl name in profiles/.
	link := filepath.Join(root, "profiles", "stray.pkl")
	if err := os.Symlink(s.ProfilePath("default"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, n := range got {
		if n == "stray" {
			t.Errorf("List included symlink-only entry %q", n)
		}
	}
}

func TestUse_SwitchesActive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "default-content")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeFile(t, root, filepath.Join("profiles", "prod.pkl"), "prod-content")

	if err := s.Use("prod"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	got, err := s.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if got != "prod" {
		t.Errorf("Active = %q, want prod", got)
	}
	// Reading the config path should yield the prod content.
	data, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != "prod-content" {
		t.Errorf("active content = %q, want prod-content", string(data))
	}
}

func TestUse_ErrorsOnMissingProfile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	err := s.Use("does-not-exist")
	if !errors.Is(err, profiles.ErrNotFound) {
		t.Errorf("Use missing: %v, want ErrNotFound", err)
	}
}

func TestUse_RejectsInvalidName(t *testing.T) {
	s := profiles.New(t.TempDir())
	err := s.Use("bad name")
	if !errors.Is(err, profiles.ErrInvalidName) {
		t.Errorf("Use bad name: %v, want ErrInvalidName", err)
	}
}

func TestSave_CopiesActiveAndDoesNotSwitch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "default-content")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := s.Save("snapshot", false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// New profile exists with copied contents.
	got, err := os.ReadFile(s.ProfilePath("snapshot"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != "default-content" {
		t.Errorf("snapshot content = %q", string(got))
	}
	// Active is still default.
	active, err := s.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != "default" {
		t.Errorf("Active = %q, want default", active)
	}
}

func TestSave_RefusesOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Plant a separate profile; saving over it without --force must fail.
	writeFile(t, root, filepath.Join("profiles", "snapshot.pkl"), "old")

	err := s.Save("snapshot", false)
	if !errors.Is(err, profiles.ErrAlreadyExists) {
		t.Errorf("Save existing: %v, want ErrAlreadyExists", err)
	}
}

func TestSave_ActiveOverItselfWithForceIsNoOp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "important-content")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Saving the active profile under its own name with --force used to
	// truncate the file via os.Create on the same inode. Now it is a no-op.
	if err := s.Save("default", true); err != nil {
		t.Fatalf("Save active=force: %v", err)
	}
	got, err := os.ReadFile(s.ProfilePath("default"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "important-content" {
		t.Errorf("active profile content lost: got %q", string(got))
	}
}

func TestSave_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "new-content")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Plant another profile we will overwrite.
	writeFile(t, root, filepath.Join("profiles", "snapshot.pkl"), "old-snapshot")

	if err := s.Save("snapshot", true); err != nil {
		t.Fatalf("Save force: %v", err)
	}
	got, err := os.ReadFile(s.ProfilePath("snapshot"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != "new-content" {
		t.Errorf("snapshot content after force = %q", string(got))
	}
}

func TestSave_NotInitialized(t *testing.T) {
	root := t.TempDir()
	s := profiles.New(root)

	err := s.Save("snapshot", false)
	if !errors.Is(err, profiles.ErrNotInitialized) {
		t.Errorf("Save uninitialized: %v, want ErrNotInitialized", err)
	}
}

func TestDelete_RemovesProfile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeFile(t, root, filepath.Join("profiles", "prod.pkl"), "x")

	if err := s.Delete("prod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(s.ProfilePath("prod")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("profile still exists after delete: %v", err)
	}
}

func TestDelete_RefusesActive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	err := s.Delete("default")
	if !errors.Is(err, profiles.ErrIsActive) {
		t.Errorf("Delete active: %v, want ErrIsActive", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	err := s.Delete("nope")
	if !errors.Is(err, profiles.ErrNotFound) {
		t.Errorf("Delete missing: %v, want ErrNotFound", err)
	}
}

func TestList_EmptyDirReturnsEmptySlice(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "x")
	s := profiles.New(root)
	if err := s.Init("default"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Remove the default profile so the directory is empty of .pkl files.
	if err := os.Remove(s.ProfilePath("default")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Errorf("List() returned nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("List() = %v; want empty", got)
	}
}
