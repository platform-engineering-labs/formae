// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// Sync is the code that removes the user's files, so it is written around one
// rule: a file is removed or replaced only when the ledger holds a valid entry
// this formae wrote for it, the path is a regular file whose bytes hash to a
// fingerprint that entry records, it is not the active profile, and the reason
// is authorised — an authoritative snapshot proving the grant is gone, or an
// explicit logout. Every uncertainty resolves toward not deleting.
//
// The order of the steps is load-bearing:
//
//   - The lock is taken before enumeration and held for the whole run. Taken
//     after, two concurrent logins could enumerate at different times and the
//     later lock-holder could prune profiles the earlier one had just created
//     from a fresher snapshot. Holding it across a network call can block a
//     second login for the client's timeout, which is bounded and far cheaper
//     than a stale snapshot deleting live work.
//   - Recovery runs before anything else reads the filesystem, and it runs
//     even when this run's enumeration later fails. It acts under authority
//     committed by an earlier run — a deleting entry was only ever written
//     after that run held an authoritative snapshot or an explicit logout — so
//     it is correct for it to finish regardless. The "no writes, no deletions"
//     guarantee of a failed enumeration is about mutations derived from *this*
//     run's snapshot.
//   - Partial knowledge may add; only complete knowledge may subtract. A
//     non-authoritative snapshot still creates and repairs profiles, because
//     both are non-destructive; pruning and dropping stale entries are gated
//     on Snapshot.Authoritative.
//
// Every mutation is commit intent, act, commit result, so every interruption
// lands in a state the next run's recovery finishes. The invariant on return
// is therefore not that the ledger matches the filesystem: it is that every
// reachable state is one recovery converges from, and that none of them can
// delete or overwrite a file the user wrote.

// syncDeps is everything a sync needs from the outside world.
type syncDeps struct {
	Client   CloudClient
	Store    *store.Store
	Verifier profileVerifier
	Out      io.Writer
	TTY      bool
	Theme    *theme.Theme
}

// syncResult is what a run did and what the caller has to report. Fatal is set
// when the sync did not complete — a lock it could not take, a ledger it could
// not read or write, an enumeration that failed, or a filesystem error while
// acting on a profile — and is left nil when the run completed with skips,
// which are warnings and a steady state rather than a failure.
type syncResult struct {
	Created, Updated, Renamed, Pruned int
	Warnings                          []string
	DesiredCount                      int   // installations in this run's desired set
	DesiredSatisfied                  int   // of those, published or verified this run
	StaleManagedForOrigin             bool  // a managed profile we own still exists for this origin
	Fatal                             error // set when the sync did not complete

	// Published names the profiles this run wrote or brought up to date, in the
	// order it acted on them. The counts above cannot answer "which one", which
	// is what a store with no active pointer needs: something has to be pointed
	// at, and it must be the same something on every repeat.
	Published []string
}

// Installation states, exactly and lowercase, over the platform's own enum.
// Every value it defines is classified here: leaving one to the unrecognised
// bucket would produce a warning on every login forever for a state that is
// perfectly well understood upstream.
var (
	// desiredStates are the states a profile is written and maintained for.
	// requested and provisioning are not serving yet, and a profile is written
	// anyway because a harness polls for readiness through it. failed,
	// suspending, and suspended keep their profiles: the grant exists, and a
	// profile that fails to connect states the truth.
	desiredStates = map[string]bool{
		"requested": true, "provisioning": true, "active": true,
		"failed": true, "suspending": true, "suspended": true,
	}

	// terminalStates are pruned exactly like a vanished grant: nothing is
	// serving and nothing will. reaped is the one that can come back, and that
	// is safe because reactivation preserves the installation id, from which
	// the profile name is derived — the next login restores the same name.
	terminalStates = map[string]bool{
		"destroying": true, "destroyed": true, "reaped": true,
	}
)

// errIsActiveProfile reports a file that is the active profile. It is never a
// reason to fail, only a reason to leave a file alone.
var errIsActiveProfile = errors.New("it is the active profile")

// saveLedger is a package seam so a test can interrupt a run between
// committing an intent and committing its result, which is the boundary every
// row of the recovery table sits on and which cannot otherwise be reached
// without killing the process. Nothing but a test ever assigns it, so
// production always runs (*ledger).save.
var saveLedger = (*ledger).save

// syncProfiles derives and maintains one profile per installation the caller's
// grants cover, and removes the profiles whose grants are gone. It runs the
// whole sequence under the ledger lock.
//
// rawAuth is the source auth block the gate validated, carried here only so
// the warning naming the keys a generated profile does not carry can be
// emitted where the profiles are written. Its values are never printed.
func syncProfiles(ctx context.Context, d syncDeps, p platform, bearer string,
	auth cliAuthBlock, rawAuth json.RawMessage) syncResult {
	r := &syncRun{d: d, origin: p.Origin, keptActive: keptActiveAfterLostAccess}
	if !r.open() {
		return r.result
	}
	defer r.close()

	r.recoverEntries()
	if r.stopped {
		return r.finish()
	}

	snapshot, err := d.Client.ListInstallations(ctx, bearer)
	if err != nil {
		// A control plane that is down must never read as "all your grants
		// vanished", so the run ends here with no writes and no deletions.
		r.fail(fmt.Errorf("ask %s which installations your grants cover: %w", r.origin, err))
		return r.finish()
	}
	r.warnAll(snapshot.Warnings)

	desired, present, terminal, unrecognised := r.partition(snapshot.Installations)
	if len(desired) > 0 {
		if warning := unknownAuthKeysWarning(rawAuth); warning != "" {
			r.warn(warning)
		}
	}
	r.syncDesired(desired, auth)

	if snapshot.Authoritative {
		// Both the prune and the drop of entries whose file and installation
		// are both gone are one pass: an entry for an absent installation
		// whose file is already gone has nothing to remove and nothing left to
		// manage, which is the self-healing step 9 describes.
		r.prune(present, terminal, unrecognised)
	}
	return r.finish()
}

// pruneAll removes every profile recorded against origin, for logout. The
// user's explicit sign-out is the authority here, so no control plane is
// asked: an empty desired set makes every entry for the origin absent.
func pruneAll(d syncDeps, origin string) syncResult {
	r := &syncRun{d: d, origin: origin, keptActive: keptActiveAfterSignOut}
	if !r.open() {
		return r.result
	}
	defer r.close()

	r.recoverEntries()
	if r.stopped {
		return r.finish()
	}
	r.prune(nil, nil, nil)
	return r.finish()
}

// syncRun is one run's state: the lock it holds, the ledger it is authorised
// by, and what it has done so far.
type syncRun struct {
	d      syncDeps
	origin string
	lock   *flock.Flock
	ledger *ledger
	result syncResult

	// keptActive phrases the profile a prune leaves alone because it is the
	// active one. It is the caller's, because why that profile was due for
	// removal is the caller's fact and not something this run can see: a
	// sign-in found the grant gone, a sign-out gave the credential up. Saying
	// the wrong one tells the user their access vanished when it did not, and
	// sends them to a remedy that cannot remove the file.
	keptActive func(e *ledgerEntry) string

	// stopped means the run cannot continue safely — the ledger could not be
	// written, so nothing more may be acted on. A failure to act on one
	// profile is not one of these: the other profiles are independent of it.
	stopped bool

	// The active profile's identity, resolved once per run. activeErr holds a
	// failure to establish it, which is treated as "this may be the active
	// profile" everywhere.
	activeInfo     os.FileInfo
	activeErr      error
	activeResolved bool
}

// open takes the lock and loads the ledger, and reports whether the run may
// continue. It releases the lock itself when it does not, so the caller only
// defers close on the path that proceeds.
func (r *syncRun) open() bool {
	lock, err := lockLedger(r.d.Store.ManagedLockPath())
	if err != nil {
		r.fail(err)
		return false
	}
	r.lock = lock

	l, warnings, err := loadLedger(r.d.Store.ManagedLedgerPath())
	r.warnAll(warnings)
	if err != nil {
		if errors.Is(err, errUnknownSchemaVersion) {
			// Those records belong to a newer formae: rewriting them would
			// destroy them, and creating a profile we could not record would
			// leave a file we could never manage. Signing in still succeeded,
			// so this is a notice and not a failure.
			r.warn(fmt.Sprintf("%v; no profile was created, changed, or removed by this run", err))
		} else {
			r.fail(err)
		}
		r.close()
		return false
	}
	r.ledger = l
	return true
}

// close releases the lock. It is safe to call more than once.
func (r *syncRun) close() {
	if r.lock == nil {
		return
	}
	_ = r.lock.Unlock()
	r.lock = nil
}

// finish fills in the summary a caller needs and returns the result.
func (r *syncRun) finish() syncResult {
	r.result.StaleManagedForOrigin = r.staleManaged()
	return r.result
}

// staleManaged reports whether a profile this formae owns for this origin
// still exists on disk. It is not a measure of success: a profile kept because
// it is active, or left alone for an unrecognised state, satisfies it, which
// is why the desired set is counted separately.
func (r *syncRun) staleManaged() bool {
	if r.ledger == nil {
		return false
	}
	for _, e := range r.ledger.Authoritative() {
		if e.ControlPlane != r.origin || e.State != entryOwned {
			continue
		}
		if info, err := os.Lstat(r.d.Store.ProfilePath(e.Name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// warn records a user-facing warning. Each one names the profile or the
// installation it is about and why nothing was done to it.
func (r *syncRun) warn(warning string) {
	r.result.Warnings = append(r.result.Warnings, warning)
}

func (r *syncRun) warnAll(warnings []string) {
	r.result.Warnings = append(r.result.Warnings, warnings...)
}

// fail records that the sync did not complete. The first failure is the one
// reported, since later ones are usually its consequences.
func (r *syncRun) fail(err error) {
	if r.result.Fatal == nil {
		r.result.Fatal = err
	}
}

// halt records a failure the run cannot continue past.
func (r *syncRun) halt(err error) {
	r.fail(err)
	r.stopped = true
}

// ack reports one change to the user, in the same idiom as the sign-in lines
// that precede it.
func (r *syncRun) ack(text string) {
	if r.d.Out == nil {
		return
	}
	ackLine(r.d.Out, r.d.TTY, r.d.Theme, components.AckDone, text)
}

// commit writes the ledger, and reports whether the run may continue. A failed
// write means the record of what this run is doing is not on disk, so nothing
// more may be acted on: the run stops and the next one's recovery finishes
// whatever was outstanding.
func (r *syncRun) commit() bool {
	if err := saveLedger(r.ledger, r.d.Store.ManagedLedgerPath()); err != nil {
		r.halt(fmt.Errorf("record managed profiles in %s: %w", r.d.Store.ManagedLedgerPath(), err))
		return false
	}
	return true
}

// drop removes an entry, so the file it named stops being ours, and commits.
func (r *syncRun) drop(e *ledgerEntry) bool {
	r.ledger.remove(e.ControlPlane, e.InstallationID)
	return r.commit()
}

// entryFor returns this origin's entry for an installation, or nil. Only
// entries that grant authority are visible here: a quarantined one authorises
// nothing, so for every decision this run takes it does not exist.
func (r *syncRun) entryFor(installationID string) *ledgerEntry {
	for _, e := range r.ledger.Authoritative() {
		if e.ControlPlane == r.origin && e.InstallationID == installationID {
			return e
		}
	}
	return nil
}

// activeFile returns the FileInfo of the active profile, or the error that
// stopped it being identified. Both are resolved once per run.
func (r *syncRun) activeFile() (os.FileInfo, error) {
	if r.activeResolved {
		return r.activeInfo, r.activeErr
	}
	r.activeResolved = true
	r.activeInfo, r.activeErr = activeProfileFile(r.d.Store)
	return r.activeInfo, r.activeErr
}

// activeProfileFile identifies the active profile, and is the one place the
// rules for doing so live: everything that decides whether it may act on a
// managed file compares against what this returns, so a second reading of
// "which file is the active one" cannot appear.
func activeProfileFile(s *store.Store) (os.FileInfo, error) {
	name, err := s.Active()
	if err != nil {
		return nil, fmt.Errorf("the active profile could not be identified: %w", err)
	}
	// Stat, not Lstat: the active profile may legitimately be reached through
	// a symlink, and it is the file at the end of it that a managed path would
	// be the same file as.
	info, err := os.Stat(s.ProfilePath(name))
	if err != nil {
		return nil, fmt.Errorf("the active profile %q could not be identified: %w", name, err)
	}
	return info, nil
}

// protectsActive reports why the file described by info must not be renamed or
// removed. Identity is decided by os.SameFile against the active profile's
// path and never by comparing names, and any failure to establish it resolves
// as "this may be the active profile": the natural implementation of a
// comparison returns false on error, which would resolve the ambiguity toward
// deleting.
func (r *syncRun) protectsActive(info os.FileInfo) error {
	activeInfo, err := r.activeFile()
	if err != nil {
		return err
	}
	if os.SameFile(activeInfo, info) {
		return errIsActiveProfile
	}
	return nil
}

// desiredRecord is one installation a profile is written and maintained for,
// with the name it derives.
type desiredRecord struct {
	installation Installation
	name         string
}

// partition sorts validated records into the set profiles are written for and
// the sets that decide pruning, and derives the desired set's names.
//
// present, terminal, and unrecognised are keyed by installation id. An
// unrecognised state is *present*: it is neither connectable nor gone, so
// nothing is created, updated, or pruned for it, while the rest of the run
// keeps its prune authority. Reading it the other way would let one state the
// platform adds later silently switch pruning off for every user.
func (r *syncRun) partition(installations []Installation) (
	desired []desiredRecord, present, terminal, unrecognised map[string]bool) {
	present = make(map[string]bool, len(installations))
	terminal = make(map[string]bool, len(installations))
	unrecognised = make(map[string]bool, len(installations))

	var records []desiredRecord
	for _, installation := range installations {
		present[installation.InstallationID] = true
		switch {
		case desiredStates[installation.State]:
			records = append(records, desiredRecord{
				installation: installation,
				name: deriveProfileName(installation.OrgName, installation.TenantName,
					installation.InstallationName, installation.InstallationID),
			})
		case terminalStates[installation.State]:
			terminal[installation.InstallationID] = true
		default:
			unrecognised[installation.InstallationID] = true
			r.warn(fmt.Sprintf(
				"installation %s is in state %q, which this formae does not understand; "+
					"its profile was left exactly as it is",
				installation.InstallationID, clip(installation.State, maxWarnedRunes)))
		}
	}
	r.result.DesiredCount = len(records)

	return r.withoutNameCollisions(records), present, terminal, unrecognised
}

// withoutNameCollisions drops every record whose derived name another record
// also derives, and warns once per colliding name.
//
// Both are skipped rather than one being chosen: there is no fact saying which
// installation the name belongs to, and writing one of them would give a user
// a profile for an installation picked by its position in a response. The
// existing profiles are left exactly as they are, and the run keeps its
// authority — the records are present, so nothing about them is unknown.
func (r *syncRun) withoutNameCollisions(records []desiredRecord) []desiredRecord {
	byName := make(map[string][]string, len(records))
	for _, record := range records {
		byName[record.name] = append(byName[record.name], record.installation.InstallationID)
	}

	kept := make([]desiredRecord, 0, len(records))
	warned := make(map[string]bool, len(records))
	for _, record := range records {
		ids := byName[record.name]
		if len(ids) < 2 {
			kept = append(kept, record)
			continue
		}
		if !warned[record.name] {
			warned[record.name] = true
			r.warn(fmt.Sprintf(
				"installations %s derive the same profile name %q, so formae wrote no profile for either; "+
					"any existing profile of that name was left exactly as it is",
				joinIDs(ids), record.name))
		}
	}
	return kept
}

// joinIDs renders installation ids for a warning.
func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		switch {
		case i == 0:
		case i == len(ids)-1:
			out += " and "
		default:
			out += ", "
		}
		out += id
	}
	return out
}

// syncDesired writes and maintains a profile for every desired installation,
// keyed on (origin, installation id) so a profile follows its installation
// rather than its name.
func (r *syncRun) syncDesired(records []desiredRecord, auth cliAuthBlock) {
	for _, record := range records {
		if r.stopped {
			return
		}
		e := r.entryFor(record.installation.InstallationID)
		switch {
		case e == nil:
			r.publishProfile(record, auth)
		case e.State != entryOwned:
			// Recovery could not settle this entry, so what the file at its
			// name is has not been established. Acting on it now would act on
			// an unestablished fact.
			r.warn(fmt.Sprintf(
				"profile %q is in the middle of a change formae could not finish, so it was left alone; "+
					"sign in again to complete it", e.Name))
		case e.Name == record.name:
			r.maintainProfile(e, record, auth)
		default:
			r.renameProfile(e, record, auth)
		}
	}
}

// publishProfile writes a profile for an installation that has none, and is
// also the repair path for one whose file is gone.
//
// The intent is committed before the temp file is created, so a temp file
// exists only if an entry names it — which is what lets a later run clean up
// temps without ever removing one on the strength of its filename.
func (r *syncRun) publishProfile(record desiredRecord, auth cliAuthBlock) {
	content := r.render(record, auth)
	tempName, err := newTempName()
	if err != nil {
		r.fail(err)
		r.warn(fmt.Sprintf("no profile was written for installation %s: %v",
			record.installation.InstallationID, err))
		return
	}

	e := &ledgerEntry{
		ControlPlane:   r.origin,
		InstallationID: record.installation.InstallationID,
		Name:           record.name,
		State:          entryPending,
		Fingerprint:    fingerprint(content),
		TempName:       tempName,
	}
	r.ledger.upsert(e)
	if !r.commit() {
		return
	}

	tempPath, err := r.writeTemp(tempName, content, record)
	if err != nil {
		r.abandonPublication(e, fmt.Sprintf(
			"no profile %q was written for installation %s: %v",
			record.name, record.installation.InstallationID, err))
		return
	}

	if err := publish(tempPath, r.d.Store.ProfilePath(record.name), content, generatedProfileMode); err != nil {
		message := fmt.Sprintf("no profile %q was written for installation %s: %v",
			record.name, record.installation.InstallationID, err)
		if errors.Is(err, errNameTaken) {
			// A name we do not own is the designed response to a file the user
			// wrote, not a failure to act.
			message = fmt.Sprintf(
				"a profile named %q already exists and formae did not write it, "+
					"so no profile was written for installation %s",
				record.name, record.installation.InstallationID)
		} else {
			r.fail(err)
		}
		r.abandonPublication(e, message)
		return
	}

	if !r.promote(e, e.Fingerprint) {
		return
	}
	r.result.Created++
	r.satisfied(record.name)
	r.ack("created profile " + record.name)
}

// maintainProfile brings the profile an entry names up to date, when the name
// it derives has not changed.
func (r *syncRun) maintainProfile(e *ledgerEntry, record desiredRecord, auth cliAuthBlock) {
	path := r.d.Store.ProfilePath(e.Name)
	info, digest, err := statAndDigest(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.publishProfile(record, auth) // repair.
		return
	case errors.Is(err, errNotRegularFile):
		r.warn(fmt.Sprintf(
			"profile %q is not a regular file, so formae neither changed nor removed it", e.Name))
		return
	case err != nil:
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q could not be read, so formae left it alone: %v", e.Name, err))
		return
	}

	if !e.records(digest) {
		r.adopted(e)
		return
	}
	r.applyContent(e, record, auth, info, digest)
}

// applyContent replaces the file an entry owns when the rendered bytes differ
// from what is on disk, and counts the profile as satisfied either way. info
// and digest are the identity and the contents the caller established its
// authority over the destination from.
//
// The replacement is a rename of the verified temp file over the destination,
// which is the atomic replace we do want here: the file being replaced is one
// this formae already owns, and the name it is published under does not move,
// so the active pointer stays valid whether or not this is the active profile.
func (r *syncRun) applyContent(e *ledgerEntry, record desiredRecord, auth cliAuthBlock,
	info os.FileInfo, digest string) {
	content := r.render(record, auth)
	updated := fingerprint(content)
	if updated == digest {
		// Byte-identical: there is nothing to write, and rewriting it anyway
		// would churn a file the user may be watching.
		r.satisfied(e.Name)
		return
	}

	tempName, err := newTempName()
	if err != nil {
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q was not updated: %v", e.Name, err))
		return
	}

	restore := *e
	e.State = entryPending
	e.Fingerprint = updated
	e.AltFingerprint = digest // the other content the file may legitimately hold.
	e.TempName = tempName
	if !r.commit() {
		return
	}

	tempPath, err := r.writeTemp(tempName, content, record)
	if err != nil {
		r.abandonChange(e, restore, fmt.Sprintf("profile %q was not updated: %v", e.Name, err))
		return
	}

	// The destination is identified and hashed again immediately before it is
	// replaced. The authority to replace it was established before the profile
	// was rendered, before the ledger was written, and before the temp file was
	// loaded back through the config loader — the slowest step of the run — and
	// a save from the user's editor anywhere in that window puts different
	// bytes, usually a whole new file, at the name. os.Rename would destroy
	// them without a trace, so the file this is about to replace must still be
	// the one whose contents authorised it: same bytes and same file, never one
	// or the other. Anything else abandons the update, exactly as a file the
	// user edited before the run started does. The removal paths re-read their
	// file immediately before removing it for the same reason.
	//
	// What is left is the window between these two syscalls, which the portable
	// filesystem API cannot close — there is no rename-if-still-this-file — so
	// this is the last thing done before the rename and nothing slow sits
	// between them.
	path := r.d.Store.ProfilePath(e.Name)
	nowInfo, nowDigest, err := statAndDigest(path)
	switch {
	case err != nil:
		// The name is no longer established as the file the update was
		// authorised against, so nothing is written over it. The entry is
		// restored to the file it described and a later run repairs or adopts
		// whatever it finds there then. A name that went empty or stopped being
		// a regular file is a skip; an error that establishes nothing is also a
		// failure to complete, as it is everywhere else in this file.
		if unreadable(err) {
			r.fail(err)
		}
		r.abandonChange(e, restore, fmt.Sprintf(
			"profile %q was not updated: what is at that name is no longer the file formae was about to "+
				"replace (%v)", e.Name, err))
		return
	case nowDigest != digest, !os.SameFile(info, nowInfo):
		temp := e.TempName
		r.adopted(e)
		r.removeTemp(temp)
		return
	}

	if err := os.Rename(tempPath, path); err != nil {
		r.fail(err)
		r.abandonChange(e, restore, fmt.Sprintf("profile %q was not updated: %v", e.Name, err))
		return
	}

	if !r.promote(e, updated) {
		return
	}
	r.result.Updated++
	r.satisfied(e.Name)
	r.ack("updated profile " + e.Name)
}

// renameProfile moves the profile an entry owns to the name its installation
// now derives.
//
// The active profile is never renamed: the profile keeps its current name,
// which is merely stale — it still addresses the right installation — while a
// dangling active pointer is a broken CLI. Content updates still apply in
// place, and the next login after the user switches away completes the move.
func (r *syncRun) renameProfile(e *ledgerEntry, record desiredRecord, auth cliAuthBlock) {
	from := e.Name
	oldPath := r.d.Store.ProfilePath(from)
	info, digest, err := statAndDigest(oldPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.publishProfile(record, auth) // nothing of ours is at the old name.
		return
	case errors.Is(err, errNotRegularFile):
		r.warn(fmt.Sprintf(
			"profile %q is not a regular file, so formae neither changed nor removed it", from))
		return
	case err != nil:
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q could not be read, so formae left it alone: %v", from, err))
		return
	}

	if !e.records(digest) {
		r.adopted(e)
		return
	}

	if err := r.protectsActive(info); err != nil {
		if errors.Is(err, errIsActiveProfile) {
			r.warn(fmt.Sprintf(
				"profile %q is the active profile, so formae did not rename it to %q; "+
					"its contents are still kept up to date. Run `formae profile use <name>` to switch away, "+
					"then sign in again to complete the rename",
				from, record.name))
		} else {
			r.warn(fmt.Sprintf(
				"profile %q could not be told apart from the active profile (%v), so formae did not rename it to %q",
				from, err, record.name))
		}
		r.applyContent(e, record, auth, info, digest)
		return
	}

	content := r.render(record, auth)
	tempName, err := newTempName()
	if err != nil {
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q was not renamed to %q: %v", from, record.name, err))
		return
	}

	restore := *e
	e.State = entryPending
	e.Name = record.name
	e.SupersedesName = from
	e.Fingerprint = fingerprint(content)
	e.AltFingerprint = digest // the old file's content, so it can be removed.
	e.TempName = tempName
	if !r.commit() {
		return
	}

	tempPath, err := r.writeTemp(tempName, content, record)
	if err != nil {
		r.abandonChange(e, restore,
			fmt.Sprintf("profile %q was not renamed to %q: %v", from, record.name, err))
		return
	}
	if err := publish(tempPath, r.d.Store.ProfilePath(record.name), content, generatedProfileMode); err != nil {
		message := fmt.Sprintf("profile %q was not renamed to %q: %v", from, record.name, err)
		if errors.Is(err, errNameTaken) {
			message = fmt.Sprintf(
				"a profile named %q already exists and formae did not write it, so profile %q was not renamed to it",
				record.name, from)
		} else {
			r.fail(err)
		}
		r.abandonChange(e, restore, message)
		return
	}

	r.removeSuperseded(e)
	if r.stopped {
		return
	}
	if !r.promote(e, e.Fingerprint) {
		return
	}
	r.result.Renamed++
	r.satisfied(record.name)
	r.ack(fmt.Sprintf("renamed profile %s to %s", from, record.name))
}

// removeSuperseded removes the file a completed rename left behind, under the
// same rules every other removal follows: it goes only if it is a regular file
// whose bytes hash to what the entry recorded for it and it is not the active
// profile.
func (r *syncRun) removeSuperseded(e *ledgerEntry) {
	if e.SupersedesName == "" {
		return
	}
	path := r.d.Store.ProfilePath(e.SupersedesName)
	info, digest, err := statAndDigest(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return // already gone.
	case unreadable(err):
		// Whether the old name still holds the file this entry wrote was not
		// established, so it is left in place and nothing is claimed about it.
		r.fail(err)
		r.warn(fmt.Sprintf(
			"profile %q was renamed to %q, but the file at the old name could not be read (%v), "+
				"so it was left in place; remove it by hand if you do not want it",
			e.SupersedesName, e.Name, err))
		return
	case err != nil, !e.records(digest):
		r.warn(fmt.Sprintf(
			"profile %q was renamed to %q, but the file at the old name is no longer one formae wrote, "+
				"so it was left in place; remove it by hand if you do not want it",
			e.SupersedesName, e.Name))
		return
	}
	if err := r.protectsActive(info); err != nil {
		r.warn(fmt.Sprintf(
			"profile %q was renamed to %q, but the file at the old name was not removed (%v); "+
				"remove it by hand once you have switched away from it",
			e.SupersedesName, e.Name, err))
		return
	}
	if err := os.Remove(path); err != nil {
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q was renamed to %q, but the file at the old name could not be removed: %v",
			e.SupersedesName, e.Name, err))
	}
}

// render returns the bytes a profile for this record carries. The endpoint is
// the record's own — the agent edge where the installation is served, which is
// deliberately a different host from the control plane that described it.
func (r *syncRun) render(record desiredRecord, auth cliAuthBlock) []byte {
	return renderProfile(record.installation.Endpoint, record.installation.InstallationID, auth)
}

// writeTemp writes content to a publication temp file beside the profiles and
// verifies that it resolves to the installation it was rendered for. It
// returns the path whether or not it succeeded, so the caller can clean up.
func (r *syncRun) writeTemp(tempName string, content []byte, record desiredRecord) (string, error) {
	if err := os.MkdirAll(r.d.Store.ProfilesDir(), 0o755); err != nil {
		return "", fmt.Errorf("mkdir profiles: %w", err)
	}
	path := filepath.Join(r.d.Store.ProfilesDir(), tempName)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, generatedProfileMode)
	if err != nil {
		return path, fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := writeFile(f, content); err != nil {
		_ = f.Close()
		return path, fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return path, fmt.Errorf("close %s: %w", path, err)
	}

	if err := r.d.Verifier.Verify(path, record.installation.Endpoint, record.installation.InstallationID); err != nil {
		return path, err
	}
	return path, nil
}

// promote records that a publication landed: the entry owns the file at its
// name, holding exactly the bytes that hash to digest.
func (r *syncRun) promote(e *ledgerEntry, digest string) bool {
	temp := e.TempName
	e.State = entryOwned
	e.Fingerprint = digest
	e.AltFingerprint = ""
	e.SupersedesName = ""
	e.TempName = ""
	if !r.commit() {
		return false
	}
	r.removeTemp(temp)
	return true
}

// abandonPublication gives up on a publication that never landed: the entry it
// committed is dropped, so nothing claims a file this run did not write.
func (r *syncRun) abandonPublication(e *ledgerEntry, warning string) {
	temp := e.TempName
	if !r.drop(e) {
		return
	}
	r.removeTemp(temp)
	r.warn(warning)
}

// abandonChange gives up on a change to a profile this formae already owns,
// restoring the entry to what it recorded before the change was attempted. The
// file itself is untouched, so the restored entry describes it exactly.
func (r *syncRun) abandonChange(e *ledgerEntry, restore ledgerEntry, warning string) {
	temp := e.TempName
	*e = restore
	if !r.commit() {
		return
	}
	r.removeTemp(temp)
	r.warn(warning)
}

// adopted stops managing a file whose bytes are no longer the ones this formae
// wrote. Hand-editing a generated profile is a normal thing to do, and the
// worst possible response is to silently revert or delete it: the file stays,
// the entry goes, and the warning names the way back.
func (r *syncRun) adopted(e *ledgerEntry) {
	name := e.Name
	if !r.drop(e) {
		return
	}
	r.warn(fmt.Sprintf(
		"profile %q is no longer the file formae wrote, so formae has stopped managing it: "+
			"it will not be updated or removed. To have formae manage it again, "+
			"run `formae profile delete %s` and sign in again",
		name, name))
}

// removeTemp removes a publication temp file this run's entry named. A temp
// file nothing recorded is never removed: the name is committed before the
// file is created, so a temp nobody recorded is not ours.
func (r *syncRun) removeTemp(tempName string) {
	if tempName == "" {
		return
	}
	_ = os.Remove(filepath.Join(r.d.Store.ProfilesDir(), tempName))
}

// satisfied counts one installation of this run's desired set as having a
// profile this run published or verified, and records the name.
//
// Every call site has just established that the file at name is one this formae
// owns and holds the content it recorded, so a name reaching here is always a
// profile that exists and can be pointed at.
func (r *syncRun) satisfied(name string) {
	r.result.DesiredSatisfied++
	r.result.Published = append(r.result.Published, name)
}

// records reports whether digest is one of the contents this entry says its
// file may legitimately hold. It is the whole proof that a file is ours.
func (e *ledgerEntry) records(digest string) bool {
	if digest == "" {
		return false
	}
	return digest == e.Fingerprint || (e.AltFingerprint != "" && digest == e.AltFingerprint)
}

// recoverEntries reconciles every entry that is not settled against the
// filesystem, before anything else reads it, so the rest of the run starts
// from a settled state.
//
// It acts under authority an earlier run committed, so it runs for every
// origin's entries and completes whether or not this run's own enumeration
// later succeeds. It never renders a profile: the only files it creates are
// none, the only ones it removes are ones an entry proves are ours, and an
// entry it cannot settle is left exactly as it is for the next run.
func (r *syncRun) recoverEntries() {
	for _, e := range r.ledger.Authoritative() {
		if r.stopped {
			return
		}
		switch e.State {
		case entryPending:
			r.recoverPending(e)
		case entryDeleting:
			r.recoverDeleting(e)
		case entryOwned:
		}
	}
}

// recoverPending settles a publication that was interrupted between its intent
// and its result.
//
// The one thing it must never do is turn a pending entry into an owned one on
// the strength of bytes this formae did not produce. A file at the name may
// hash to what we were about to write and still be the user's own, and after a
// crash nothing in a hash tells the two apart, so promotion needs the witness:
// publication is a hard link, so the temp file and the published file are the
// same inode, and os.SameFile answers the question exactly. No witness means
// no promotion, whatever the bytes say — which is why a publication that fell
// back to an exclusive write, and so has no witness at all, is abandoned
// rather than adopted.
//
// The one case that does not need a witness is a replacement of a file the
// entry already owned at that name, since the authority over it predates the
// operation being recovered. An entry naming a supersedesName is not that
// case: the name it publishes to is one it did not own before.
func (r *syncRun) recoverPending(e *ledgerEntry) {
	path := r.d.Store.ProfilePath(e.Name)
	info, digest, err := statAndDigest(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.recoverUnpublished(e)
		return
	case errors.Is(err, errNotRegularFile):
		r.abandonPending(e, fmt.Sprintf(
			"profile %q is not a regular file, so formae could not confirm it wrote it and no longer manages it",
			e.Name))
		return
	case err != nil:
		r.unsettled(e.Name, err)
		return
	}

	witnessed := r.witnesses(e, info)
	replacement := e.AltFingerprint != "" && e.SupersedesName == ""
	switch {
	case witnessed && digest == e.Fingerprint:
		r.removeSuperseded(e)
		if r.stopped {
			return
		}
		r.promote(e, digest)
	case replacement && e.records(digest):
		// Whichever side of the replacement it stopped on, the file is one
		// this formae owns; the entry is settled at the content actually on
		// disk, and a later run writes the update again if it is still due.
		r.promote(e, digest)
	default:
		r.abandonPending(e, fmt.Sprintf(
			"formae could not confirm that it wrote profile %q, so it does not manage that file: "+
				"nothing was changed or removed. Remove %s by hand if you do not want it",
			e.Name, r.d.Store.ProfilePath(e.Name)))
	}
}

// recoverUnpublished settles a pending entry whose destination does not exist.
func (r *syncRun) recoverUnpublished(e *ledgerEntry) {
	switch {
	case e.SupersedesName != "":
		// The rename never published, so the file at the old name is still the
		// one this entry owns.
		r.revertRename(e)
	case e.AltFingerprint != "":
		// A replacement of a file we owned, whose file is gone: keep the entry
		// at the content it owned, so the desired step repairs it and a prune
		// can still clean it up.
		r.settle(e, e.AltFingerprint)
	default:
		// A publication that never landed: nothing on disk is ours.
		r.abandonPending(e, "")
	}
}

// revertRename returns an entry to the name it still owns, after a rename that
// published nothing.
//
// "The old name is gone, or holds something this formae did not write" and "the
// old name could not be read at all" are different answers. The first is an
// established fact and forfeits both names. The second establishes nothing, so
// the entry is left exactly as it is for the next run: forfeiting on it would
// orphan a profile this formae still owns, at a name nothing would ever manage
// or remove again.
func (r *syncRun) revertRename(e *ledgerEntry) {
	oldName := e.SupersedesName
	_, digest, err := statAndDigest(r.d.Store.ProfilePath(oldName))
	switch {
	case unreadable(err):
		r.unsettled(oldName, err)
	case err != nil, !e.records(digest):
		r.abandonPending(e, fmt.Sprintf(
			"formae was renaming profile %q to %q when it was interrupted, and the file at the old name is no "+
				"longer one it wrote, so it no longer manages either name", oldName, e.Name))
	default:
		e.Name = oldName
		r.settle(e, digest)
	}
}

// unreadable reports an error that establishes nothing about a path: neither
// that there is no file there nor that what is there is not one this formae
// could own, but that it could not be read well enough to decide. Every
// decision this file takes resolves such an error toward doing nothing.
func unreadable(err error) bool {
	return err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errNotRegularFile)
}

// settle records that an entry owns the file at its name, holding the content
// that hashes to digest, and clears everything the interrupted operation had
// recorded.
func (r *syncRun) settle(e *ledgerEntry, digest string) {
	temp := e.TempName
	e.State = entryOwned
	e.Fingerprint = digest
	e.AltFingerprint = ""
	e.SupersedesName = ""
	e.TempName = ""
	if !r.commit() {
		return
	}
	r.removeTemp(temp)
}

// abandonPending drops a pending entry that cannot be settled, leaving
// whatever is on disk exactly where it is. An empty warning means there is
// nothing at the name to tell the user about.
func (r *syncRun) abandonPending(e *ledgerEntry, warning string) {
	temp := e.TempName
	if !r.drop(e) {
		return
	}
	r.removeTemp(temp)
	if warning != "" {
		r.warn(warning)
	}
}

// unsettled leaves an entry exactly as it is, because what is at the named
// path could not be established. Acting on it later would act on an
// unestablished fact, so the entry stays for the next run. name is the profile
// the unreadable path belongs to, which is not always the entry's own name.
func (r *syncRun) unsettled(name string, err error) {
	r.fail(err)
	r.warn(fmt.Sprintf("profile %q could not be read, so formae left it alone: %v", name, err))
}

// witnesses reports whether the file at an entry's name is the same file as
// the temp it published from, which is the proof that the publication was
// this formae's own.
func (r *syncRun) witnesses(e *ledgerEntry, info os.FileInfo) bool {
	if e.TempName == "" {
		return false
	}
	temp, err := os.Lstat(filepath.Join(r.d.Store.ProfilesDir(), e.TempName))
	if err != nil {
		return false
	}
	return os.SameFile(temp, info)
}

// recoverDeleting finishes a removal an earlier run committed. That run held
// an authoritative snapshot or an explicit logout when it wrote the intent, so
// finishing it needs no authority of this run's own — but it still needs every
// other condition of the deletion rule to hold now.
func (r *syncRun) recoverDeleting(e *ledgerEntry) {
	path := r.d.Store.ProfilePath(e.Name)
	info, digest, err := statAndDigest(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.drop(e) // the unlink completed.
		return
	case errors.Is(err, errNotRegularFile):
		r.warn(fmt.Sprintf(
			"profile %q is not a regular file, so formae did not remove it; formae no longer manages it", e.Name))
		r.drop(e)
		return
	case err != nil:
		r.unsettled(e.Name, err)
		return
	}

	if !e.records(digest) {
		r.adopted(e)
		return
	}
	if err := r.protectsActive(info); err != nil {
		// The file and the entry are both kept, so a later run finishes the
		// removal once the user has switched away.
		r.warn(fmt.Sprintf(
			"profile %q was being removed when formae was interrupted, and it was not removed now (%v); "+
				"run `formae profile use <name>` to switch away, then sign in again", e.Name, err))
		return
	}

	name := e.Name
	if err := os.Remove(path); err != nil {
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q could not be removed: %v", name, err))
		return
	}
	if !r.drop(e) {
		return
	}
	r.result.Pruned++
	r.ack("removed profile " + name)
}

// prune removes the profiles this formae owns for this origin whose
// installation is absent from the snapshot or present in a terminal state.
//
// It runs only under complete knowledge: on a login that is the snapshot's
// Authoritative flag, and on a logout the user's explicit sign-out, which is
// the authority an empty desired set is read against. An installation in an
// unrecognised state is neither absent nor terminal, so nothing is pruned for
// it, while every sibling is pruned as usual.
func (r *syncRun) prune(present, terminal, unrecognised map[string]bool) {
	for _, e := range r.ledger.Authoritative() {
		if r.stopped {
			return
		}
		if e.ControlPlane != r.origin || e.State != entryOwned {
			continue
		}
		id := e.InstallationID
		if unrecognised[id] || (present[id] && !terminal[id]) {
			continue
		}
		r.pruneEntry(e)
	}
}

// pruneEntry removes one profile whose grant is gone, under the deletion rule
// in full: the entry is valid and ours, the path is a regular file whose bytes
// hash to a fingerprint the entry records, and it is not the active profile.
func (r *syncRun) pruneEntry(e *ledgerEntry) {
	path := r.d.Store.ProfilePath(e.Name)
	info, digest, err := statAndDigest(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Nothing to remove and nothing left to manage: dropping the entry is
		// what keeps a `formae profile delete` of a managed profile from
		// leaving a permanent record.
		r.drop(e)
		return
	case errors.Is(err, errNotRegularFile):
		r.warn(fmt.Sprintf(
			"profile %q is not a regular file, so formae did not remove it even though your access to "+
				"installation %s is gone; formae no longer manages it", e.Name, e.InstallationID))
		r.drop(e)
		return
	case err != nil:
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q could not be read, so formae left it alone: %v", e.Name, err))
		return
	}

	if !e.records(digest) {
		r.adopted(e)
		return
	}

	if err := r.protectsActive(info); err != nil {
		// The file *and* the entry are kept, so a later login removes it once
		// the user has switched away.
		if errors.Is(err, errIsActiveProfile) {
			r.warn(r.keptActive(e))
		} else {
			r.warn(fmt.Sprintf(
				"profile %q could not be told apart from the active profile (%v), so formae left it in place",
				e.Name, err))
		}
		return
	}

	name := e.Name
	e.State = entryDeleting
	e.Fingerprint = digest
	e.AltFingerprint = ""
	if !r.commit() {
		return
	}
	if err := os.Remove(path); err != nil {
		// The entry stays in its deleting state, so the next run finishes it.
		r.fail(err)
		r.warn(fmt.Sprintf("profile %q could not be removed: %v", name, err))
		return
	}
	if !r.drop(e) {
		return
	}
	r.result.Pruned++
	r.ack("removed profile " + name)
}

// keptActiveAfterLostAccess phrases the profile a sign-in's prune kept because
// it is the active one. The grant behind it is gone, so the file is due for
// removal, and the next sign-in takes it once the user has switched away.
func keptActiveAfterLostAccess(e *ledgerEntry) string {
	return fmt.Sprintf(
		"profile %q is the active profile, so formae did not remove it even though your access to "+
			"installation %s is gone. Run `formae profile use <name>` to switch away, "+
			"then sign in again to remove it", e.Name, e.InstallationID)
}

// keptActiveAfterSignOut phrases the same profile after a sign-out, where both
// halves of the other wording would be wrong. Nothing was revoked — the user
// gave a credential up and the grant is still theirs — and signing in again
// would derive every profile the sign-out has just removed while keeping this
// one anyway. Keeping it is the point of the sign-out's design: it is the
// profile there is left to sign back in with, so what the message offers is a
// way to be rid of it rather than a way to have it removed for you.
func keptActiveAfterSignOut(e *ledgerEntry) string {
	return fmt.Sprintf(
		"profile %q is the active profile, so formae did not remove it: it is what you have left to "+
			"sign back in with. Run `formae profile use <name>` to switch away, "+
			"then `formae profile delete %s` if you do not want it", e.Name, e.Name)
}
