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
	"path/filepath"
	"regexp"
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
