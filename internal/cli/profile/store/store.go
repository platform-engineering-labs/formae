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
//	  managed.json           (ledger of the profiles `formae login` wrote)
//	  managed.lock           (lock file serialising ledger updates)
//	  profiles/
//	    <name>.pkl
package store

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	managedLedgerFileName = "managed.json"
	managedLockFileName   = "managed.lock"
)

// Error sentinels returned by Store methods. Callers should match using errors.Is.
var (
	ErrInvalidName    = errors.New("invalid profile name")
	ErrNotInitialized = errors.New("not initialized")
	ErrNotFound       = errors.New("profile not found")
	ErrAlreadyExists  = errors.New("profile already exists")
	ErrIsActive       = errors.New("profile is active")
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

// ConfigPath returns the path to the active config file.
func (s *Store) ConfigPath() string {
	return filepath.Join(s.root, configFileName)
}

// ProfilePath returns the path to a profile file for the given name.
// It does not validate the name.
func (s *Store) ProfilePath(name string) string {
	return filepath.Join(s.root, profilesSubdir, name+profileExt)
}

// ProfilesDir returns the directory holding the profile files.
func (s *Store) ProfilesDir() string {
	return filepath.Join(s.root, profilesSubdir)
}

// ManagedLedgerPath returns the path to the managed-profile ledger, the record
// of the profiles `formae login` wrote. It sits beside profiles/ rather than
// inside it so it is never mistaken for a profile.
func (s *Store) ManagedLedgerPath() string {
	return filepath.Join(s.root, managedLedgerFileName)
}

// ManagedLockPath returns the path to the lock file that serialises updates to
// the managed-profile ledger.
func (s *Store) ManagedLockPath() string {
	return filepath.Join(s.root, managedLockFileName)
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
//
// A .pkl file whose stem is not a valid profile name is not listed: no
// command can use, save, or delete it by name, so reporting it as a profile
// only invites a user to try. The files this excludes in practice are the
// dotfile temporaries `formae login` writes beside the profiles it
// publishes, which exist for the length of one publication and are not
// profiles at any point.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.ProfilesDir())
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
		name := strings.TrimSuffix(n, profileExt)
		if ValidateName(name) != nil {
			continue
		}
		names = append(names, name)
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
	if err := s.ensureInitialized(); err != nil &&
		!errors.Is(err, ErrNotInitialized) && !errors.Is(err, ErrInvalidName) {
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

// linkFile is os.Link, replaced in tests to exercise the fallback below.
var linkFile = os.Link

// createIfAbsent publishes content at path unless something is already there,
// and reports success either way: the caller wants the file to exist, not to be
// the one who made it.
//
// Hard-linking a staged file is the atomic test-and-create — it fails when the
// destination exists, so a concurrent explicit write always wins and a reader
// never sees a half-written file. The config dir can live wherever
// FORMAE_CONFIG_DIR points, including filesystems with no hard links (FAT, some
// FUSE mounts), so any other link failure falls back to an exclusive create.
// That is universal, at the cost of a brief window in which a racing reader
// could see an empty file. Refusing to start there is the worse trade.
func createIfAbsent(path string, content []byte, mode os.FileMode) error {
	tmp, err := stageTemp(filepath.Dir(path), content, mode)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()

	if err := linkFile(tmp, path); err == nil || errors.Is(err, os.ErrExist) {
		return nil
	}

	f, err := createExclusive(path, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	// The destination exists from here on, so any failure has to take it back
	// out. Leaving a half-written file would be worse than failing: the next
	// run sees it, treats "already there" as another process's work, and adopts
	// debris that nothing ever repairs.
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// createExclusive creates path and fails if it already exists. It is a variable
// so tests can exercise the write failure that must not leave debris behind.
var createExclusive = func(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}

// stageTemp writes content to a unique temp file in dir and returns its path,
// so publishing it is a single atomic step.
func stageTemp(dir string, content []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, ".staged-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	cleanup := func(err error) (string, error) {
		_ = os.Remove(tmp)
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return cleanup(err)
	}
	return tmp, nil
}

// writeActive atomically points the active pointer at name, replacing whatever
// it named before. This is someone choosing a profile, so it wins.
func (s *Store) writeActive(name string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	tmp, err := stageTemp(s.root, []byte(name+"\n"), 0o600)
	if err != nil {
		return fmt.Errorf("write temp active: %w", err)
	}
	if err := os.Rename(tmp, s.activePath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename active: %w", err)
	}
	return nil
}

// setActiveIfAbsent points the active pointer at name only when there is none.
// Every initialization branch that writes the pointer got there by observing it
// absent, so the write means "there should be a pointer", never "it should be
// this one". An explicit profile choice racing initialization therefore always
// wins, and is never quietly undone.
func (s *Store) setActiveIfAbsent(name string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	if err := createIfAbsent(s.activePath(), []byte(name+"\n"), 0o600); err != nil {
		return fmt.Errorf("write active: %w", err)
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
		// force: drop any existing entry (incl. a symlink) so copyFile writes a
		// fresh regular file inside profiles/ rather than following a link outside it.
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove existing profile: %w", err)
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
	if err := s.ensureInitialized(); err != nil &&
		!errors.Is(err, ErrNotInitialized) && !errors.Is(err, ErrInvalidName) {
		return err
	}
	dst := s.ProfilePath(name)
	if _, err := os.Lstat(dst); err == nil {
		if !force {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
		}
		// force: drop any existing entry (incl. a symlink) so we write a fresh
		// regular file inside profiles/ rather than following a link outside it.
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove existing profile: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := os.MkdirAll(s.ProfilesDir(), 0o755); err != nil {
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

// activeIsUsable reports whether the active pointer names a profile that
// exists, which is the state ensureInitialized is trying to reach.
func (s *Store) activeIsUsable() bool {
	name, err := s.Active()
	if err != nil {
		return false
	}
	_, statErr := os.Stat(s.ProfilePath(name))
	return statErr == nil
}

// writeStubProfile creates a starter profile if the machine has none. It
// appears complete or not at all, so a racing reader cannot load a half-written
// profile, and it never replaces an existing one.
func (s *Store) writeStubProfile(name string) error {
	if err := createIfAbsent(s.ProfilePath(name), []byte(StubTemplate), 0o644); err != nil {
		return fmt.Errorf("write default profile: %w", err)
	}
	return nil
}

// removeMigratedLegacy deletes a legacy config whose contents have been
// migrated. A concurrent formae removing it first is success: the file being
// gone is the whole point of the step.
func removeMigratedLegacy(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
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

// ensureInitialized establishes a usable active profile if one does not already
// exist. It is a totally-defined ordered decision: every
// reachable state maps to exactly one outcome. It never deletes a profile file
// and never overwrites an existing one; its two mutations (rename of the legacy
// file, write of the active pointer) are atomic. Returns ErrNotInitialized for
// the two states that need user action (stale active; orphaned profiles with no
// default).
//
// Concurrent callers are not serialised, and deliberately so: every mutation
// here is idempotent, and losing a race means the state this call wanted was
// produced by the winner. Each step therefore treats "already done" as done.
// Several formae processes starting at once is ordinary, so a lost race must
// not surface as a startup failure.
func (s *Store) ensureInitialized() error {
	// Step 1/2: an active pointer already exists.
	if data, err := os.ReadFile(s.activePath()); err == nil {
		name := strings.TrimSpace(string(data))
		if err := ValidateName(name); err != nil {
			return err // malformed/corrupt active pointer — never auto-rewrite (ErrInvalidName).
		}
		if _, statErr := os.Stat(s.ProfilePath(name)); statErr == nil {
			return nil // Step 1: valid active.
		}
		// Step 2: valid name, profile file missing — recoverable.
		return fmt.Errorf("%w: active profile %q not found — run `formae profile use <name>` or `formae profile list`", ErrNotInitialized, name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read active: %w", err)
	}

	cfg := s.ConfigPath()
	info, lerr := os.Lstat(cfg)
	switch {
	case lerr == nil && info.Mode()&os.ModeSymlink != 0:
		// Steps 3/4: legacy symlink.
		if name, ok := s.validSymlinkTarget(); ok {
			if err := s.setActiveIfAbsent(name); err != nil {
				return err
			}
			return removeMigratedLegacy(cfg) // Step 3.
		}
		// Step 4: broken/invalid symlink — leave it, warn, fall through.
		slog.Warn("ignoring broken legacy config symlink", "path", cfg)
	case lerr == nil:
		// Step 5: bare regular file.
		dst := s.ProfilePath("default")
		if _, err := os.Lstat(dst); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(s.ProfilesDir(), 0o755); err != nil {
				return fmt.Errorf("mkdir profiles: %w", err)
			}
			if err := os.Rename(cfg, dst); err != nil { // Step 5a.
				// A concurrent formae may have moved the same file first. That
				// is the outcome this step wanted, so adopt it instead of
				// failing a startup that merely raced.
				if _, statErr := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) || statErr != nil {
					return fmt.Errorf("move legacy config: %w", err)
				}
			}
			return s.setActiveIfAbsent("default")
		} else if err != nil {
			return fmt.Errorf("stat default profile: %w", err)
		}
		// Step 5b: collision — adopt existing default, keep bare file, warn.
		slog.Warn("formae.conf.pkl left untouched; profiles/default.pkl already exists — reconcile manually", "path", cfg)
		return s.setActiveIfAbsent("default")
	case !errors.Is(lerr, os.ErrNotExist):
		return fmt.Errorf("stat legacy config: %w", lerr)
	}

	// No usable formae.conf.pkl beyond this point. A concurrent formae may have
	// initialised the store since the check at the top of this function, so ask
	// again before deciding: adopting the winner's result beats racing it to a
	// second, possibly different, decision.
	if s.activeIsUsable() {
		return nil
	}
	if _, err := os.Stat(s.ProfilePath("default")); err == nil {
		return s.setActiveIfAbsent("default") // Step 6: orphaned default (crash recovery).
	}
	if names, err := s.List(); err == nil && len(names) > 0 {
		// The listing itself is evidence another formae is mid-flight: it may
		// have finished, or created the default but not yet pointed at it.
		// Either is Step 1 or Step 6 arriving late, not a store needing the
		// user's hand.
		if s.activeIsUsable() {
			return nil
		}
		if _, statErr := os.Stat(s.ProfilePath("default")); statErr == nil {
			return s.setActiveIfAbsent("default")
		}
		// Step 7: other orphaned profiles, no default.
		return fmt.Errorf("%w: no active profile — run `formae profile use <name>` (available: %s)", ErrNotInitialized, strings.Join(names, ", "))
	}
	// Step 8: clean install — bootstrap from the stub.
	if err := os.MkdirAll(s.ProfilesDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir profiles: %w", err)
	}
	if err := s.writeStubProfile("default"); err != nil {
		return err
	}
	return s.setActiveIfAbsent("default")
}

// validSymlinkTarget returns the profile name a valid legacy symlink points at.
// A target is valid only if it resolves to an existing profiles/<name>.pkl with
// a valid name.
func (s *Store) validSymlinkTarget() (string, bool) {
	target, err := os.Readlink(s.ConfigPath())
	if err != nil {
		return "", false
	}
	base := filepath.Base(target)
	if !strings.HasSuffix(base, profileExt) {
		return "", false
	}
	name := strings.TrimSuffix(base, profileExt)
	if ValidateName(name) != nil {
		return "", false
	}
	// Confirm the target is under profiles/ and exists.
	if filepath.Dir(target) != profilesSubdir && filepath.Dir(target) != s.ProfilesDir() {
		return "", false
	}
	if _, err := os.Stat(s.ProfilePath(name)); err != nil {
		return "", false
	}
	return name, true
}
