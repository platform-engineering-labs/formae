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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"time"

	"github.com/gofrs/flock"
)

const (
	configFileName = "formae.conf.pkl"
	profilesSubdir = "profiles"
	profileExt     = ".pkl"
	activeFileName = "active"

	managedLedgerFileName = "managed.json"
	managedLockFileName   = "managed.lock"
	initLockFileName      = "init.lock"
)

// Error sentinels returned by Store methods. Callers should match using errors.Is.
var (
	ErrInvalidName    = errors.New("invalid profile name")
	ErrNotInitialized = errors.New("not initialized")
	ErrNotFound       = errors.New("profile not found")
	ErrAlreadyExists  = errors.New("profile already exists")
	ErrIsActive       = errors.New("profile is active")
)

// initLockWait bounds how long initialization waits for a peer holding the
// lock; initLockRetry is how often it re-checks while waiting.
var (
	initLockWait  = 5 * time.Second
	initLockRetry = 10 * time.Millisecond
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

// initLockPath returns the lock file that serialises store initialization. It
// is deliberately not the managed-profile lock: `formae login` holds that one
// while it publishes profiles, and it would deadlock the moment initialization
// ran underneath it.
func (s *Store) initLockPath() string {
	return filepath.Join(s.root, initLockFileName)
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

// writeActive atomically writes the active pointer file using a unique temp
// file so concurrent calls cannot clobber each other's temp before rename.
func (s *Store) writeActive(name string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	f, err := os.CreateTemp(s.root, "active-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp active: %w", err)
	}
	tmp := f.Name()
	if _, err := f.WriteString(name + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp active: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp active: %w", err)
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
func (s *Store) ensureInitialized() error {
	// Several formae can start at once — an assistant issuing a batch of tool
	// calls on a fresh machine is the ordinary case, not a contrived one — and
	// they all load configuration through here. Deciding and then mutating
	// without serialising lets one lose a race it had in fact won: a migration
	// completed by the winner reads as a failure, and a pointer written by the
	// winner reads as a store that needs the user's hand.
	//
	// Best-effort by design. Where the lock cannot be taken — a read-only dir, a
	// filesystem without locking, a peer that outlives the wait — initialization
	// proceeds anyway. That is exactly the behaviour before this lock existed,
	// so a machine that cannot lock is never worse off than it was.
	if unlock, ok := s.lockInit(); ok {
		defer unlock()
	}
	return s.initialize()
}

// lockInit takes the initialization lock, waiting briefly for a peer that is
// already initializing: the work behind it is a handful of syscalls, so waiting
// beats racing. Reports whether the lock was taken.
func (s *Store) lockInit() (func(), bool) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return nil, false
	}
	lock := flock.New(s.initLockPath())
	ctx, cancel := context.WithTimeout(context.Background(), initLockWait)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, initLockRetry)
	if err != nil || !locked {
		return nil, false
	}
	return func() { _ = lock.Unlock() }, true
}

// initialize is ensureInitialized's decision, run under the lock above.
func (s *Store) initialize() error {
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
			if err := s.writeActive(name); err != nil {
				return err
			}
			return os.Remove(cfg) // Step 3.
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
				return fmt.Errorf("move legacy config: %w", err)
			}
			return s.writeActive("default")
		} else if err != nil {
			return fmt.Errorf("stat default profile: %w", err)
		}
		// Step 5b: collision — adopt existing default, keep bare file, warn.
		slog.Warn("formae.conf.pkl left untouched; profiles/default.pkl already exists — reconcile manually", "path", cfg)
		return s.writeActive("default")
	case !errors.Is(lerr, os.ErrNotExist):
		return fmt.Errorf("stat legacy config: %w", lerr)
	}

	// No usable formae.conf.pkl beyond this point.
	if _, err := os.Stat(s.ProfilePath("default")); err == nil {
		return s.writeActive("default") // Step 6: orphaned default (crash recovery).
	}
	if names, err := s.List(); err == nil && len(names) > 0 {
		// Step 7: other orphaned profiles, no default.
		return fmt.Errorf("%w: no active profile — run `formae profile use <name>` (available: %s)", ErrNotInitialized, strings.Join(names, ", "))
	}
	// Step 8: clean install — bootstrap from the stub.
	if err := os.MkdirAll(s.ProfilesDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir profiles: %w", err)
	}
	if err := os.WriteFile(s.ProfilePath("default"), []byte(StubTemplate), 0o644); err != nil {
		return fmt.Errorf("write default profile: %w", err)
	}
	return s.writeActive("default")
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

// ResolveExisting is Resolve for a caller that must not create anything: it
// resolves every store Resolve can resolve, and does none of the writing.
//
// Resolve initializes, which is right for a command about to use a config and
// wrong for one merely reading a preference out of it. The difference is
// load-bearing on a machine nobody has signed in on: the store Resolve creates
// there names a local agent, and that is a decision the user has not been asked
// about. Anything that only wants to look should not be the thing that makes it.
//
// The cases below mirror initialize's, in its order, minus its side effects. A
// version that only read the active pointer would have made a configured user's
// theme depend on some other command having migrated their legacy config first
// - and the read-only caller is exactly the one that cannot cause that
// migration.
func (s *Store) ResolveExisting() (string, error) {
	// A pointer that exists is decisive: Resolve refuses to rewrite a malformed
	// one and reports a dangling one rather than looking further, so neither may
	// fall through to the recovery cases.
	if name, err := s.Active(); err == nil {
		path := s.ProfilePath(name)
		if _, statErr := os.Stat(path); statErr != nil {
			return "", fmt.Errorf("%w: active profile %q not found", ErrNotInitialized, name)
		}
		return path, nil
	} else if !errors.Is(err, ErrNotInitialized) {
		return "", err
	}

	cfg := s.ConfigPath()
	if info, lerr := os.Lstat(cfg); lerr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// A legacy symlink already points at a profile, so reading it needs
			// no migration at all.
			if name, ok := s.validSymlinkTarget(); ok {
				return s.ProfilePath(name), nil
			}
		} else {
			// A bare legacy file is itself a readable config, and Resolve moves
			// it into profiles/ and points at it - unless a default is already
			// there. That collision is initialize's step 5b: it leaves the bare
			// file where it lies and adopts the default instead, so returning
			// the bare file here would resolve a config no command will use.
			if path := s.ProfilePath("default"); fileExists(path) {
				return path, nil
			}
			return cfg, nil
		}
	}

	if path := s.ProfilePath("default"); fileExists(path) {
		return path, nil // an orphaned default, which Resolve adopts.
	}
	return "", ErrNotInitialized
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
