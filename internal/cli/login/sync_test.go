// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

const (
	// The installation ids are distinct in their first twelve hex characters,
	// which is what a derived name carries, so two of them name two profiles.
	installOne = "11111111-1111-4111-8111-111111111111"
	installTwo = "22222222-2222-4222-8222-222222222222"

	// The names those ids derive with the org, tenant, and installation names
	// the fixtures use. They are written out rather than derived here, so a
	// change to name derivation shows up as a failing sync test too.
	nameOne = "acme-default-prod-111111111111"
	nameTwo = "acme-default-staging-222222222222"

	// stateActive is the ordinary installation state; the rest of the enum is
	// exercised by the classification table.
	stateActive = "active"
)

// unreadableName is a valid profile name whose path is longer than one
// filesystem component may be, so every stat of it fails with an error that is
// neither "it is not there" nor "it is not a regular file" — the third answer,
// "this could not be read at all". It is a length rather than a permission bit
// because a permission bit is readable by root, and a test may run as one.
var unreadableName = strings.Repeat("u", 300)

// stubCloudClient answers with a fixed snapshot, so a sync test exercises the
// algorithm rather than the HTTP client the client's own tests already cover.
type stubCloudClient struct {
	snapshot Snapshot
	err      error
	calls    int
	// onCall runs before the answer is returned, so a test can observe the
	// state of the world at the moment the control plane is asked.
	onCall func()
}

func (c *stubCloudClient) ListInstallations(_ context.Context, _ string) (Snapshot, error) {
	c.calls++
	if c.onCall != nil {
		c.onCall()
	}
	return c.snapshot, c.err
}

// stubVerifier accepts every profile unless err is set, and records what it
// was asked to verify.
type stubVerifier struct {
	err   error
	paths []string
	// onVerify runs before the answer is returned, so a test can change the
	// world during the slowest step of a publication — the window between a
	// destination being identified and the rendered profile reaching it.
	onVerify func()
}

func (v *stubVerifier) Verify(path, _, _ string) error {
	v.paths = append(v.paths, path)
	if v.onVerify != nil {
		v.onVerify()
	}
	return v.err
}

// syncFixture is a config directory with a store, an active profile the sync
// never manages, and a stub control plane.
type syncFixture struct {
	t        *testing.T
	root     string
	store    *store.Store
	client   *stubCloudClient
	verifier *stubVerifier
	out      *bytes.Buffer
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	root := t.TempDir()
	s := store.New(root)
	require.NoError(t, os.MkdirAll(s.ProfilesDir(), 0o755))
	require.NoError(t, os.WriteFile(s.ProfilePath("default"), []byte(store.StubTemplate), 0o644))
	require.NoError(t, s.Use("default"))

	return &syncFixture{
		t:        t,
		root:     root,
		store:    s,
		client:   &stubCloudClient{},
		verifier: &stubVerifier{},
		out:      &bytes.Buffer{},
	}
}

func (f *syncFixture) deps() syncDeps {
	return syncDeps{
		Client:   f.client,
		Store:    f.store,
		Verifier: f.verifier,
		Out:      f.out,
		Theme:    theme.New(""),
	}
}

// answer points the stub control plane at an authoritative snapshot of these
// installations.
func (f *syncFixture) answer(installations ...Installation) {
	f.t.Helper()
	f.client.snapshot = Snapshot{Installations: installations, Authoritative: true}
}

// sync runs a whole sync against the fixture's platform.
func (f *syncFixture) sync() syncResult {
	f.t.Helper()
	return syncProfiles(context.Background(), f.deps(), f.platform(), "Bearer "+testToken,
		cliAuth("", ""), oidcAuth(f.t, nil))
}

func (f *syncFixture) platform() platform {
	return platform{Origin: testOrigin, Issuer: testIssuer}
}

// content returns the bytes a generated profile for id carries.
func (f *syncFixture) content(id string) []byte {
	return renderProfile(testEndpoint, id, cliAuth("", ""))
}

// read returns the contents of the named profile.
func (f *syncFixture) read(name string) []byte {
	f.t.Helper()
	data, err := os.ReadFile(f.store.ProfilePath(name))
	require.NoError(f.t, err)
	return data
}

// exists reports whether a regular file sits at the named profile's path.
func (f *syncFixture) exists(name string) bool {
	f.t.Helper()
	info, err := os.Lstat(f.store.ProfilePath(name))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	require.NoError(f.t, err)
	return info.Mode().IsRegular()
}

// writeProfile puts content at a profile name as a plain file, which is how
// the fixtures stand in for a file the user wrote.
func (f *syncFixture) writeProfile(name string, content []byte) {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(f.store.ProfilePath(name), content, 0o644))
}

// replaceProfile puts content at a profile name the way an editor that saves
// through a temp file does: a whole new file is renamed over the name, so a
// different file answers to it afterwards.
func (f *syncFixture) replaceProfile(name string, content []byte) {
	f.t.Helper()
	temp := filepath.Join(f.root, "editor-save")
	require.NoError(f.t, os.WriteFile(temp, content, 0o644))
	require.NoError(f.t, os.Rename(temp, f.store.ProfilePath(name)))
}

// pointActiveAt writes the active pointer directly, so a test can express a
// pointer the store's own Use would refuse — one naming a profile that is not
// there.
func (f *syncFixture) pointActiveAt(name string) {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(filepath.Join(f.root, "active"), []byte(name+"\n"), 0o600))
}

// writeLedger writes a ledger file at the store's ledger path.
func (f *syncFixture) writeLedger(entries ...any) {
	f.t.Helper()
	if entries == nil {
		entries = []any{}
	}
	data, err := json.Marshal(map[string]any{"schemaVersion": ledgerSchemaVersion, "entries": entries})
	require.NoError(f.t, err)
	require.NoError(f.t, os.WriteFile(f.store.ManagedLedgerPath(), data, 0o600))
}

// entries returns what the ledger file records, decoded without validation so
// a test sees what was written rather than what a later load makes of it.
func (f *syncFixture) entries() []*ledgerEntry {
	f.t.Helper()
	if _, err := os.Stat(f.store.ManagedLedgerPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return savedEntries(f.t, f.store.ManagedLedgerPath())
}

// entryFor returns the recorded entry for an installation, or nil.
func (f *syncFixture) entryFor(installationID string) *ledgerEntry {
	f.t.Helper()
	for _, e := range f.entries() {
		if e.InstallationID == installationID {
			return e
		}
	}
	return nil
}

// tempFiles returns the publication temp files left in the profiles
// directory.
func (f *syncFixture) tempFiles() []string {
	f.t.Helper()
	dir, err := os.ReadDir(f.store.ProfilesDir())
	require.NoError(f.t, err)
	var temps []string
	for _, e := range dir {
		if tempNameRE.MatchString(e.Name()) {
			temps = append(temps, e.Name())
		}
	}
	return temps
}

// installation returns a record the fixtures' derived names are built from.
func installation(id, installationName, state string) Installation {
	return Installation{
		InstallationID:   id,
		InstallationName: installationName,
		TenantName:       "default",
		OrgName:          "acme",
		Endpoint:         testEndpoint,
		State:            state,
	}
}

// managedEntry returns a raw ledger entry in the given state, with fields
// merged over it, so a test can express any state an interrupted run leaves.
func managedEntry(state entryState, name, installationID string, fields rawEntry) rawEntry {
	e := rawEntry{
		"controlPlane":   testOrigin,
		"installationId": installationID,
		"name":           name,
		"state":          string(state),
	}
	for k, v := range fields {
		e[k] = v
	}
	return e
}

// writeTempFileNamed writes a publication temp file into the profiles
// directory and returns its path.
func (f *syncFixture) writeTempFileNamed(tempName string, content []byte) string {
	f.t.Helper()
	path := filepath.Join(f.store.ProfilesDir(), tempName)
	require.NoError(f.t, os.WriteFile(path, content, 0o600))
	return path
}

// linkTemp publishes a temp file at a profile name the way publication does,
// so the destination and the temp are the same file: the witness that proves
// the publication was this formae's.
func (f *syncFixture) linkTemp(tempName, name string, content []byte) {
	f.t.Helper()
	temp := f.writeTempFileNamed(tempName, content)
	require.NoError(f.t, os.Link(temp, f.store.ProfilePath(name)))
}

// tempExists reports whether a publication temp file is still there.
func (f *syncFixture) tempExists(tempName string) bool {
	f.t.Helper()
	_, err := os.Lstat(filepath.Join(f.store.ProfilesDir(), tempName))
	return err == nil
}

// recoverOnly runs a sync whose enumeration fails, so nothing derived from
// this run's snapshot happens and what is left is the recovery step alone.
func (f *syncFixture) recoverOnly() syncResult {
	f.t.Helper()
	f.client.err = &cloudTransientError{Cause: errors.New("the control plane returned HTTP 503")}
	f.client.snapshot = Snapshot{}
	result := f.sync()
	require.Error(f.t, result.Fatal, "the enumeration failed, so the run did not complete")
	return result
}

// warningsContaining returns the warnings mentioning substr.
func warningsContaining(result syncResult, substr string) []string {
	var found []string
	for _, w := range result.Warnings {
		if strings.Contains(w, substr) {
			found = append(found, w)
		}
	}
	return found
}

func TestSyncCreatesAProfilePerInstallation(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Empty(t, result.Warnings)
	assert.Equal(t, 2, result.Created)
	assert.Equal(t, 2, result.DesiredCount)
	assert.Equal(t, 2, result.DesiredSatisfied)
	assert.True(t, result.StaleManagedForOrigin)

	for name, id := range map[string]string{nameOne: installOne, nameTwo: installTwo} {
		assert.Equal(t, f.content(id), f.read(name), "profile %s carries the rendered bytes", name)
		info, err := os.Stat(f.store.ProfilePath(name))
		require.NoError(t, err)
		assert.Equal(t, generatedProfileMode, info.Mode().Perm())

		entry := f.entryFor(id)
		require.NotNil(t, entry, "an entry is recorded for %s", name)
		assert.Equal(t, entryOwned, entry.State)
		assert.Equal(t, name, entry.Name)
		assert.Equal(t, testOrigin, entry.ControlPlane)
		assert.Equal(t, fingerprint(f.content(id)), entry.Fingerprint)
		assert.Empty(t, entry.TempName)
		assert.Empty(t, entry.AltFingerprint)
		assert.Empty(t, entry.SupersedesName)
		assert.Contains(t, f.out.String(), "created profile "+name)
	}
	assert.Empty(t, f.tempFiles(), "a promoted publication leaves no temp file behind")
	assert.Len(t, f.verifier.paths, 2, "every profile is verified before it is published")
}

func TestSyncSecondRunWritesNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	before, err := os.Stat(f.store.ProfilePath(nameOne))
	require.NoError(t, err)
	fingerprintBefore := f.entryFor(installOne).Fingerprint

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Empty(t, result.Warnings)
	assert.Zero(t, result.Created)
	assert.Zero(t, result.Updated)
	assert.Zero(t, result.Renamed)
	assert.Zero(t, result.Pruned)
	assert.Equal(t, 1, result.DesiredSatisfied, "an unchanged profile is still satisfied")
	after, err := os.Stat(f.store.ProfilePath(nameOne))
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "an unchanged profile is not republished")
	assert.Equal(t, fingerprintBefore, f.entryFor(installOne).Fingerprint)
	assert.Empty(t, f.tempFiles())
}

func TestSyncUpdatesEveryProfileWhenTheEndpointChanges(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)

	moved := []Installation{
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", stateActive),
	}
	moved[0].Endpoint = testOtherOrigin
	moved[1].Endpoint = testOtherOrigin
	f.answer(moved...)

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, 2, result.Updated)
	assert.Equal(t, 2, result.DesiredSatisfied)
	for name, id := range map[string]string{nameOne: installOne, nameTwo: installTwo} {
		want := renderProfile(testOtherOrigin, id, cliAuth("", ""))
		assert.Equal(t, want, f.read(name))
		assert.Equal(t, fingerprint(want), f.entryFor(id).Fingerprint)
		assert.Contains(t, f.out.String(), "updated profile "+name)
	}
	assert.Empty(t, f.tempFiles())
}

func TestSyncRepairsAMissingProfile(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, os.Remove(f.store.ProfilePath(nameOne)))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 1, result.DesiredSatisfied)
	assert.Equal(t, f.content(installOne), f.read(nameOne))
	assert.Equal(t, entryOwned, f.entryFor(installOne).State)
	assert.Empty(t, f.tempFiles())
}

func TestSyncRenamesWhenTheDerivedNameChanges(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)

	f.answer(installation(installOne, "renamed", stateActive))
	const renamed = "acme-default-renamed-111111111111"

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, 1, result.Renamed)
	assert.Equal(t, 1, result.DesiredSatisfied)
	assert.False(t, f.exists(nameOne), "the old name is removed")
	assert.Equal(t, f.content(installOne), f.read(renamed))
	entry := f.entryFor(installOne)
	require.NotNil(t, entry)
	assert.Equal(t, renamed, entry.Name)
	assert.Equal(t, entryOwned, entry.State)
	assert.Empty(t, entry.SupersedesName)
	assert.Empty(t, entry.TempName)
	assert.Contains(t, f.out.String(), fmt.Sprintf("renamed profile %s to %s", nameOne, renamed))
	assert.Empty(t, f.tempFiles())
}

func TestSyncEnumerationFailureChangesNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	before := f.entries()

	f.client.err = &cloudTransientError{Cause: errors.New("the control plane returned HTTP 503")}
	f.client.snapshot = Snapshot{}

	result := f.sync()

	require.Error(t, result.Fatal)
	assert.True(t, f.exists(nameOne), "a control plane that is down never reads as lost grants")
	assert.Equal(t, before, f.entries())
	assert.Zero(t, result.Pruned)
	assert.Zero(t, result.Created)
}

func TestSyncPublishesThroughTheRealVerifier(t *testing.T) {
	f := newSyncFixture(t)
	deps := f.deps()
	deps.Verifier = newProfileVerifier()
	f.answer(installation(installOne, "prod", stateActive))

	result := syncProfiles(context.Background(), deps, f.platform(), "Bearer "+testToken,
		cliAuth("", ""), oidcAuth(t, nil))

	require.NoError(t, result.Fatal)
	assert.Equal(t, 1, result.Created)
	hosted := loadHosted(t, f.store.ProfilePath(nameOne))
	assert.Equal(t, testEndpoint, hosted.Endpoint)
	assert.Equal(t, installOne, hosted.Installation)
}

func TestSyncSkipsAProfileTheVerifierRefuses(t *testing.T) {
	f := newSyncFixture(t)
	f.verifier.err = errors.New("the generated profile does not load")
	f.answer(installation(installOne, "prod", stateActive))

	result := f.sync()

	assert.False(t, f.exists(nameOne), "an unverified profile is never published")
	assert.Empty(t, f.entries(), "and nothing claims to own it")
	assert.Zero(t, result.DesiredSatisfied)
	assert.Equal(t, 1, result.DesiredCount)
	assert.NotEmpty(t, warningsContaining(result, nameOne))
	assert.Empty(t, f.tempFiles(), "an abandoned publication leaves no temp file behind")
}

// bucket is what a run does with an installation in a given state.
type bucket int

const (
	bucketDesired bucket = iota
	bucketTerminal
	bucketUnrecognised
)

func TestSyncClassifiesEveryInstallationState(t *testing.T) {
	tests := []struct {
		state string
		want  bucket
	}{
		{state: "requested", want: bucketDesired},
		{state: "provisioning", want: bucketDesired},
		{state: "active", want: bucketDesired},
		{state: "failed", want: bucketDesired},
		{state: "suspending", want: bucketDesired},
		{state: "suspended", want: bucketDesired},
		{state: "destroying", want: bucketTerminal},
		{state: "destroyed", want: bucketTerminal},
		{state: "reaped", want: bucketTerminal},
		// Comparison is exact and lowercase, so a case variant is a state this
		// formae has not been told about.
		{state: "Active", want: bucketUnrecognised},
		{state: "hibernating", want: bucketUnrecognised},
		{state: "", want: bucketUnrecognised},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			f := newSyncFixture(t)
			f.answer(installation(installOne, "prod", stateActive))
			require.NoError(t, f.sync().Fatal)
			f.answer(installation(installOne, "prod", tt.state))

			result := f.sync()

			require.NoError(t, result.Fatal)
			switch tt.want {
			case bucketDesired:
				assert.True(t, f.exists(nameOne), "a desired state keeps its profile")
				assert.NotNil(t, f.entryFor(installOne))
				assert.Equal(t, 1, result.DesiredCount)
				assert.Equal(t, 1, result.DesiredSatisfied)
				assert.Zero(t, result.Pruned)
				assert.Empty(t, result.Warnings)
			case bucketTerminal:
				assert.False(t, f.exists(nameOne), "a terminal state prunes its profile")
				assert.Nil(t, f.entryFor(installOne))
				assert.Equal(t, 1, result.Pruned)
				assert.Zero(t, result.DesiredCount)
				assert.Contains(t, f.out.String(), "removed profile "+nameOne)
			case bucketUnrecognised:
				assert.True(t, f.exists(nameOne), "an unrecognised state is present, not absent")
				assert.NotNil(t, f.entryFor(installOne))
				assert.Zero(t, result.Pruned)
				assert.Zero(t, result.DesiredCount)
				warnings := warningsContaining(result, installOne)
				require.Len(t, warnings, 1)
				assert.Contains(t, warnings[0], fmt.Sprintf("%q", tt.state))
			}
		})
	}
}

func TestSyncUnrecognisedStateDoesNotStopASiblingBeingPruned(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)

	f.answer(installation(installOne, "prod", "hibernating"), installation(installTwo, "staging", "destroyed"))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.True(t, f.exists(nameOne), "the unrecognised installation keeps its profile")
	assert.False(t, f.exists(nameTwo), "and the run keeps its authority to prune the sibling")
	assert.Equal(t, 1, result.Pruned)
}

func TestSyncReapedPrunesAndALaterLoginRestoresTheSameName(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)

	f.answer(installation(installOne, "prod", "reaped"))
	require.NoError(t, f.sync().Fatal)
	require.False(t, f.exists(nameOne))

	f.answer(installation(installOne, "prod", stateActive))
	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.True(t, f.exists(nameOne), "reactivation restores the profile under exactly the same name")
	assert.Equal(t, 1, result.Created)
}

func TestSyncPrunesAProfileWhoseInstallationIsAbsent(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)

	f.answer(installation(installTwo, "staging", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, 1, result.Pruned)
	assert.False(t, f.exists(nameOne))
	assert.Nil(t, f.entryFor(installOne))
	assert.True(t, f.exists(nameTwo), "the installation still granted keeps its profile")
}

func TestSyncLeavesEntriesForOtherOriginsAlone(t *testing.T) {
	f := newSyncFixture(t)
	other := f.content(installTwo)
	f.writeProfile("staging-profile", other)
	f.writeLedger(rawEntry{
		"controlPlane":   testOtherOrigin,
		"installationId": installTwo,
		"name":           "staging-profile",
		"state":          "owned",
		"fingerprint":    fingerprint(other),
	})
	f.answer(installation(installOne, "prod", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.True(t, f.exists("staging-profile"), "a sync against one control plane never prunes another's profiles")
	entry := f.entryFor(installTwo)
	require.NotNil(t, entry)
	assert.Equal(t, testOtherOrigin, entry.ControlPlane)
}

func TestSyncNonAuthoritativeSnapshotWritesButPrunesNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	// The first installation is missing from a response that cannot be
	// believed to be complete, and a second one is new.
	f.client.snapshot = Snapshot{
		Installations: []Installation{installation(installTwo, "staging", stateActive)},
		Authoritative: false,
	}

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.True(t, f.exists(nameOne), "partial knowledge may not subtract")
	assert.NotNil(t, f.entryFor(installOne))
	assert.Zero(t, result.Pruned)
	assert.True(t, f.exists(nameTwo), "and may still add")
	assert.Equal(t, 1, result.Created)
}

func TestSyncNonAuthoritativeSnapshotKeepsAnEntryWhoseFileIsGone(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, os.Remove(f.store.ProfilePath(nameOne)))
	f.client.snapshot = Snapshot{Authoritative: false}

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.NotNil(t, f.entryFor(installOne),
		"on a partial response the installation's absence is not established, so the repair path is kept")
	assert.False(t, result.StaleManagedForOrigin)
}

func TestSyncDropsAnEntryWhoseFileAndInstallationAreBothGone(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, os.Remove(f.store.ProfilePath(nameOne)))
	f.answer()

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Nil(t, f.entryFor(installOne), "a deleted managed profile leaves no permanent record")
	assert.Zero(t, result.Pruned, "nothing was removed: the file was already gone")
	assert.False(t, result.StaleManagedForOrigin)
}

func TestPruneAllRemovesEveryProfileForOneOrigin(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)
	theirs := []byte("a profile the user wrote\n")
	f.writeProfile("handwritten", theirs)
	f.client.calls = 0

	result := pruneAll(f.deps(), testOrigin)

	require.NoError(t, result.Fatal)
	assert.Equal(t, 2, result.Pruned)
	assert.False(t, f.exists(nameOne))
	assert.False(t, f.exists(nameTwo))
	assert.Empty(t, f.entries())
	assert.Equal(t, theirs, f.read("handwritten"), "a profile formae did not write is never removed")
	assert.True(t, f.exists("default"), "the active profile survives")
	assert.Zero(t, f.client.calls, "signing out asks no control plane anything")
	assert.False(t, result.StaleManagedForOrigin)
}

func TestSyncKeepsAPrunableActiveProfileUntilTheUserSwitchesAway(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, f.store.Use(nameOne))
	f.answer()

	kept := f.sync()

	require.NoError(t, kept.Fatal)
	assert.Zero(t, kept.Pruned)
	assert.True(t, f.exists(nameOne), "a dangling active pointer is worse than a stale profile")
	require.NotNil(t, f.entryFor(installOne), "the entry is kept so a later login prunes it")
	warnings := warningsContaining(kept, nameOne)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "formae profile use")
	assert.Contains(t, warnings[0], "your access to installation "+installOne+" is gone",
		"on this path the grant really has vanished, which is why the profile is due for removal")
	assert.Contains(t, warnings[0], "sign in again to remove it",
		"the next sign-in is what removes it here, and it derives nothing this run has not already")

	require.NoError(t, f.store.Use("default"))
	pruned := f.sync()

	require.NoError(t, pruned.Fatal)
	assert.Equal(t, 1, pruned.Pruned)
	assert.False(t, f.exists(nameOne))
	assert.Nil(t, f.entryFor(installOne))
}

func TestSyncDoesNotRenameTheActiveProfileButKeepsItUpToDate(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, f.store.Use(nameOne))

	moved := installation(installOne, "renamed", stateActive)
	moved.Endpoint = testOtherOrigin
	f.answer(moved)
	const renamed = "acme-default-renamed-111111111111"

	kept := f.sync()

	require.NoError(t, kept.Fatal)
	assert.Zero(t, kept.Renamed)
	assert.Equal(t, 1, kept.Updated, "content updates still apply in place")
	assert.Equal(t, 1, kept.DesiredSatisfied)
	assert.True(t, f.exists(nameOne), "the active pointer stays valid")
	assert.False(t, f.exists(renamed))
	want := renderProfile(testOtherOrigin, installOne, cliAuth("", ""))
	assert.Equal(t, want, f.read(nameOne))
	assert.Equal(t, fingerprint(want), f.entryFor(installOne).Fingerprint)
	warnings := warningsContaining(kept, renamed)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "formae profile use")

	require.NoError(t, f.store.Use("default"))
	moved2 := f.sync()

	require.NoError(t, moved2.Fatal)
	assert.Equal(t, 1, moved2.Renamed)
	assert.False(t, f.exists(nameOne))
	assert.Equal(t, want, f.read(renamed))
}

func TestSyncResolvesTheActiveProfileThroughASymlink(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	// The active profile is reached through a symlink to the managed file, so
	// only os.SameFile — never a name comparison — recognises it.
	require.NoError(t, os.Symlink(f.store.ProfilePath(nameOne), f.store.ProfilePath("current")))
	require.NoError(t, f.store.Use("current"))
	f.answer()

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Zero(t, result.Pruned)
	assert.True(t, f.exists(nameOne))
	assert.NotEmpty(t, warningsContaining(result, nameOne))
}

func TestSyncDeletesAndRenamesNothingWhenTheActiveProfileCannotBeIdentified(t *testing.T) {
	tests := []struct {
		name    string
		breakIt func(f *syncFixture)
	}{
		{
			name:    "the active pointer dangles",
			breakIt: func(f *syncFixture) { f.pointActiveAt("ghost") },
		},
		{
			name: "there is no active pointer",
			breakIt: func(f *syncFixture) {
				require.NoError(f.t, os.Remove(filepath.Join(f.root, "active")))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
			require.NoError(t, f.sync().Fatal)
			tt.breakIt(f)
			// One installation's grant is gone and the other has been renamed
			// upstream: without an identity, neither may be acted on.
			f.answer(installation(installTwo, "renamed", stateActive))

			result := f.sync()

			assert.Zero(t, result.Pruned)
			assert.Zero(t, result.Renamed)
			assert.True(t, f.exists(nameOne), "no managed file is deleted")
			assert.True(t, f.exists(nameTwo), "and none is renamed")
			assert.False(t, f.exists("acme-default-renamed-222222222222"))
			assert.NotEmpty(t, warningsContaining(result, nameOne))
			assert.NotEmpty(t, warningsContaining(result, nameTwo))
		})
	}
}

// The recovery table: one case per state an interrupted run can leave behind.
// Every case runs against an enumeration that fails, so nothing derived from
// this run's snapshot can happen and what is asserted is recovery alone.
func TestSyncRecoversEveryInterruptedState(t *testing.T) {
	const tempName = ".tmp-0123456789abcdef.pkl"
	const renamedName = "acme-default-renamed-111111111111"
	oldContent := renderProfile(testEndpoint, installOne, cliAuth("", ""))
	newContent := renderProfile(testOtherOrigin, installOne, cliAuth("", ""))
	theirs := []byte("a profile the user wrote\n")

	tests := []struct {
		name   string
		setUp  func(f *syncFixture)
		assert func(t *testing.T, f *syncFixture, result syncResult)
	}{
		{
			name: "intent committed, nothing published",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				assert.Nil(t, f.entryFor(installOne), "nothing on disk is ours, so nothing claims to be")
				assert.False(t, f.exists(nameOne))
			},
		},
		{
			name: "temp written, never published",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
				f.writeTempFileNamed(tempName, oldContent)
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.False(t, f.exists(nameOne))
				assert.False(t, f.tempExists(tempName), "an abandoned entry's temp file goes with it")
			},
		},
		{
			name: "published, result not committed",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
				f.linkTemp(tempName, nameOne, oldContent)
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e, "the witness proves the publication was ours")
				assert.Equal(t, entryOwned, e.State)
				assert.Equal(t, fingerprint(oldContent), e.Fingerprint)
				assert.Empty(t, e.TempName)
				assert.Equal(t, oldContent, f.read(nameOne))
				assert.False(t, f.tempExists(tempName), "a promoted entry's temp file is removed")
			},
		},
		{
			name: "the name was taken by another file",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
				f.writeTempFileNamed(tempName, oldContent)
				f.writeProfile(nameOne, theirs)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.Equal(t, theirs, f.read(nameOne), "the file at that name was never ours")
				assert.False(t, f.tempExists(tempName))
				assert.NotEmpty(t, warningsContaining(result, nameOne))
			},
		},
		{
			name: "a byte-identical file with no witness is not adopted",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
				// The temp exists and the destination holds exactly the bytes
				// we render, but they are two files: nothing proves this
				// publication happened.
				f.writeTempFileNamed(tempName, oldContent)
				f.writeProfile(nameOne, oldContent)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne), "a hash cannot tell our file from an identical one")
				assert.True(t, f.exists(nameOne), "and the file is left alone")
				assert.NotEmpty(t, warningsContaining(result, nameOne))
			},
		},
		{
			name: "the witness is gone",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
				f.writeProfile(nameOne, oldContent)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.True(t, f.exists(nameOne))
				assert.NotEmpty(t, warningsContaining(result, nameOne))
			},
		},
		{
			name: "the fallback write was interrupted",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent), "tempName": tempName,
				}))
				f.writeTempFileNamed(tempName, oldContent)
				// A destination written byte by byte and cut off partway
				// hashes nothing anyone recorded, and shares no inode with the
				// temp file.
				f.writeProfile(nameOne, oldContent[:len(oldContent)/2])
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.True(t, f.exists(nameOne), "a truncated file is contained, not deleted")
				warnings := warningsContaining(result, nameOne)
				require.Len(t, warnings, 1)
				assert.Contains(t, warnings[0], "by hand")
			},
		},
		{
			name: "a rename published the new name but had not removed the old one",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, renamedName, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"supersedesName": nameOne,
					"tempName":       tempName,
				}))
				f.writeProfile(nameOne, oldContent)
				f.linkTemp(tempName, renamedName, newContent)
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e)
				assert.Equal(t, entryOwned, e.State)
				assert.Equal(t, renamedName, e.Name)
				assert.Equal(t, fingerprint(newContent), e.Fingerprint)
				assert.Empty(t, e.SupersedesName)
				assert.Empty(t, e.AltFingerprint)
				assert.False(t, f.exists(nameOne), "the old name is removed once the new one is proven ours")
				assert.Equal(t, newContent, f.read(renamedName))
				assert.False(t, f.tempExists(tempName))
			},
		},
		{
			name: "a rename never published the new name",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, renamedName, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"supersedesName": nameOne,
					"tempName":       tempName,
				}))
				f.writeProfile(nameOne, oldContent)
				f.writeTempFileNamed(tempName, newContent)
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e, "the file at the old name is still ours")
				assert.Equal(t, entryOwned, e.State)
				assert.Equal(t, nameOne, e.Name)
				assert.Equal(t, fingerprint(oldContent), e.Fingerprint)
				assert.Empty(t, e.SupersedesName)
				assert.True(t, f.exists(nameOne))
				assert.False(t, f.exists(renamedName))
				assert.False(t, f.tempExists(tempName))
			},
		},
		{
			name: "a rename whose old name cannot be read leaves the entry alone",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, renamedName, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"supersedesName": unreadableName,
					"tempName":       tempName,
				}))
				f.writeTempFileNamed(tempName, newContent)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e, "what is at the old name was not established, so nothing is forfeited")
				assert.Equal(t, entryPending, e.State)
				assert.Equal(t, renamedName, e.Name)
				assert.Equal(t, unreadableName, e.SupersedesName)
				assert.True(t, f.tempExists(tempName))
				warnings := warningsContaining(result, "could not be read")
				require.Len(t, warnings, 1)
				assert.NotContains(t, warnings[0], "no longer",
					"an unreadable file was not edited, and is never described as if it had been")
			},
		},
		{
			name: "a rename published the new name but cannot read the old one",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, renamedName, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"supersedesName": unreadableName,
					"tempName":       tempName,
				}))
				f.linkTemp(tempName, renamedName, newContent)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e)
				assert.Equal(t, entryOwned, e.State, "the witness proves the new name is ours")
				assert.Equal(t, renamedName, e.Name)
				warnings := warningsContaining(result, "could not be read")
				require.Len(t, warnings, 1)
				assert.NotContains(t, warnings[0], "no longer one formae wrote",
					"an unreadable file was not edited, and is never described as if it had been")
			},
		},
		{
			name: "an update was interrupted before the replacement",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"tempName":       tempName,
				}))
				f.writeProfile(nameOne, oldContent)
				f.writeTempFileNamed(tempName, newContent)
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e, "the file is one we already owned, so it stays ours")
				assert.Equal(t, entryOwned, e.State)
				assert.Equal(t, fingerprint(oldContent), e.Fingerprint,
					"the entry records the content that is actually on disk")
				assert.Empty(t, e.AltFingerprint)
				assert.Equal(t, oldContent, f.read(nameOne))
				assert.False(t, f.tempExists(tempName))
			},
		},
		{
			name: "an update was interrupted after the replacement",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"tempName":       tempName,
				}))
				// The replacement is a rename, so the temp file is gone.
				f.writeProfile(nameOne, newContent)
			},
			assert: func(t *testing.T, f *syncFixture, _ syncResult) {
				e := f.entryFor(installOne)
				require.NotNil(t, e)
				assert.Equal(t, entryOwned, e.State)
				assert.Equal(t, fingerprint(newContent), e.Fingerprint)
				assert.Empty(t, e.AltFingerprint)
				assert.Equal(t, newContent, f.read(nameOne))
			},
		},
		{
			name: "an update was interrupted and the file was edited",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
					"fingerprint":    fingerprint(newContent),
					"altFingerprint": fingerprint(oldContent),
					"tempName":       tempName,
				}))
				f.writeProfile(nameOne, theirs)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.Equal(t, theirs, f.read(nameOne))
				assert.NotEmpty(t, warningsContaining(result, nameOne))
			},
		},
		{
			name: "a deletion was interrupted before the file was removed",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryDeleting, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent),
				}))
				f.writeProfile(nameOne, oldContent)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.False(t, f.exists(nameOne), "a committed deletion is finished")
				assert.Equal(t, 1, result.Pruned)
			},
		},
		{
			name: "a deletion was interrupted after the file was removed",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryDeleting, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent),
				}))
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.Zero(t, result.Pruned)
			},
		},
		{
			name: "a deletion whose file was edited leaves it alone",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryDeleting, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent),
				}))
				f.writeProfile(nameOne, theirs)
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				assert.Equal(t, theirs, f.read(nameOne))
				assert.Zero(t, result.Pruned)
				assert.NotEmpty(t, warningsContaining(result, nameOne))
			},
		},
		{
			name: "a deletion of a symlink removes nothing",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryDeleting, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent),
				}))
				f.writeProfile("target", oldContent)
				require.NoError(f.t, os.Symlink(f.store.ProfilePath("target"), f.store.ProfilePath(nameOne)))
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				assert.Nil(t, f.entryFor(installOne))
				_, err := os.Lstat(f.store.ProfilePath(nameOne))
				assert.NoError(t, err, "a symlink at a managed path is never removed")
				assert.True(t, f.exists("target"), "and neither is what it points at")
				assert.Zero(t, result.Pruned)
			},
		},
		{
			name: "a deletion of the active profile keeps the file and the entry",
			setUp: func(f *syncFixture) {
				f.writeLedger(managedEntry(entryDeleting, nameOne, installOne, rawEntry{
					"fingerprint": fingerprint(oldContent),
				}))
				f.writeProfile(nameOne, oldContent)
				require.NoError(f.t, f.store.Use(nameOne))
			},
			assert: func(t *testing.T, f *syncFixture, result syncResult) {
				require.NotNil(t, f.entryFor(installOne), "the entry is kept so a later login finishes it")
				assert.True(t, f.exists(nameOne))
				assert.Zero(t, result.Pruned)
				assert.NotEmpty(t, warningsContaining(result, nameOne))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			tt.setUp(f)

			result := f.recoverOnly()

			tt.assert(t, f, result)
		})
	}
}

func TestSyncRetriesAPublicationThatNeverLanded(t *testing.T) {
	f := newSyncFixture(t)
	const tempName = ".tmp-0123456789abcdef.pkl"
	f.writeLedger(managedEntry(entryPending, nameOne, installOne, rawEntry{
		"fingerprint": fingerprint(f.content(installOne)), "tempName": tempName,
	}))
	f.answer(installation(installOne, "prod", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, 1, result.Created, "recovery settles the entry and the desired step publishes it")
	assert.Equal(t, f.content(installOne), f.read(nameOne))
	e := f.entryFor(installOne)
	require.NotNil(t, e)
	assert.Equal(t, entryOwned, e.State)
	assert.Empty(t, f.tempFiles())
}

func TestSyncLeavesAnUnreferencedTempFileAlone(t *testing.T) {
	f := newSyncFixture(t)
	// Shaped exactly like ours, but no entry names it: the name is committed
	// before the file is created, so a temp nobody recorded is not ours.
	const orphan = ".tmp-fedcba9876543210.pkl"
	f.writeTempFileNamed(orphan, []byte("someone else's temp\n"))
	f.answer(installation(installOne, "prod", stateActive))

	require.NoError(t, f.sync().Fatal)

	assert.True(t, f.tempExists(orphan), "a temp file is removed only when an entry names it")
}

// failSaveAfter makes the nth-and-later ledger writes fail, which is how a
// test reaches the boundary between committing an intent and committing its
// result. It returns the function that restores real writes.
func failSaveAfter(t *testing.T, n int) func() {
	t.Helper()
	original := saveLedger
	t.Cleanup(func() { saveLedger = original })
	calls := 0
	saveLedger = func(l *ledger, path string) error {
		calls++
		if calls > n {
			return errors.New("the managed-profile ledger could not be written")
		}
		return original(l, path)
	}
	return func() { saveLedger = original }
}

func TestSyncNeverRemovesAProfileWithNoLedgerEntry(t *testing.T) {
	f := newSyncFixture(t)
	// A hosted profile at exactly the name this formae would derive, pointing
	// at exactly the installation whose grant is gone — and recorded by
	// nothing. "This profile points at hosted" is not "login wrote this file".
	hosted := f.content(installOne)
	f.writeProfile(nameOne, hosted)
	f.answer()

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Zero(t, result.Pruned)
	assert.Equal(t, hosted, f.read(nameOne))
	assert.Empty(t, f.entries())
}

func TestSyncNeverOverwritesAFileAtTheDerivedName(t *testing.T) {
	f := newSyncFixture(t)
	theirs := []byte("a profile the user wrote\n")
	f.writeProfile(nameOne, theirs)
	// A name differing only in case is a different file on a case-sensitive
	// filesystem and the same one elsewhere; neither may be written over.
	upper := strings.ToUpper(nameTwo)
	f.writeProfile(upper, theirs)
	f.answer(installation(installOne, "prod", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, theirs, f.read(nameOne), "a file formae did not write is never replaced")
	assert.Equal(t, theirs, f.read(upper))
	assert.Empty(t, f.entries(), "and nothing claims to own it")
	assert.Zero(t, result.Created)
	assert.Equal(t, 1, result.DesiredCount)
	assert.Zero(t, result.DesiredSatisfied)
	assert.NotEmpty(t, warningsContaining(result, nameOne))
	assert.Empty(t, f.tempFiles())
}

func TestSyncDoesNotReplaceAProfileThatChangedWhileItWasBeingUpdated(t *testing.T) {
	theirs := []byte("a profile the user wrote\n")

	tests := []struct {
		name string
		// change alters the file the update was authorised against and returns
		// the bytes that must survive.
		change func(f *syncFixture) []byte
	}{
		{
			name: "an editor saved a new file over the name",
			change: func(f *syncFixture) []byte {
				f.replaceProfile(nameOne, theirs)
				return theirs
			},
		},
		{
			name: "the file was edited in place",
			change: func(f *syncFixture) []byte {
				f.writeProfile(nameOne, theirs)
				return theirs
			},
		},
		{
			name: "a different file holding the same bytes took the name",
			change: func(f *syncFixture) []byte {
				content := f.content(installOne)
				f.replaceProfile(nameOne, content)
				return content
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			f.answer(installation(installOne, "prod", stateActive))
			require.NoError(t, f.sync().Fatal)

			moved := installation(installOne, "prod", stateActive)
			moved.Endpoint = testOtherOrigin
			f.answer(moved)

			// The change lands while the replacement is being verified, which
			// is inside the window between the destination being identified
			// and the replacement being renamed over it.
			var want []byte
			var changed os.FileInfo
			f.verifier.onVerify = func() {
				want = tt.change(f)
				info, err := os.Stat(f.store.ProfilePath(nameOne))
				require.NoError(t, err)
				changed = info
			}

			result := f.sync()

			assert.Equal(t, want, f.read(nameOne), "the file that took the name is never written over")
			now, err := os.Stat(f.store.ProfilePath(nameOne))
			require.NoError(t, err)
			assert.True(t, os.SameFile(changed, now), "and it is still the same file")
			assert.Zero(t, result.Updated)
			assert.Zero(t, result.DesiredSatisfied)
			assert.Nil(t, f.entryFor(installOne), "formae stopped managing it rather than replacing it")
			warnings := warningsContaining(result, nameOne)
			require.Len(t, warnings, 1)
			assert.Empty(t, f.tempFiles())
		})
	}
}

func TestSyncDoesNotUpdateAProfileThatStoppedBeingOneWhileItWasBeingUpdated(t *testing.T) {
	tests := []struct {
		name       string
		change     func(f *syncFixture)
		assertFile func(t *testing.T, f *syncFixture)
	}{
		{
			name:   "the name went empty",
			change: func(f *syncFixture) { require.NoError(f.t, os.Remove(f.store.ProfilePath(nameOne))) },
			assertFile: func(t *testing.T, f *syncFixture) {
				assert.False(t, f.exists(nameOne), "nothing was written at a name this run no longer owned")
			},
		},
		{
			name: "a symlink took the name",
			change: func(f *syncFixture) {
				require.NoError(f.t, os.Remove(f.store.ProfilePath(nameOne)))
				require.NoError(f.t, os.Symlink(f.store.ProfilePath("default"), f.store.ProfilePath(nameOne)))
			},
			assertFile: func(t *testing.T, f *syncFixture) {
				assert.Equal(t, []byte(store.StubTemplate), f.read("default"),
					"a symlink's target is never written through")
				info, err := os.Lstat(f.store.ProfilePath(nameOne))
				require.NoError(t, err)
				assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink, "and the symlink itself is not replaced")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			f.answer(installation(installOne, "prod", stateActive))
			require.NoError(t, f.sync().Fatal)
			before := f.entryFor(installOne)

			moved := installation(installOne, "prod", stateActive)
			moved.Endpoint = testOtherOrigin
			f.answer(moved)
			f.verifier.onVerify = func() { tt.change(f) }

			result := f.sync()

			assert.NoError(t, result.Fatal, "a name that is no longer ours is a skip, not a failure")
			tt.assertFile(t, f)
			assert.Zero(t, result.Updated)
			assert.Zero(t, result.DesiredSatisfied)
			assert.Equal(t, before, f.entryFor(installOne),
				"the entry still describes the file it described before the update was attempted")
			assert.NotEmpty(t, warningsContaining(result, nameOne))
			assert.Empty(t, f.tempFiles())
		})
	}
}

func TestSyncStopsManagingAHandEditedProfile(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	edited := append(f.content(installOne), []byte("\n// my own note\n")...)
	f.writeProfile(nameOne, edited)

	adopted := f.sync()

	require.NoError(t, adopted.Fatal)
	assert.Equal(t, edited, f.read(nameOne), "an edited profile is never reverted")
	assert.Nil(t, f.entryFor(installOne), "and stops being ours")
	assert.Zero(t, adopted.DesiredSatisfied)
	warnings := warningsContaining(adopted, nameOne)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "formae profile delete "+nameOne)

	// And with the grant gone it is still not deleted, because nothing
	// records it any more.
	f.answer()
	pruned := f.sync()

	require.NoError(t, pruned.Fatal)
	assert.Zero(t, pruned.Pruned)
	assert.Equal(t, edited, f.read(nameOne))
}

func TestSyncCorruptLedgerDeletesNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, os.WriteFile(f.store.ManagedLedgerPath(), []byte("not json at all"), 0o600))
	f.answer()

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Zero(t, result.Pruned, "an empty ledger authorises no deletion at all")
	assert.True(t, f.exists(nameOne))
	assert.NotEmpty(t, result.Warnings)

	// The unreadable file is left as it is until there is something to
	// record, and the first record replaces it with a ledger this formae
	// wrote.
	f.answer(installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)

	assert.Equal(t, ledgerSchemaVersion, savedSchemaVersion(t, f.store.ManagedLedgerPath()))
	assert.True(t, f.exists(nameTwo))
	assert.True(t, f.exists(nameOne), "the profile nothing records is still not removed")
}

func TestSyncUnknownLedgerSchemaVersionWritesNothingAndDeletesNothing(t *testing.T) {
	f := newSyncFixture(t)
	f.writeProfile(nameTwo, f.content(installTwo))
	data, err := json.Marshal(map[string]any{
		"schemaVersion": ledgerSchemaVersion + 1,
		"entries":       []any{managedEntry(entryOwned, nameTwo, installTwo, rawEntry{"fingerprint": fingerprint(f.content(installTwo))})},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.store.ManagedLedgerPath(), data, 0o600))
	f.answer(installation(installOne, "prod", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal, "signing in still succeeded")
	assert.Zero(t, f.client.calls, "nothing is enumerated when nothing may be written")
	assert.False(t, f.exists(nameOne), "records belonging to a newer formae are not written around")
	assert.True(t, f.exists(nameTwo))
	after, err := os.ReadFile(f.store.ManagedLedgerPath())
	require.NoError(t, err)
	assert.Equal(t, data, after, "a ledger this formae cannot read is not rewritten")
	warnings := warningsContaining(result, f.store.ManagedLedgerPath())
	assert.NotEmpty(t, warnings)
}

func TestSyncQuarantinedEntriesGrantNoAuthority(t *testing.T) {
	f := newSyncFixture(t)
	content := f.content(installOne)
	f.writeProfile(nameOne, content)
	// Two entries sharing an installation id: neither is believed while they
	// conflict, so neither authorises removing the file they name.
	f.writeLedger(
		managedEntry(entryOwned, nameOne, installOne, rawEntry{"fingerprint": fingerprint(content)}),
		managedEntry(entryOwned, nameTwo, installOne, rawEntry{"fingerprint": fingerprint(content)}),
	)
	f.answer()

	quarantined := f.sync()

	require.NoError(t, quarantined.Fatal)
	assert.Zero(t, quarantined.Pruned)
	assert.Equal(t, content, f.read(nameOne))
	assert.Len(t, f.entries(), 2, "a conflicting set is carried forward unchanged")
	assert.NotEmpty(t, quarantined.Warnings)

	// Removing the ledger is the stated remedy, and it deletes nothing.
	require.NoError(t, os.Remove(f.store.ManagedLedgerPath()))
	f.answer(installation(installOne, "prod", stateActive))
	rederived := f.sync()

	require.NoError(t, rederived.Fatal)
	assert.Equal(t, content, f.read(nameOne), "the file at the derived name is the user's until they say otherwise")
	assert.Zero(t, rederived.Created)
	assert.NotEmpty(t, warningsContaining(rederived, nameOne))
}

func TestSyncNeverReplacesOrRemovesASymlink(t *testing.T) {
	f := newSyncFixture(t)
	content := f.content(installOne)
	f.writeProfile("target", content)
	require.NoError(t, os.Symlink(f.store.ProfilePath("target"), f.store.ProfilePath(nameOne)))
	f.writeLedger(managedEntry(entryOwned, nameOne, installOne, rawEntry{"fingerprint": fingerprint(content)}))

	// First with the installation still granted, then with it gone.
	moved := installation(installOne, "prod", stateActive)
	moved.Endpoint = testOtherOrigin
	f.answer(moved)
	updated := f.sync()

	require.NoError(t, updated.Fatal)
	assert.Equal(t, content, f.read("target"), "a symlink's target is never written through")
	assert.NotEmpty(t, warningsContaining(updated, nameOne))

	f.answer()
	pruned := f.sync()

	require.NoError(t, pruned.Fatal)
	assert.Zero(t, pruned.Pruned)
	_, err := os.Lstat(f.store.ProfilePath(nameOne))
	assert.NoError(t, err, "the symlink itself is never removed")
	assert.True(t, f.exists("target"))
}

func TestSyncSkipsBothSidesOfANameCollisionAndKeepsItsAuthority(t *testing.T) {
	f := newSyncFixture(t)
	// The two ids share the twelve hex characters a derived name carries, and
	// everything else about them is equal, so both derive one name.
	const shared = "acme-default-prod-3f2b8c140000"
	theirs := []byte("a profile the user wrote\n")
	f.writeProfile(shared, theirs)
	f.answer(
		installation(testUUIDA, "prod", stateActive),
		installation(testUUIDB, "prod", stateActive),
		installation(installTwo, "staging", stateActive),
	)
	require.NoError(t, f.sync().Fatal)
	f.answer(
		installation(testUUIDA, "prod", stateActive),
		installation(testUUIDB, "prod", stateActive),
		installation(installTwo, "staging", "destroyed"),
	)

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, theirs, f.read(shared), "neither side of a collision is written")
	assert.Nil(t, f.entryFor(testUUIDA))
	assert.Nil(t, f.entryFor(testUUIDB))
	assert.Equal(t, 1, result.Pruned, "a collision leaves the run authoritative")
	assert.False(t, f.exists(nameTwo))
	warnings := warningsContaining(result, shared)
	require.Len(t, warnings, 1, "one warning per colliding name")
	assert.Contains(t, warnings[0], testUUIDA)
	assert.Contains(t, warnings[0], testUUIDB)
}

func TestSyncSerialisesWithAnotherProcess(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	before := f.entries()

	held, err := lockLedger(f.store.ManagedLockPath())
	require.NoError(t, err)
	defer func() { _ = held.Unlock() }()
	f.answer()
	f.client.calls = 0

	result := f.sync()

	require.Error(t, result.Fatal)
	assert.ErrorIs(t, result.Fatal, errLedgerLocked)
	assert.Zero(t, f.client.calls, "a run that cannot take the lock asks nothing and changes nothing")
	assert.True(t, f.exists(nameOne))
	assert.Equal(t, before, f.entries(), "neither run loses an entry")
}

func TestSyncTakesTheLockBeforeEnumerating(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	var lockedDuringEnumeration bool
	f.client.onCall = func() {
		_, err := lockLedger(f.store.ManagedLockPath())
		lockedDuringEnumeration = errors.Is(err, errLedgerLocked)
	}

	require.NoError(t, f.sync().Fatal)

	assert.True(t, lockedDuringEnumeration,
		"the lock spans the network call, so a run that enumerated a fresher set cannot be undone by an older one")
}

func TestSyncInterruptedAfterPublishingConvergesOnTheNextRun(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	// The intent is written, the file is published, and the run stops before
	// it can record the result.
	restore := failSaveAfter(t, 1)

	interrupted := f.sync()

	require.Error(t, interrupted.Fatal)
	require.Equal(t, f.content(installOne), f.read(nameOne), "the destination is never half-written")
	entry := f.entryFor(installOne)
	require.NotNil(t, entry)
	assert.Equal(t, entryPending, entry.State)
	require.Len(t, f.tempFiles(), 1)

	restore()
	result := f.sync()

	require.NoError(t, result.Fatal)
	settled := f.entryFor(installOne)
	require.NotNil(t, settled)
	assert.Equal(t, entryOwned, settled.State)
	assert.Equal(t, fingerprint(f.content(installOne)), settled.Fingerprint)
	assert.Equal(t, f.content(installOne), f.read(nameOne))
	assert.Empty(t, f.tempFiles())
}

func TestSyncInterruptedOnTheFallbackPathAbandonsRatherThanAdopts(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	// Hard links are unavailable, so the publication writes a second file and
	// leaves no witness behind; the run then stops before recording it.
	refuseLinks(t, syscall.EPERM)
	restore := failSaveAfter(t, 1)

	require.Error(t, f.sync().Fatal)
	require.Equal(t, f.content(installOne), f.read(nameOne))

	restore()
	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Nil(t, f.entryFor(installOne), "no witness means no promotion, whatever the bytes say")
	assert.Equal(t, f.content(installOne), f.read(nameOne), "and the file is left where it is")
	warnings := warningsContaining(result, nameOne)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0], "by hand")
	assert.Zero(t, result.Created, "the name stays wedged until the user removes that file")
}

func TestSyncStopsWhenTheLedgerCannotBeWritten(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	failSaveAfter(t, 0)

	result := f.sync()

	require.Error(t, result.Fatal)
	assert.False(t, f.exists(nameOne), "nothing is published without a record of the intent to publish it")
	assert.False(t, f.exists(nameTwo))
	assert.Empty(t, f.tempFiles())
}

func TestSyncCountsOnlyTheDesiredSetItSatisfied(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, f.sync().Fatal)
	// The profile for the one installation still granted is now the user's,
	// and a stale profile this formae owns survives for an unrelated reason.
	f.answer(installation(installTwo, "staging", "hibernating"))
	require.NoError(t, f.sync().Fatal)
	theirs := []byte("a profile the user wrote\n")
	f.writeProfile(nameTwo, theirs)
	f.answer(installation(installTwo, "staging", stateActive), installation(installOne, "prod", stateActive))

	result := f.sync()

	require.NoError(t, result.Fatal)
	assert.Equal(t, 2, result.DesiredCount)
	assert.Equal(t, 1, result.DesiredSatisfied, "a name formae does not own satisfies nothing")
	assert.True(t, result.StaleManagedForOrigin)
}

func TestPruneAllKeepsTheActiveManagedProfile(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, f.store.Use(nameOne))

	result := pruneAll(f.deps(), testOrigin)

	require.NoError(t, result.Fatal)
	assert.Equal(t, 1, result.Pruned)
	assert.True(t, f.exists(nameOne), "the active profile leaves an auth block to sign back in with")
	require.NotNil(t, f.entryFor(installOne), "and its entry, so a later login removes it")
	assert.False(t, f.exists(nameTwo))
	assert.True(t, result.StaleManagedForOrigin)
}

func TestSyncWarnsAboutAuthKeysAGeneratedProfileDoesNotCarry(t *testing.T) {
	f := newSyncFixture(t)
	raw := oidcAuth(t, map[string]any{"audience": "https://api.example"})
	f.answer(installation(installOne, "prod", stateActive))

	result := syncProfiles(context.Background(), f.deps(), f.platform(), "Bearer "+testToken, cliAuth("", ""), raw)

	require.NoError(t, result.Fatal)
	warnings := warningsContaining(result, "audience")
	require.Len(t, warnings, 1, "one warning per run, not one per profile")
	assert.NotContains(t, warnings[0], "https://api.example", "a value may belong to another system")

	// With nothing to write, there is nothing to warn about.
	f.answer()
	quiet := syncProfiles(context.Background(), f.deps(), f.platform(), "Bearer "+testToken, cliAuth("", ""), raw)

	require.NoError(t, quiet.Fatal)
	assert.Empty(t, warningsContaining(quiet, "audience"))
}

func TestPruneNeverRemovesAFileWhoseContentsChanged(t *testing.T) {
	tests := []struct {
		name string
		run  func(f *syncFixture) syncResult
	}{
		{
			name: "the grant is gone",
			run: func(f *syncFixture) syncResult {
				f.answer()
				return f.sync()
			},
		},
		{
			name: "the user signed out",
			run:  func(f *syncFixture) syncResult { return pruneAll(f.deps(), testOrigin) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			f.answer(installation(installOne, "prod", stateActive))
			require.NoError(t, f.sync().Fatal)
			// The edit and the vanished grant land in the same run, so the
			// fingerprint is the only thing standing between the user's file
			// and a deletion.
			edited := append(f.content(installOne), []byte("\n// my own note\n")...)
			f.writeProfile(nameOne, edited)

			result := tt.run(f)

			require.NoError(t, result.Fatal)
			assert.Zero(t, result.Pruned)
			assert.Equal(t, edited, f.read(nameOne), "a file whose bytes are not the ones formae wrote is not ours")
			assert.Nil(t, f.entryFor(installOne))
			warnings := warningsContaining(result, nameOne)
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], "formae profile delete "+nameOne)
		})
	}
}
