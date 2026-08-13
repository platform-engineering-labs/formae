// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gofrs/flock"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

// The managed-profile ledger records the profiles `formae login` wrote, and is
// the only thing that authorises removing or replacing one. "This profile
// points at a hosted installation" is a different fact from "login wrote this
// file and may remove it": hosted profiles get copied, hand-edited, and
// hand-authored, so the file's own contents can never license its deletion.
// No valid entry, no deletion.
//
// Every field of an entry eventually names a path or licenses a removal, so
// every field is validated on load. An entry that fails validation is garbage:
// it is dropped, warned about, and not written back. Entries that are
// individually well-formed but collide with each other are quarantined
// instead — kept, warned about, and believed by nobody.

// ledgerSchemaVersion is the only schema version this formae understands.
const ledgerSchemaVersion = 1

var (
	// errUnknownSchemaVersion reports a ledger written by a formae that owns
	// records this one cannot read. The caller must then write nothing, prune
	// nothing, and create nothing.
	errUnknownSchemaVersion = errors.New("unrecognised managed-profile ledger schema version")

	// errLedgerLocked reports that another formae process holds the ledger
	// lock. It is distinct from a failure to lock at all, because "wait and
	// retry" and "something is wrong with this machine" are different answers.
	errLedgerLocked = errors.New("another formae process is updating managed profiles")
)

var (
	// installationRE matches the canonical lowercase UUID text form, the same
	// syntactic shape the hosted connection schema accepts as a routing key.
	installationRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// fingerprintRE matches a content fingerprint: a sha256 digest in lowercase hex.
	fingerprintRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

	// tempNameRE matches the basename of a publication temp file.
	tempNameRE = regexp.MustCompile(`^\.tmp-[0-9a-f]{16}\.pkl$`)
)

// entryState is where an entry sits in the publication lifecycle: intent
// committed before a file exists (pending), the file is ours (owned), or
// removal is committed and may be resumed (deleting).
type entryState string

const (
	entryPending  entryState = "pending"
	entryOwned    entryState = "owned"
	entryDeleting entryState = "deleting"
)

// ledgerEntry is one record: a profile file this formae wrote for one
// installation of one control plane.
type ledgerEntry struct {
	ControlPlane   string     `json:"controlPlane"`
	InstallationID string     `json:"installationId"`
	Name           string     `json:"name"`
	State          entryState `json:"state"`
	Fingerprint    string     `json:"fingerprint"`
	AltFingerprint string     `json:"altFingerprint,omitempty"`
	SupersedesName string     `json:"supersedesName,omitempty"`
	TempName       string     `json:"tempName,omitempty"`

	// quarantined marks an entry that collides with another one. It is
	// deliberately not serialised: quarantine is recomputed on every load from
	// the file's own contents, so it can never be asserted by the file itself
	// and can never be stale.
	quarantined bool
}

// ledgerFile is the ledger as it appears on disk. It exists so the ledger
// itself need not expose its entries: the wire shape is read by loadLedger
// and written by save, and nothing else sees it.
//
// The entries stay raw so each one can be decoded on its own. Decoding them
// in one piece would let a single wrongly typed field fail the whole file,
// which reads as an empty ledger and lets the next save destroy every record
// in it, including records for control planes this run never touched.
type ledgerFile struct {
	SchemaVersion int               `json:"schemaVersion"`
	Entries       []json.RawMessage `json:"entries"`
}

// ledger is the whole file: every entry to write back, quarantined ones
// included. The entries are unexported because holding one is authority over
// a file, and ranging over all of them would hand that authority to exactly
// the entries quarantine exists to strip it from. Authoritative is the only
// way to obtain an entry that may act on a file.
type ledger struct {
	entries []*ledgerEntry
}

// Authoritative returns only the entries that grant authority over a file.
// Everything that reads the ledger to decide whether it may remove, replace,
// or rename a profile must go through it.
func (l *ledger) Authoritative() []*ledgerEntry {
	out := make([]*ledgerEntry, 0, len(l.entries))
	for _, e := range l.entries {
		if !e.quarantined {
			out = append(out, e)
		}
	}
	return out
}

// carriedForward returns every entry the next save will write, quarantined
// ones included. It is the write-back set and not a set of permissions:
// nothing deciding whether it may remove, replace, or rename a profile may
// read it.
func (l *ledger) carriedForward() []*ledgerEntry {
	return slices.Clone(l.entries)
}

// upsert records e, replacing the entry for the same (controlPlane,
// installationId) when there is one and appending otherwise, so one
// installation never accumulates two records.
//
// Quarantined entries are invisible to it, as they are to remove: a
// conflicting set is carried forward exactly as it was found, and resolving
// a conflict by replacing one of its members is the choice quarantine exists
// to refuse. An entry appended alongside one joins the conflict, which costs
// a run nothing and grants nobody authority.
func (l *ledger) upsert(e *ledgerEntry) {
	for i, existing := range l.entries {
		if existing.quarantined {
			continue
		}
		if existing.ControlPlane == e.ControlPlane && existing.InstallationID == e.InstallationID {
			l.entries[i] = e
			return
		}
	}
	l.entries = append(l.entries, e)
}

// remove drops the entry for (controlPlane, installationId), so the file it
// named stops being ours. Absent that entry it does nothing.
func (l *ledger) remove(controlPlane, installationID string) {
	for i, e := range l.entries {
		if e.quarantined {
			continue
		}
		if e.ControlPlane == controlPlane && e.InstallationID == installationID {
			l.entries = slices.Delete(l.entries, i, i+1)
			return
		}
	}
}

// loadLedger reads, validates, and quarantines the ledger at path, returning
// warnings for everything it refused to believe.
//
// The three ways the file itself can fail are deliberately different. An
// absent file is a first run: an empty ledger and nothing to say. An
// unparseable file yields an empty ledger and one warning — safe by
// construction, because an empty ledger authorises no deletion at all, so the
// visible consequence is a skipped profile, never a lost one. An unrecognised
// schemaVersion is the one case that stops the caller: those records belong to
// a newer formae and rewriting them would destroy them, while creating a
// profile we could not record would leave a file we could never manage.
func loadLedger(path string) (*ledger, []string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ledger{}, nil, nil
	}
	if err != nil {
		// An unreadable file is not an empty one: continuing would overwrite
		// records we could not read.
		return nil, nil, fmt.Errorf("read managed-profile ledger %s: %w", path, err)
	}

	var file ledgerFile
	if err := json.Unmarshal(data, &file); err != nil {
		return &ledger{}, []string{fmt.Sprintf(
			"managed-profile ledger %s could not be read (%v); continuing as if it were empty, "+
				"so no profile will be removed by this run", path, err)}, nil
	}
	if file.SchemaVersion != ledgerSchemaVersion {
		return nil, nil, fmt.Errorf("%w: %s records version %d, this formae understands version %d",
			errUnknownSchemaVersion, path, file.SchemaVersion, ledgerSchemaVersion)
	}

	l := &ledger{}
	var warnings []string
	for _, raw := range file.Entries {
		e := &ledgerEntry{}
		if err := json.Unmarshal(raw, e); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"ignoring a managed-profile ledger entry in %s that could not be read (%v); "+
					"it authorises nothing and has been dropped", path, err))
			continue
		}
		if err := e.normalize(); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"ignoring managed-profile ledger entry for profile %q (installation %q): %v; "+
					"it authorises nothing and has been dropped", e.Name, e.InstallationID, err))
			continue
		}
		l.entries = append(l.entries, e)
	}

	return l, append(warnings, l.quarantineConflicts(path)...), nil
}

// normalize validates every field of the entry and, on success, rewrites
// ControlPlane to its canonical form so two spellings of one origin are one
// origin. It mutates nothing when it returns an error, since a rejected entry
// is dropped whole.
//
// name and supersedesName are both profile names, and fingerprint and
// altFingerprint are both hashes a file may legitimately match: supersedesName
// is the old name during a rename, altFingerprint the other acceptable content
// during a replacement. Both of each pair decide deletions, so both are
// checked.
func (e *ledgerEntry) normalize() error {
	origin, err := canonicalOrigin(e.ControlPlane)
	if err != nil {
		return fmt.Errorf("controlPlane: %w", err)
	}
	if !installationRE.MatchString(e.InstallationID) {
		return fmt.Errorf("installationId %q is not a canonical lowercase UUID", e.InstallationID)
	}
	if err := store.ValidateName(e.Name); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if e.SupersedesName != "" {
		if err := store.ValidateName(e.SupersedesName); err != nil {
			return fmt.Errorf("supersedesName: %w", err)
		}
	}
	if e.Fingerprint != "" && !fingerprintRE.MatchString(e.Fingerprint) {
		return fmt.Errorf("fingerprint %q is not a sha256 digest", e.Fingerprint)
	}
	if e.AltFingerprint != "" && !fingerprintRE.MatchString(e.AltFingerprint) {
		return fmt.Errorf("altFingerprint %q is not a sha256 digest", e.AltFingerprint)
	}
	if e.TempName != "" && !tempNameRE.MatchString(e.TempName) {
		return fmt.Errorf("tempName %q is not a publication temp file name", e.TempName)
	}
	switch e.State {
	case entryPending:
		// A pending entry names a file that need not exist yet, so it is the
		// one state with nothing to hash.
	case entryOwned, entryDeleting:
		// Both states name a file the entry expects to hash to fingerprint,
		// and the fingerprint is the whole proof that the file is ours.
		// Without one the entry would license removing or replacing a file on
		// its name alone.
		if e.Fingerprint == "" {
			return fmt.Errorf("state %q requires a fingerprint", e.State)
		}
	default:
		return fmt.Errorf("state %q is not one of %q, %q, %q", e.State, entryPending, entryOwned, entryDeleting)
	}

	e.ControlPlane = origin
	return nil
}

// quarantineConflicts marks every entry that shares a (controlPlane, name) or
// a (controlPlane, installationId) with another one, and returns one warning
// per conflicting set.
//
// The relation is "shares a name or an installation id with", so a conflicting
// set is a connected component and not a pair: an entry colliding with one
// entry by name and another by id ties all three together. Members are kept
// rather than dropped — they are probably ours, and dropping them would
// silently forfeit management of the files they name — but none of them is
// believed, because picking a winner would hand delete authority to whichever
// entry the file happened to list first.
func (l *ledger) quarantineConflicts(path string) []string {
	sets := newDisjointSets(len(l.entries))
	firstByName := make(map[string]int, len(l.entries))
	firstByInstallation := make(map[string]int, len(l.entries))
	for i, e := range l.entries {
		// A NUL separator cannot occur in a validated origin, name, or UUID,
		// so no pair of fields can be concatenated into another pair's key.
		nameKey := e.ControlPlane + "\x00" + e.Name
		if j, ok := firstByName[nameKey]; ok {
			sets.union(i, j)
		} else {
			firstByName[nameKey] = i
		}
		installationKey := e.ControlPlane + "\x00" + e.InstallationID
		if j, ok := firstByInstallation[installationKey]; ok {
			sets.union(i, j)
		} else {
			firstByInstallation[installationKey] = i
		}
	}

	members := make(map[int][]int, len(l.entries))
	for i := range l.entries {
		root := sets.find(i)
		members[root] = append(members[root], i)
	}

	var warnings []string
	for i := range l.entries {
		set := members[sets.find(i)]
		if len(set) < 2 || set[0] != i {
			continue // report each set once, at its first entry in file order.
		}
		labels := make([]string, 0, len(set))
		for _, m := range set {
			l.entries[m].quarantined = true
			labels = append(labels, fmt.Sprintf("%q (installation %s)", l.entries[m].Name, l.entries[m].InstallationID))
		}
		warnings = append(warnings, fmt.Sprintf(
			"managed-profile ledger entries for %s share a name or an installation id: %s. "+
				"None of them authorises removing or replacing a profile while they conflict; "+
				"remove %s to reset the ledger, which deletes no profile.",
			l.entries[i].ControlPlane, strings.Join(labels, ", "), path))
	}
	return warnings
}

// disjointSets is a union-find over entry indices, used to group conflicting
// entries into connected components.
type disjointSets struct {
	parent []int
}

func newDisjointSets(n int) *disjointSets {
	d := &disjointSets{parent: make([]int, n)}
	for i := range d.parent {
		d.parent[i] = i
	}
	return d
}

func (d *disjointSets) find(i int) int {
	for d.parent[i] != i {
		d.parent[i] = d.parent[d.parent[i]] // path halving.
		i = d.parent[i]
	}
	return i
}

func (d *disjointSets) union(a, b int) {
	rootA, rootB := d.find(a), d.find(b)
	if rootA != rootB {
		d.parent[rootA] = rootB
	}
}

// save writes the ledger to path through a unique temp file and a rename, so
// a crash or a concurrent writer can never leave a truncated ledger behind: a
// reader sees either the old file or the new one. Every carried-forward
// entry is written, quarantined ones and entries for other control planes
// included — a sync against one control plane must not disturb another's
// records.
//
// Every entry's control plane is canonicalised before it is written, so the
// invariants the conflict scan relies on hold by construction and not by the
// convention that callers build entries correctly. An entry whose control
// plane will not canonicalise is refused outright rather than written: the
// next load would drop it, leaving a profile file recorded by nothing and so
// managed by nobody.
func (l *ledger) save(path string) error {
	entries := make([]json.RawMessage, 0, len(l.entries))
	for _, e := range l.entries {
		origin, err := canonicalOrigin(e.ControlPlane)
		if err != nil {
			return fmt.Errorf("managed-profile ledger entry for profile %q: controlPlane: %w", e.Name, err)
		}
		e.ControlPlane = origin
		raw, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encode managed-profile ledger entry for profile %q: %w", e.Name, err)
		}
		entries = append(entries, raw)
	}
	data, err := json.MarshalIndent(ledgerFile{SchemaVersion: ledgerSchemaVersion, Entries: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed-profile ledger: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "managed-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp managed-profile ledger: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp managed-profile ledger: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp managed-profile ledger: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename managed-profile ledger: %w", err)
	}
	return nil
}

// lockLedger takes an exclusive lock on path, held until the caller unlocks
// it, so two formae processes cannot interleave ledger updates. The lock is
// taken without blocking: a second formae is told another one is running
// rather than left waiting behind a sign-in that may itself be waiting on a
// browser. A busy lock reports errLedgerLocked so the caller can distinguish
// it from a lock that could not be taken at all.
func lockLedger(path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir config dir: %w", err)
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s is held", errLedgerLocked, path)
	}
	return lock, nil
}
