// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package profiles manages named formae configuration profiles on disk.
//
// Layout under root:
//
//	root/
//	  formae.conf.pkl              -> profiles/<active>.pkl  (symlink)
//	  profiles/
//	    <name>.pkl
package profiles

import (
	"errors"
	"fmt"
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
)

// Error sentinels returned by Store methods. Callers should match using errors.Is.
var (
	ErrInvalidName        = errors.New("invalid profile name")
	ErrNotInitialized     = errors.New("not initialized")
	ErrAlreadyInitialized = errors.New("already initialized")
	ErrNoConfigFile       = errors.New("no config file")
	ErrNotFound           = errors.New("profile not found")
	ErrAlreadyExists      = errors.New("profile already exists")
	ErrIsActive           = errors.New("profile is active")
)

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

// ConfigPath returns the path to the active config symlink.
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

// Init converts a regular file at ConfigPath() into profiles/<name>.pkl and
// replaces the original path with a relative symlink. It is idempotent: if
// ConfigPath() is already a symlink, it returns ErrAlreadyInitialized.
// It returns ErrNoConfigFile if no file exists at ConfigPath().
func (s *Store) Init(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	cfg := s.ConfigPath()
	info, err := os.Lstat(cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNoConfigFile, cfg)
		}
		return fmt.Errorf("stat config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrAlreadyInitialized
	}
	if err := os.MkdirAll(s.profilesDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir profiles: %w", err)
	}
	dst := s.ProfilePath(name)
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := os.Rename(cfg, dst); err != nil {
		return fmt.Errorf("move config to profile: %w", err)
	}
	rel := filepath.Join(profilesSubdir, name+profileExt)
	if err := os.Symlink(rel, cfg); err != nil {
		// Best-effort rollback so the user can retry Init.
		if rbErr := os.Rename(dst, cfg); rbErr != nil {
			return fmt.Errorf("create symlink: %w (rollback also failed: %v)", err, rbErr)
		}
		return fmt.Errorf("create symlink: %w", err)
	}
	return nil
}

// Active returns the name of the active profile, or ErrNotInitialized if
// ConfigPath() is missing or is not a symlink.
func (s *Store) Active() (string, error) {
	info, err := os.Lstat(s.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotInitialized
		}
		return "", fmt.Errorf("stat config: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", ErrNotInitialized
	}
	target, err := os.Readlink(s.ConfigPath())
	if err != nil {
		return "", fmt.Errorf("readlink: %w", err)
	}
	base := filepath.Base(target)
	if !strings.HasSuffix(base, profileExt) {
		return "", fmt.Errorf("symlink target has unexpected extension: %s", target)
	}
	return strings.TrimSuffix(base, profileExt), nil
}

// List returns all profile names in sorted order.
// Returns ErrNotInitialized if the profiles directory does not exist.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.profilesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotInitialized
		}
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	var names []string
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

// Use atomically points the active config symlink at profiles/<name>.pkl.
// Returns ErrNotFound if the profile does not exist.
func (s *Store) Use(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dst := s.ProfilePath(name)
	if _, err := os.Stat(dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return fmt.Errorf("stat profile: %w", err)
	}
	cfg := s.ConfigPath()
	rel := filepath.Join(profilesSubdir, name+profileExt)
	tmp := cfg + ".tmp." + name
	// Clean up any stale temp from a previous failed run.
	_ = os.Remove(tmp)
	if err := os.Symlink(rel, tmp); err != nil {
		return fmt.Errorf("create temp symlink: %w", err)
	}
	if err := os.Rename(tmp, cfg); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename symlink: %w", err)
	}
	return nil
}
