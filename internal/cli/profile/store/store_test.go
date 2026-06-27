// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// writeActive sets the active pointer + a profile of the same name.
func writeActive(t *testing.T, root, name, content string) {
	t.Helper()
	writeFile(t, root, filepath.Join("profiles", name+".pkl"), content)
	writeFile(t, root, "active", name)
}

func TestValidateName(t *testing.T) {
	for _, n := range []string{"default", "local-dev", "load_test", "a", "AB-12_cd"} {
		if err := store.ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range []string{"", "with space", "has/slash", ".dotfile", "name.pkl", "../escape"} {
		if err := store.ValidateName(n); !errors.Is(err, store.ErrInvalidName) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestStorePaths(t *testing.T) {
	s := store.New("/root")
	if got, want := s.ConfigPath(), filepath.Join("/root", "formae.conf.pkl"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
	if got, want := s.ProfilePath("local-dev"), filepath.Join("/root", "profiles", "local-dev.pkl"); got != want {
		t.Errorf("ProfilePath = %q, want %q", got, want)
	}
}

func TestActive_ReadsPointer(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "prod", "prod-content")
	got, err := store.New(root).Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if got != "prod" {
		t.Errorf("Active = %q, want prod", got)
	}
}

func TestActive_NotInitialized(t *testing.T) {
	_, err := store.New(t.TempDir()).Active()
	if !errors.Is(err, store.ErrNotInitialized) {
		t.Errorf("Active uninitialized: %v, want ErrNotInitialized", err)
	}
}

func TestActive_RejectsInvalidStoredName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "active", "../escape")
	_, err := store.New(root).Active()
	if !errors.Is(err, store.ErrInvalidName) {
		t.Errorf("Active with bad stored name: %v, want ErrInvalidName", err)
	}
}

func TestUse_SwitchesActivePointer(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "default-content")
	writeFile(t, root, filepath.Join("profiles", "prod.pkl"), "prod-content")
	s := store.New(root)
	if err := s.Use("prod"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "active"))
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if strings.TrimSpace(string(data)) != "prod" {
		t.Errorf("active pointer = %q, want prod", string(data))
	}
}

func TestUse_ErrorsOnMissingProfile(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "x")
	err := store.New(root).Use("nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Use missing: %v, want ErrNotFound", err)
	}
}

func TestSave_CopiesActiveDoesNotSwitch(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "default-content")
	s := store.New(root)
	if err := s.Save("snapshot", false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(s.ProfilePath("snapshot"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != "default-content" {
		t.Errorf("snapshot content = %q", string(got))
	}
	active, _ := s.Active()
	if active != "default" {
		t.Errorf("active changed to %q, want default", active)
	}
}

func TestSave_RefusesOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "x")
	writeFile(t, root, filepath.Join("profiles", "snapshot.pkl"), "old")
	err := store.New(root).Save("snapshot", false)
	if !errors.Is(err, store.ErrAlreadyExists) {
		t.Errorf("Save existing: %v, want ErrAlreadyExists", err)
	}
}

func TestCreate_WritesStubDoesNotSwitch(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "default-content")
	s := store.New(root)
	if err := s.Create("staging", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := os.ReadFile(s.ProfilePath("staging"))
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}
	if !strings.Contains(string(got), `amends "formae:/Config.pkl"`) {
		t.Errorf("staging not from stub template: %q", string(got))
	}
	active, _ := s.Active()
	if active != "default" {
		t.Errorf("Create switched active to %q", active)
	}
}

func TestCreate_RefusesOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("profiles", "staging.pkl"), "existing")
	err := store.New(root).Create("staging", false)
	if !errors.Is(err, store.ErrAlreadyExists) {
		t.Errorf("Create existing: %v, want ErrAlreadyExists", err)
	}
}

func TestDelete_RefusesActive(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "x")
	err := store.New(root).Delete("default")
	if !errors.Is(err, store.ErrIsActive) {
		t.Errorf("Delete active: %v, want ErrIsActive", err)
	}
}

func TestList_SortedAndFiltered(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"prod", "default", "local-dev"} {
		writeFile(t, root, filepath.Join("profiles", n+".pkl"), "x")
	}
	writeFile(t, root, filepath.Join("profiles", "README.md"), "x")
	got, err := store.New(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if strings.Join(got, ",") != "default,local-dev,prod" {
		t.Errorf("List = %v", got)
	}
}

func TestList_EmptyWhenNoProfilesDir(t *testing.T) {
	got, err := store.New(t.TempDir()).List()
	if err != nil {
		t.Fatalf("List on clean store: %v, want nil", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("List on clean store = %v, want empty slice", got)
	}
}

// Migration is driven by Resolve() (the config-load path). Active() is a pure
// read and never bootstraps, so these tests call Resolve().

func activeName(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "active"))
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestEnsure_Step1_ValidActiveNoOp(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "content")
	s := store.New(root)
	path, err := s.Resolve()
	if err != nil || path != s.ProfilePath("default") {
		t.Fatalf("Resolve = %q, %v; want %q, nil", path, err, s.ProfilePath("default"))
	}
	if _, err := os.Lstat(filepath.Join(root, "formae.conf.pkl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("formae.conf.pkl should not exist")
	}
}

func TestEnsure_Step2_StaleActiveErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "active", "ghost") // valid name, profile missing
	_, err := store.New(root).Resolve()
	if !errors.Is(err, store.ErrNotInitialized) {
		t.Errorf("stale active: %v, want ErrNotInitialized", err)
	}
	if activeName(t, root) != "ghost" {
		t.Errorf("stale active was rewritten")
	}
}

func TestEnsure_MalformedActiveIsInvalidName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "active", "../escape")
	_, err := store.New(root).Resolve()
	if !errors.Is(err, store.ErrInvalidName) {
		t.Errorf("malformed active: %v, want ErrInvalidName", err)
	}
	if activeName(t, root) != "../escape" {
		t.Errorf("malformed active was rewritten")
	}
}

func TestEnsure_Step3_ValidSymlinkAdoptsAndRemoves(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("profiles", "prod.pkl"), "prod-content")
	// formae.conf.pkl -> profiles/prod.pkl (relative symlink).
	if err := os.Symlink(filepath.Join("profiles", "prod.pkl"), filepath.Join(root, "formae.conf.pkl")); err != nil {
		t.Fatal(err)
	}
	s := store.New(root)
	path, err := s.Resolve()
	if err != nil || path != s.ProfilePath("prod") {
		t.Fatalf("Resolve = %q, %v; want prod path", path, err)
	}
	if activeName(t, root) != "prod" {
		t.Errorf("active = %q, want prod", activeName(t, root))
	}
	if _, err := os.Lstat(filepath.Join(root, "formae.conf.pkl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink should have been removed")
	}
}

func TestEnsure_Step4_BrokenSymlinkLeftThenBootstrap(t *testing.T) {
	root := t.TempDir()
	// Dangling symlink to a nonexistent target.
	if err := os.Symlink(filepath.Join("profiles", "ghost.pkl"), filepath.Join(root, "formae.conf.pkl")); err != nil {
		t.Fatal(err)
	}
	s := store.New(root)
	path, err := s.Resolve()
	if err != nil || path != s.ProfilePath("default") {
		t.Fatalf("Resolve = %q, %v; want default (bootstrap)", path, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "formae.conf.pkl")); err != nil {
		t.Errorf("broken symlink should be left in place: %v", err)
	}
}

func TestEnsure_Step5a_BareFileMoved(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "real-config")
	s := store.New(root)
	if _, err := s.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	moved, err := os.ReadFile(s.ProfilePath("default"))
	if err != nil || string(moved) != "real-config" {
		t.Fatalf("profiles/default.pkl = %q, %v; want real-config", string(moved), err)
	}
	if activeName(t, root) != "default" {
		t.Errorf("active = %q, want default", activeName(t, root))
	}
	if _, err := os.Lstat(filepath.Join(root, "formae.conf.pkl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("formae.conf.pkl should have been moved away")
	}
}

func TestEnsure_Step5b_BareFileCollisionAdoptsAndKeeps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "formae.conf.pkl", "bare")
	writeFile(t, root, filepath.Join("profiles", "default.pkl"), "existing-default")
	s := store.New(root)
	if _, err := s.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if activeName(t, root) != "default" {
		t.Errorf("active = %q, want default", activeName(t, root))
	}
	d, _ := os.ReadFile(s.ProfilePath("default"))
	if string(d) != "existing-default" {
		t.Errorf("default overwritten: %q", string(d))
	}
	if _, err := os.Lstat(filepath.Join(root, "formae.conf.pkl")); err != nil {
		t.Errorf("bare formae.conf.pkl should be left: %v", err)
	}
}

func TestEnsure_Step6_OrphanedDefaultRecovered(t *testing.T) {
	root := t.TempDir()
	// Simulate crash after move but before active write.
	writeFile(t, root, filepath.Join("profiles", "default.pkl"), "recovered")
	s := store.New(root)
	path, err := s.Resolve()
	if err != nil || path != s.ProfilePath("default") {
		t.Fatalf("Resolve = %q, %v; want default (recovery)", path, err)
	}
	if activeName(t, root) != "default" {
		t.Errorf("active = %q, want default", activeName(t, root))
	}
}

func TestEnsure_Step7_OrphanedProfilesNoDefaultErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("profiles", "prod.pkl"), "x")
	_, err := store.New(root).Resolve()
	if !errors.Is(err, store.ErrNotInitialized) {
		t.Errorf("orphaned profiles: %v, want ErrNotInitialized", err)
	}
}

func TestEnsure_Step8_CleanInstallBootstraps(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	path, err := s.Resolve()
	if err != nil || path != s.ProfilePath("default") {
		t.Fatalf("Resolve = %q, %v; want default", path, err)
	}
	stub, err := os.ReadFile(s.ProfilePath("default"))
	if err != nil || !strings.Contains(string(stub), `amends "formae:/Config.pkl"`) {
		t.Fatalf("default not bootstrapped from stub: %q, %v", string(stub), err)
	}
}

func TestEnsure_Idempotent(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	if _, err := s.Resolve(); err != nil { // bootstrap
		t.Fatal(err)
	}
	first, _ := os.ReadFile(s.ProfilePath("default"))
	if _, err := s.Resolve(); err != nil { // run again
		t.Fatal(err)
	}
	second, _ := os.ReadFile(s.ProfilePath("default"))
	if string(first) != string(second) {
		t.Errorf("not idempotent: default changed between runs")
	}
}

// Fix 1: malformed active pointer must not wedge Use or Create.

func TestUse_ToleratesMalformedActivePointer(t *testing.T) {
	root := t.TempDir()
	// Write a malformed active pointer (invalid name).
	writeFile(t, root, "active", "../escape")
	// Write a valid profile that we want to switch to.
	writeFile(t, root, filepath.Join("profiles", "default.pkl"), "default-content")

	s := store.New(root)
	if err := s.Use("default"); err != nil {
		t.Fatalf("Use with malformed active: %v, want nil", err)
	}
	// Store must be healed: Active() now returns "default".
	got, err := s.Active()
	if err != nil {
		t.Fatalf("Active after healing: %v", err)
	}
	if got != "default" {
		t.Errorf("Active = %q after Use, want default", got)
	}
}

func TestCreate_ToleratesMalformedActivePointer(t *testing.T) {
	root := t.TempDir()
	// Write a malformed active pointer.
	writeFile(t, root, "active", "../escape")

	s := store.New(root)
	if err := s.Create("foo", false); err != nil {
		t.Fatalf("Create with malformed active: %v, want nil", err)
	}
	if _, err := os.Stat(s.ProfilePath("foo")); err != nil {
		t.Errorf("foo profile not created: %v", err)
	}
}

// Fix 2: writeActive leaves no leftover active-*.tmp files.

func TestWriteActive_NoLeftoverTempFiles(t *testing.T) {
	root := t.TempDir()
	writeActive(t, root, "default", "default-content")
	writeFile(t, root, filepath.Join("profiles", "prod.pkl"), "prod-content")

	s := store.New(root)
	if err := s.Use("prod"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	// Glob for any leftover active-*.tmp files.
	matches, err := filepath.Glob(filepath.Join(root, "active-*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files after Use: %v", matches)
	}
	// Verify the active pointer is correct.
	got, _ := s.Active()
	if got != "prod" {
		t.Errorf("Active = %q, want prod", got)
	}
}

// Fix 3: Create --force does not follow symlinks outside profiles/.

func TestCreate_ForceDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "sensitive.txt")
	outsideContent := "sensitive-content"
	if err := os.WriteFile(outsideFile, []byte(outsideContent), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	// Set up profiles/ dir with a symlink pointing outside.
	if err := os.MkdirAll(filepath.Join(root, "profiles"), 0o755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	symlinkPath := filepath.Join(root, "profiles", "target.pkl")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// Also set up a valid active profile so Create can proceed.
	writeActive(t, root, "default", "default-content")

	s := store.New(root)
	if err := s.Create("target", true); err != nil {
		t.Fatalf("Create force over symlink: %v", err)
	}

	// (a) The outside file must be unchanged.
	got, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(got) != outsideContent {
		t.Errorf("outside file modified: got %q, want %q", string(got), outsideContent)
	}

	// (b) profiles/target.pkl must now be a regular file (not a symlink).
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("profiles/target.pkl is still a symlink after Create --force")
	}
	if !info.Mode().IsRegular() {
		t.Errorf("profiles/target.pkl is not a regular file: mode=%v", info.Mode())
	}
	// Content must be the stub template.
	data, err := os.ReadFile(symlinkPath)
	if err != nil {
		t.Fatalf("read target profile: %v", err)
	}
	if !strings.Contains(string(data), `amends "formae:/Config.pkl"`) {
		t.Errorf("target profile not from stub template: %q", string(data))
	}
}

// Fix 4: Save --force does not follow symlinks outside profiles/.

func TestSave_ForceDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "sensitive.txt")
	outsideContent := "sensitive-content"
	if err := os.WriteFile(outsideFile, []byte(outsideContent), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	// Set up an active profile with known content.
	activeContent := "active-profile-content"
	writeActive(t, root, "default", activeContent)

	// Set up a symlink in profiles/ pointing outside.
	symlinkPath := filepath.Join(root, "profiles", "snap.pkl")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	s := store.New(root)
	if err := s.Save("snap", true); err != nil {
		t.Fatalf("Save force over symlink: %v", err)
	}

	// (a) The outside file must be unchanged.
	got, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(got) != outsideContent {
		t.Errorf("outside file modified: got %q, want %q", string(got), outsideContent)
	}

	// (b) profiles/snap.pkl must now be a regular file (not a symlink) with the active profile's content.
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat snap: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("profiles/snap.pkl is still a symlink after Save --force")
	}
	if !info.Mode().IsRegular() {
		t.Errorf("profiles/snap.pkl is not a regular file: mode=%v", info.Mode())
	}
	data, err := os.ReadFile(symlinkPath)
	if err != nil {
		t.Fatalf("read snap profile: %v", err)
	}
	if string(data) != activeContent {
		t.Errorf("snap content = %q, want %q", string(data), activeContent)
	}
}
