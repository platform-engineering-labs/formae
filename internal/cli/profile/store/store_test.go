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
