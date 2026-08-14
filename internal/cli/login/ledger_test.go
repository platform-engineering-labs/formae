// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOrigin      = "https://cloud.formae.io"
	testOtherOrigin = "https://staging.formae.io"

	testUUIDA = "3f2b8c14-0000-4000-8000-00000000000a"
	testUUIDB = "3f2b8c14-0000-4000-8000-00000000000b"
	testUUIDC = "3f2b8c14-0000-4000-8000-00000000000c"

	testFingerprintA = "sha256:" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testFingerprintB = "sha256:" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// rawEntry is a ledger entry as it appears in the file, built as raw JSON so
// tests can express shapes the Go type cannot hold.
type rawEntry map[string]any

// validRaw returns a well-formed entry that every validation rule accepts.
func validRaw(name, installationID string) rawEntry {
	return rawEntry{
		"controlPlane":   testOrigin,
		"installationId": installationID,
		"name":           name,
		"state":          "owned",
		"fingerprint":    testFingerprintA,
	}
}

// writeLedgerFile writes a ledger file with the given schema version and
// entries, and returns its path.
func writeLedgerFile(t *testing.T, version int, entries ...any) string {
	t.Helper()
	if entries == nil {
		entries = []any{}
	}
	data, err := json.Marshal(map[string]any{"schemaVersion": version, "entries": entries})
	require.NoError(t, err)
	return writeRawLedgerFile(t, string(data))
}

// writeRawLedgerFile writes arbitrary bytes to a ledger path and returns it.
func writeRawLedgerFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// savedSchemaVersion returns the schemaVersion recorded in the file at path.
func savedSchemaVersion(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var file ledgerFile
	require.NoError(t, json.Unmarshal(data, &file))
	return file.SchemaVersion
}

// savedEntries returns the entries recorded in the file at path, decoded
// without validation, so a test can assert what save actually wrote rather
// than what a later load makes of it.
func savedEntries(t *testing.T, path string) []*ledgerEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var file struct {
		Entries []*ledgerEntry `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(data, &file))
	return file.Entries
}

// names returns the Name of every entry, in order.
func names(entries []*ledgerEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// TestLoadLedger_AbsentFileIsEmptyAndSilent pins the first-run case: no file
// is not a problem to report, it is a formae that has never written a profile.
func TestLoadLedger_AbsentFileIsEmptyAndSilent(t *testing.T) {
	l, warnings, err := loadLedger(filepath.Join(t.TempDir(), "managed.json"))

	require.NoError(t, err)
	require.NotNil(t, l)
	assert.Empty(t, warnings)
	assert.Empty(t, l.entries)
	assert.Empty(t, l.Authoritative())
}

// TestLoadLedger_UnparseableIsEmptyWithOneWarning pins the safe-by-
// construction reading of a corrupt file: an empty ledger grants no authority
// over any file, so continuing can only skip work, never delete something.
func TestLoadLedger_UnparseableIsEmptyWithOneWarning(t *testing.T) {
	for _, content := range []string{"", "{", "not json at all", `{"schemaVersion": "one"}`} {
		path := writeRawLedgerFile(t, content)

		l, warnings, err := loadLedger(path)

		require.NoError(t, err)
		require.NotNil(t, l)
		assert.Empty(t, l.entries)
		assert.Empty(t, l.Authoritative())
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], path)
	}
}

// TestLoadLedger_UnknownSchemaVersionRefusesToLoad pins the one failure mode
// that must stop the caller: records written by another formae are not ours
// to rewrite, and a create we could not record is a profile we could never
// manage afterwards.
func TestLoadLedger_UnknownSchemaVersionRefusesToLoad(t *testing.T) {
	for _, version := range []int{0, 2, 99, -1} {
		path := writeLedgerFile(t, version, validRaw("acme-prod", testUUIDA))

		l, warnings, err := loadLedger(path)

		require.ErrorIs(t, err, errUnknownSchemaVersion)
		assert.Nil(t, l)
		assert.Empty(t, warnings)
	}
}

// TestLoadLedger_ValidEntriesAreAuthoritative covers the happy path over
// every entry state and every optional field. A pending entry names a file
// that need not exist yet, so it is the one state that carries no
// fingerprint.
func TestLoadLedger_ValidEntriesAreAuthoritative(t *testing.T) {
	pending := validRaw("acme-pending", testUUIDA)
	pending["state"] = string(entryPending)
	pending["fingerprint"] = ""
	pending["tempName"] = ".tmp-0123456789abcdef.pkl"

	owned := validRaw("acme-owned", testUUIDB)
	owned["altFingerprint"] = testFingerprintB
	owned["supersedesName"] = "acme-old-name"

	deleting := validRaw("acme-deleting", testUUIDC)
	deleting["state"] = string(entryDeleting)

	path := writeLedgerFile(t, ledgerSchemaVersion, pending, owned, deleting)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, []string{"acme-pending", "acme-owned", "acme-deleting"}, names(l.entries))
	assert.Equal(t, names(l.entries), names(l.Authoritative()))
	assert.Equal(t, entryPending, l.entries[0].State)
	assert.Equal(t, ".tmp-0123456789abcdef.pkl", l.entries[0].TempName)
	assert.Equal(t, testFingerprintB, l.entries[1].AltFingerprint)
	assert.Equal(t, "acme-old-name", l.entries[1].SupersedesName)
	assert.Equal(t, entryDeleting, l.entries[2].State)
}

// TestLoadLedger_DropsMalformedEntries walks every field that eventually
// names a path or licenses a deletion. A malformed entry is garbage: it is
// dropped, warned about, grants no authority, and is not written back.
func TestLoadLedger_DropsMalformedEntries(t *testing.T) {
	mutate := func(f func(rawEntry)) rawEntry {
		e := validRaw("acme-bad", testUUIDB)
		f(e)
		return e
	}

	tests := []struct {
		name string
		bad  any
	}{
		{name: "null entry", bad: nil},
		{name: "entry that is not an object", bad: 7},
		{name: "type error in state", bad: mutate(func(e rawEntry) { e["state"] = 1 })},
		{name: "type error in name", bad: mutate(func(e rawEntry) { e["name"] = []string{"acme"} })},
		{name: "empty entry", bad: rawEntry{}},
		{name: "traversal in name", bad: mutate(func(e rawEntry) { e["name"] = "../../etc/passwd" })},
		{name: "empty name", bad: mutate(func(e rawEntry) { e["name"] = "" })},
		{name: "name with a slash", bad: mutate(func(e rawEntry) { e["name"] = "sub/dir" })},
		{name: "name with an extension", bad: mutate(func(e rawEntry) { e["name"] = "acme.pkl" })},
		{name: "traversal in supersedesName", bad: mutate(func(e rawEntry) { e["supersedesName"] = "../evil" })},
		{name: "non-uuid installationId", bad: mutate(func(e rawEntry) { e["installationId"] = "not-a-uuid" })},
		{name: "uppercase installationId", bad: mutate(func(e rawEntry) { e["installationId"] = strings.ToUpper(testUUIDB) })},
		{name: "empty installationId", bad: mutate(func(e rawEntry) { e["installationId"] = "" })},
		{name: "malformed fingerprint", bad: mutate(func(e rawEntry) { e["fingerprint"] = "sha256:nothex" })},
		{name: "unprefixed fingerprint", bad: mutate(func(e rawEntry) { e["fingerprint"] = strings.TrimPrefix(testFingerprintA, "sha256:") })},
		{name: "malformed altFingerprint", bad: mutate(func(e rawEntry) { e["altFingerprint"] = "sha256:zz" })},
		{name: "owned entry with an empty fingerprint", bad: mutate(func(e rawEntry) { e["fingerprint"] = "" })},
		{name: "owned entry with no fingerprint at all", bad: mutate(func(e rawEntry) { delete(e, "fingerprint") })},
		{name: "deleting entry with an empty fingerprint", bad: mutate(func(e rawEntry) {
			e["state"] = string(entryDeleting)
			e["fingerprint"] = ""
		})},
		{name: "unknown state", bad: mutate(func(e rawEntry) { e["state"] = "adopted" })},
		{name: "empty state", bad: mutate(func(e rawEntry) { e["state"] = "" })},
		{name: "bogus controlPlane", bad: mutate(func(e rawEntry) { e["controlPlane"] = "not a url" })},
		{name: "plain http controlPlane", bad: mutate(func(e rawEntry) { e["controlPlane"] = "http://cloud.formae.io" })},
		{name: "controlPlane with a path", bad: mutate(func(e rawEntry) { e["controlPlane"] = testOrigin + "/api" })},
		{name: "empty controlPlane", bad: mutate(func(e rawEntry) { e["controlPlane"] = "" })},
		{name: "tempName not matching the pattern", bad: mutate(func(e rawEntry) { e["tempName"] = "scratch.pkl" })},
		{name: "tempName with a traversal", bad: mutate(func(e rawEntry) { e["tempName"] = "../.tmp-0123456789abcdef.pkl" })},
		{name: "tempName with the wrong hex width", bad: mutate(func(e rawEntry) { e["tempName"] = ".tmp-0123.pkl" })},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			good := validRaw("acme-good", testUUIDA)
			path := writeLedgerFile(t, ledgerSchemaVersion, good, tc.bad)

			l, warnings, err := loadLedger(path)

			require.NoError(t, err)
			assert.Equal(t, []string{"acme-good"}, names(l.entries), "the malformed entry must not be carried forward")
			assert.Equal(t, []string{"acme-good"}, names(l.Authoritative()))
			require.Len(t, warnings, 1)

			// A dropped entry must be gone from the file after a load/save cycle.
			require.NoError(t, l.save(path))
			reloaded, warnings, err := loadLedger(path)
			require.NoError(t, err)
			assert.Empty(t, warnings)
			assert.Equal(t, []string{"acme-good"}, names(reloaded.entries))
		})
	}
}

// TestLoadLedger_ConfinesATypeErrorToItsOwnEntry pins the blast radius of a
// single bad record. Decoding the file in one piece would make one wrongly
// typed field read the whole ledger as empty, and the next save would then
// destroy every record in it — including records for control planes this run
// never touched. Each entry is decoded on its own, so a type error is the
// same dropped, warned entry as any other malformed one.
func TestLoadLedger_ConfinesATypeErrorToItsOwnEntry(t *testing.T) {
	broken := validRaw("acme-broken", testUUIDB)
	broken["state"] = 1 // a number where a string belongs.

	other := validRaw("staging-one", testUUIDC)
	other["controlPlane"] = testOtherOrigin

	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA), broken, other)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Equal(t, []string{"acme-prod", "staging-one"}, names(l.entries),
		"the siblings of a wrongly typed entry survive")

	require.NoError(t, l.save(path))
	reloaded, warnings, err := loadLedger(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, []string{"acme-prod", "staging-one"}, names(reloaded.Authoritative()))
	assert.Equal(t, testOtherOrigin, reloaded.Authoritative()[1].ControlPlane)
}

// TestLoadLedger_QuarantinesEntriesSharingAName pins the other half of the
// rule: a uniqueness conflict is not garbage, so no member is dropped and no
// member is believed. Picking a winner would hand delete authority to
// whichever entry the file happened to list first.
func TestLoadLedger_QuarantinesEntriesSharingAName(t *testing.T) {
	path := writeLedgerFile(t, ledgerSchemaVersion,
		validRaw("acme-prod", testUUIDA),
		validRaw("acme-prod", testUUIDB),
	)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Len(t, l.entries, 2, "quarantined entries are carried forward")
	assert.Empty(t, l.Authoritative(), "no member of a conflicting set grants authority")
	require.Len(t, warnings, 1)

	require.NoError(t, l.save(path))
	reloaded, _, err := loadLedger(path)
	require.NoError(t, err)
	assert.Len(t, reloaded.entries, 2, "quarantined entries survive a save")
	assert.Empty(t, reloaded.Authoritative())
}

// TestLoadLedger_QuarantinesEntriesSharingAnInstallationID is the same rule
// keyed on the installation rather than the file name.
func TestLoadLedger_QuarantinesEntriesSharingAnInstallationID(t *testing.T) {
	path := writeLedgerFile(t, ledgerSchemaVersion,
		validRaw("acme-prod", testUUIDA),
		validRaw("acme-staging", testUUIDA),
	)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Len(t, l.entries, 2)
	assert.Empty(t, l.Authoritative())
	require.Len(t, warnings, 1)

	require.NoError(t, l.save(path))
	reloaded, _, err := loadLedger(path)
	require.NoError(t, err)
	assert.Len(t, reloaded.entries, 2)
	assert.Empty(t, reloaded.Authoritative())
}

// TestLoadLedger_QuarantinesTheWholeConnectedComponent builds a chain: A and
// B share a name, B and C share an installation id. A and C share nothing
// directly, but B ties all three together, so all three must be quarantined.
// A pairwise implementation clears A or C and hands it authority it has not
// earned.
func TestLoadLedger_QuarantinesTheWholeConnectedComponent(t *testing.T) {
	a := validRaw("shared-name", testUUIDA)
	b := validRaw("shared-name", testUUIDB)
	c := validRaw("other-name", testUUIDB)
	unrelated := validRaw("unrelated", testUUIDC)

	path := writeLedgerFile(t, ledgerSchemaVersion, a, b, c, unrelated)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Len(t, l.entries, 4)
	assert.Equal(t, []string{"unrelated"}, names(l.Authoritative()),
		"every member of the connected component is quarantined; the unrelated entry is not")
	require.Len(t, warnings, 1, "one warning for the one conflicting set")
}

// TestLoadLedger_QuarantineWarningNamesEveryMemberAndTheRemedy pins the exit
// from quarantine. Without a stated remedy the state is permanent, and
// removing the ledger is always safe: it grants zero authority, so it deletes
// nothing and leaves every profile in place.
func TestLoadLedger_QuarantineWarningNamesEveryMemberAndTheRemedy(t *testing.T) {
	path := writeLedgerFile(t, ledgerSchemaVersion,
		validRaw("shared-name", testUUIDA),
		validRaw("shared-name", testUUIDB),
		validRaw("other-name", testUUIDB),
	)

	_, warnings, err := loadLedger(path)

	require.NoError(t, err)
	require.Len(t, warnings, 1)
	warning := warnings[0]
	assert.Contains(t, warning, "shared-name")
	assert.Contains(t, warning, "other-name")
	assert.Contains(t, warning, testUUIDA)
	assert.Contains(t, warning, testUUIDB)
	assert.Contains(t, warning, path, "the remedy must name the file to remove")
	assert.Contains(t, warning, "remove")
}

// TestLoadLedger_DifferentControlPlanesDoNotConflict verifies uniqueness is
// scoped to a control plane: the same profile name against two control planes
// is a collision the sync step resolves, not a ledger conflict.
func TestLoadLedger_DifferentControlPlanesDoNotConflict(t *testing.T) {
	other := validRaw("acme-prod", testUUIDA)
	other["controlPlane"] = testOtherOrigin

	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA), other)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Len(t, l.Authoritative(), 2)
}

// TestLoadLedger_CanonicalisesControlPlane verifies a control plane is stored
// canonically, so two spellings of one origin are recognised as one origin —
// both for the conflict scan and for later comparison against the origin a
// run is syncing.
func TestLoadLedger_CanonicalisesControlPlane(t *testing.T) {
	noisy := validRaw("acme-prod", testUUIDA)
	noisy["controlPlane"] = "HTTPS://Cloud.Formae.IO:443/"

	path := writeLedgerFile(t, ledgerSchemaVersion, noisy)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, l.Authoritative(), 1)
	assert.Equal(t, testOrigin, l.entries[0].ControlPlane)
}

// TestLoadLedger_ConflictSurvivesADifferentSpellingOfTheSameOrigin makes the
// point above load-bearing: a second spelling must not slip a duplicate past
// the conflict scan.
func TestLoadLedger_ConflictSurvivesADifferentSpellingOfTheSameOrigin(t *testing.T) {
	noisy := validRaw("acme-prod", testUUIDB)
	noisy["controlPlane"] = "https://CLOUD.formae.io:443"

	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA), noisy)

	l, warnings, err := loadLedger(path)

	require.NoError(t, err)
	assert.Len(t, l.entries, 2)
	assert.Empty(t, l.Authoritative())
	assert.Len(t, warnings, 1)
}

// TestLedgerUpsert_ReplacesTheRecordForAnInstallation pins that a record is
// keyed on (controlPlane, installationId): re-recording an installation
// replaces its entry rather than leaving a second one behind, since two
// entries for one installation would quarantine both and forfeit management
// of the file.
func TestLedgerUpsert_ReplacesTheRecordForAnInstallation(t *testing.T) {
	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA))

	l, _, err := loadLedger(path)
	require.NoError(t, err)
	l.upsert(&ledgerEntry{
		ControlPlane:   testOrigin,
		InstallationID: testUUIDA,
		Name:           "acme-renamed",
		State:          entryOwned,
		Fingerprint:    testFingerprintB,
	})

	assert.Equal(t, []string{"acme-renamed"}, names(l.entries))
	require.NoError(t, l.save(path))

	reloaded, _, err := loadLedger(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"acme-renamed"}, names(reloaded.Authoritative()))
	assert.Equal(t, testFingerprintB, reloaded.Authoritative()[0].Fingerprint)
}

// TestLedgerRemove_DropsTheRecordForAnInstallation pins the other half: a
// removed record is gone from the file, so the file it named is no longer
// ours to delete.
func TestLedgerRemove_DropsTheRecordForAnInstallation(t *testing.T) {
	other := validRaw("staging-one", testUUIDC)
	other["controlPlane"] = testOtherOrigin
	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA), other)

	l, _, err := loadLedger(path)
	require.NoError(t, err)
	l.remove(testOrigin, testUUIDA)
	l.remove(testOrigin, testUUIDB) // absent: nothing to drop.
	require.NoError(t, l.save(path))

	reloaded, _, err := loadLedger(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"staging-one"}, names(reloaded.entries))
}

// TestLedgerMutation_NeverTouchesAQuarantinedEntry pins that a conflicting
// set is carried forward exactly as it was found: no run resolves a conflict
// by replacing or dropping one of its members, because choosing a member is
// the act quarantine exists to prevent.
func TestLedgerMutation_NeverTouchesAQuarantinedEntry(t *testing.T) {
	path := writeLedgerFile(t, ledgerSchemaVersion,
		validRaw("acme-prod", testUUIDA),
		validRaw("acme-staging", testUUIDA),
	)

	l, _, err := loadLedger(path)
	require.NoError(t, err)
	l.remove(testOrigin, testUUIDA)
	l.upsert(&ledgerEntry{
		ControlPlane:   testOrigin,
		InstallationID: testUUIDA,
		Name:           "acme-new",
		State:          entryOwned,
		Fingerprint:    testFingerprintA,
	})

	assert.Equal(t, []string{"acme-prod", "acme-staging", "acme-new"}, names(l.entries),
		"neither member of the conflicting set is replaced or dropped")
	require.NoError(t, l.save(path))

	reloaded, warnings, err := loadLedger(path)
	require.NoError(t, err)
	assert.Empty(t, reloaded.Authoritative(), "the appended entry joins the conflict rather than resolving it")
	assert.Len(t, warnings, 1)
}

// TestLedgerSave_CarriesForwardEveryEntryUnchanged covers the round trip,
// including a quarantined pair and an entry recorded against another control
// plane: a sync against staging must never disturb production's records.
func TestLedgerSave_CarriesForwardEveryEntryUnchanged(t *testing.T) {
	staging := validRaw("staging-one", testUUIDC)
	staging["controlPlane"] = testOtherOrigin
	staging["state"] = string(entryPending)
	staging["fingerprint"] = ""
	staging["tempName"] = ".tmp-abcdef0123456789.pkl"

	conflictA := validRaw("shared-name", testUUIDA)
	conflictB := validRaw("shared-name", testUUIDB)
	conflictB["supersedesName"] = "older-name"
	conflictB["altFingerprint"] = testFingerprintB

	path := writeLedgerFile(t, ledgerSchemaVersion, conflictA, conflictB, staging)

	l, _, err := loadLedger(path)
	require.NoError(t, err)
	require.NoError(t, l.save(path))

	reloaded, _, err := loadLedger(path)
	require.NoError(t, err)
	assert.Equal(t, l.entries, reloaded.entries)
	assert.Equal(t, []string{"shared-name", "shared-name", "staging-one"}, names(savedEntries(t, path)),
		"a quarantined entry grants no authority, and is still written back")
	assert.Equal(t, ledgerSchemaVersion, savedSchemaVersion(t, path))
	assert.Equal(t, []string{"staging-one"}, names(reloaded.Authoritative()))
}

// TestLedgerSave_LeavesOtherControlPlanesUntouched exercises the case a sync
// actually performs: entries are added for one origin and written back, and
// another origin's records must come through byte-identical.
func TestLedgerSave_LeavesOtherControlPlanesUntouched(t *testing.T) {
	other := validRaw("staging-one", testUUIDC)
	other["controlPlane"] = testOtherOrigin
	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA), other)

	l, _, err := loadLedger(path)
	require.NoError(t, err)
	l.upsert(&ledgerEntry{
		ControlPlane:   testOrigin,
		InstallationID: testUUIDB,
		Name:           "acme-dev",
		State:          entryOwned,
		Fingerprint:    testFingerprintA,
	})
	require.NoError(t, l.save(path))

	reloaded, warnings, err := loadLedger(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, []string{"acme-prod", "staging-one", "acme-dev"}, names(reloaded.entries))
	assert.Equal(t, testOtherOrigin, reloaded.entries[1].ControlPlane)
	assert.Equal(t, testUUIDC, reloaded.entries[1].InstallationID)
}

// TestLedgerSave_CanonicalisesTheEntriesItWrites pins that the file's
// invariants are true by construction rather than by convention: an entry
// added during a run is written under the same origin spelling as one read
// from the file, so two spellings of one control plane cannot slip a
// duplicate past the conflict scan.
func TestLedgerSave_CanonicalisesTheEntriesItWrites(t *testing.T) {
	path := writeLedgerFile(t, ledgerSchemaVersion, validRaw("acme-prod", testUUIDA))

	l, _, err := loadLedger(path)
	require.NoError(t, err)
	l.upsert(&ledgerEntry{
		ControlPlane:   "HTTPS://Cloud.Formae.IO:443/",
		InstallationID: testUUIDB,
		Name:           "acme-dev",
		State:          entryOwned,
		Fingerprint:    testFingerprintA,
	})
	require.NoError(t, l.save(path))

	written := savedEntries(t, path)
	require.Len(t, written, 2)
	assert.Equal(t, testOrigin, written[1].ControlPlane, "the written record carries the canonical origin")
}

// TestLedgerSave_WritesAtomicallyThroughAUniqueTempFile drives concurrent
// saves against one path while reading it: with a unique temp file plus a
// rename, every observer sees a complete ledger, never a truncated one, and
// no temp file is left behind.
func TestLedgerSave_WritesAtomicallyThroughAUniqueTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.json")

	newLedger := func(n int) *ledger {
		l := &ledger{}
		for i := 0; i < n; i++ {
			l.upsert(&ledgerEntry{
				ControlPlane:   testOrigin,
				InstallationID: fmt.Sprintf("3f2b8c14-0000-4000-8000-%012d", i),
				Name:           fmt.Sprintf("acme-prod-%d", i),
				State:          entryOwned,
				Fingerprint:    testFingerprintA,
			})
		}
		return l
	}

	var wg sync.WaitGroup
	for w := 1; w <= 4; w++ {
		wg.Add(1)
		go func(size int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				assert.NoError(t, newLedger(size).save(path))
			}
		}(w)
	}

	for i := 0; i < 200; i++ {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		var observed ledgerFile
		require.NoError(t, json.Unmarshal(data, &observed), "a reader must never observe a partial ledger")
		assert.NotEmpty(t, observed.Entries)
	}
	wg.Wait()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp file may be left behind")
	assert.Equal(t, "managed.json", entries[0].Name())
}

// TestLedgerSave_FailureLeavesTheExistingLedgerIntact pins that a save that
// cannot complete never truncates the records already on disk, whether it
// gives up before writing anything or fails at the rename. Both failures are
// injected without permission bits, so the assertion holds for every user,
// root included.
func TestLedgerSave_FailureLeavesTheExistingLedgerIntact(t *testing.T) {
	original, err := json.Marshal(map[string]any{
		"schemaVersion": ledgerSchemaVersion,
		"entries":       []any{validRaw("acme-prod", testUUIDA)},
	})
	require.NoError(t, err)

	validEntry := func() *ledgerEntry {
		return &ledgerEntry{
			ControlPlane:   testOrigin,
			InstallationID: testUUIDB,
			Name:           "acme-dev",
			State:          entryOwned,
			Fingerprint:    testFingerprintA,
		}
	}

	tests := []struct {
		name string
		// setUp returns the ledger to save, the path to save it to, and the
		// file whose bytes must still be the original records afterwards.
		setUp func(t *testing.T, dir string) (l *ledger, path, witness string)
	}{
		{
			name: "an entry whose control plane will not canonicalise",
			setUp: func(t *testing.T, dir string) (*ledger, string, string) {
				path := filepath.Join(dir, "managed.json")
				require.NoError(t, os.WriteFile(path, original, 0o600))
				l := &ledger{}
				e := validEntry()
				e.ControlPlane = "not a url"
				l.upsert(e)
				return l, path, path
			},
		},
		{
			name: "a destination that cannot be replaced",
			setUp: func(t *testing.T, dir string) (*ledger, string, string) {
				// A directory at the destination makes the rename fail for
				// every user, so the temp file has to be cleaned up and the
				// records it was to replace left where they are.
				path := filepath.Join(dir, "managed.json")
				require.NoError(t, os.Mkdir(path, 0o755))
				witness := filepath.Join(path, "records.json")
				require.NoError(t, os.WriteFile(witness, original, 0o600))
				l := &ledger{}
				l.upsert(validEntry())
				return l, path, witness
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			l, path, witness := tc.setUp(t, dir)

			assert.Error(t, l.save(path))

			after, err := os.ReadFile(witness)
			require.NoError(t, err)
			assert.Equal(t, string(original), string(after))

			left, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, left, 1, "no temp file may be left behind")
			assert.Equal(t, "managed.json", left[0].Name())
		})
	}
}

// TestLockLedger_ExcludesASecondHolder pins that the lock actually excludes,
// and that "someone else is syncing" is distinguishable from a broken lock:
// the caller reports the two differently.
func TestLockLedger_ExcludesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.lock")

	held, err := lockLedger(path)
	require.NoError(t, err)
	require.NotNil(t, held)

	second, err := lockLedger(path)
	assert.Nil(t, second)
	require.Error(t, err)
	assert.ErrorIs(t, err, errLedgerLocked)

	require.NoError(t, held.Unlock())

	again, err := lockLedger(path)
	require.NoError(t, err)
	require.NotNil(t, again)
	require.NoError(t, again.Unlock())
}

// TestLockLedger_CreatesTheParentDirectory covers a first run on a machine
// with no config directory yet: taking the lock must not fail merely because
// nothing has been written there before.
func TestLockLedger_CreatesTheParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh", "managed.lock")

	held, err := lockLedger(path)

	require.NoError(t, err)
	require.NotNil(t, held)
	assert.FileExists(t, path)
	require.NoError(t, held.Unlock())
}
