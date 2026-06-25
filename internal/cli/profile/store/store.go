// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package store manages named formae configuration profiles on disk.
//
// Layout under root:
//
//	root/
//	  formae.conf.pkl        (plain file; legacy symlink migrated by ensureInitialized)
//	  active                 (plain text pointer file: contains the active profile name)
//	  profiles/
//	    <name>.pkl
package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	configFileName = "formae.conf.pkl"
	profilesSubdir = "profiles"
	profileExt     = ".pkl"
	activeFileName = "active"
)

// Error sentinels returned by Store methods. Callers should match using errors.Is.
var (
	ErrInvalidName    = errors.New("invalid profile name")
	ErrNotInitialized = errors.New("not initialized")
	ErrNotFound       = errors.New("profile not found")
	ErrAlreadyExists  = errors.New("profile already exists")
	ErrIsActive       = errors.New("profile is active")
)

// StubTemplate is the minimal PKL content written by Create.
// Task 4 moves this to template.go and replaces it with the full stub.
const StubTemplate = "amends \"formae:/Config.pkl\"\n"

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateName checks that name is a permissible profile name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// Store provides operations on the formae profile directory rooted at root.
type Store struct {
	root string
}

// New returns a Store rooted at root. The directory does not have to exist yet.
func New(root string) *Store {
	return &Store{root: root}
}

// ConfigPath returns the path to the active config file.
func (s *Store) ConfigPath() string {
	return filepath.Join(s.root, configFileName)
}

// ProfilePath returns the path to a profile file for the given name.
// It does not validate the name.
func (s *Store) ProfilePath(name string) string {
	return filepath.Join(s.root, profilesSubdir, name+profileExt)
}

func (s *Store) profilesDir() string {
	return filepath.Join(s.root, profilesSubdir)
}

func (s *Store) activePath() string {
	return filepath.Join(s.root, activeFileName)
}

// Active returns the name recorded in the active pointer file. PURE read:
// it does not migrate, bootstrap, or check that the named profile exists.
// Returns ErrNotInitialized if the pointer is absent, or ErrInvalidName if
// the stored name is malformed.
func (s *Store) Active() (string, error) {
	data, err := os.ReadFile(s.activePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotInitialized
		}
		return "", fmt.Errorf("read active: %w", err)
	}
	name := strings.TrimSpace(string(data))
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

// Resolve returns the path to the active profile file, running migration/
// bootstrap first. This is the config-load entry point.
func (s *Store) Resolve() (string, error) {
	if err := s.ensureInitialized(); err != nil {
		return "", err
	}
	name, err := s.Active()
	if err != nil {
		return "", err
	}
	return s.ProfilePath(name), nil
}

// List returns all profile names in sorted order. An absent profiles/ dir
// yields an empty slice (a clean store is not an error for introspection).
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.profilesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	names := make([]string, 0)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, profileExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(n, profileExt))
	}
	sort.Strings(names)
	return names, nil
}

// Use atomically points the active pointer file at <name>. Returns
// ErrNotFound if the profile does not exist.
func (s *Store) Use(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := s.ensureInitialized(); err != nil && !errors.Is(err, ErrNotInitialized) {
		return err
	}
	if _, err := os.Stat(s.ProfilePath(name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return fmt.Errorf("stat profile: %w", err)
	}
	return s.writeActive(name)
}

// writeActive atomically writes the active pointer file.
func (s *Store) writeActive(name string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	tmp := s.activePath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(name+"\n"), 0o644); err != nil {
		return fmt.Errorf("write temp active: %w", err)
	}
	if err := os.Rename(tmp, s.activePath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename active: %w", err)
	}
	return nil
}

// Save copies the resolved active profile to profiles/<name>.pkl. It does not
// switch to the new profile. Returns ErrAlreadyExists if the destination
// already exists and force is false. Saving the active profile under its own
// name is a no-op.
func (s *Store) Save(name string, force bool) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	active, err := s.Active()
	if err != nil {
		return err
	}
	src := s.ProfilePath(active)
	dst := s.ProfilePath(name)
	if src == dst {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		if !force {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target: %w", err)
	}
	return copyFile(src, dst)
}

// Create writes profiles/<name>.pkl from the embedded stub template. It does
// not change the active pointer. Returns ErrAlreadyExists if the profile
// exists and force is false.
func (s *Store) Create(name string, force bool) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := s.ensureInitialized(); err != nil && !errors.Is(err, ErrNotInitialized) {
		return err
	}
	dst := s.ProfilePath(name)
	if _, err := os.Lstat(dst); err == nil {
		if !force {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := os.MkdirAll(s.profilesDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir profiles: %w", err)
	}
	return os.WriteFile(dst, []byte(StubTemplate), 0o644)
}

// Delete removes profiles/<name>.pkl. Returns ErrIsActive if name is the
// currently active profile (the caller should switch first), or ErrNotFound
// if it does not exist.
func (s *Store) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	active, err := s.Active()
	if err != nil {
		return err
	}
	if name == active {
		return fmt.Errorf("%w: %s", ErrIsActive, name)
	}
	dst := s.ProfilePath(name)
	if err := os.Remove(dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return fmt.Errorf("remove profile: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}
	return nil
}

// ensureInitialized is implemented in Task 3. Temporary no-op.
func (s *Store) ensureInitialized() error { return nil }
