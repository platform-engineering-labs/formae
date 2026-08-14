// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	// installThree and nameThree stand for a profile derived from a second
	// control plane, so a test can prove one origin's sign-out leaves the
	// other's profiles alone.
	installThree = "33333333-3333-4333-8333-333333333333"
	nameThree    = "acme-default-dev-333333333333"
)

// signedOut is the ordinary successful sign-out.
func signedOut() *stubAuthClient {
	return &stubAuthClient{logoutResp: &pkgauth.LogoutResponse{}}
}

// hostedAt returns a hosted connection addressing one installation, which is
// what a profile `formae login` derived resolves to.
func hostedAt(t *testing.T, installation string) *pkgmodel.HostedConnection {
	t.Helper()
	conn := hosted(oidcAuth(t, nil))
	conn.Installation = installation
	return conn
}

// logoutStep returns the prune half of a logout, pointed at the fixture's
// config directory, on a hosted profile addressing one installation. It
// carries no control-plane flag and no client: a logout asks nobody anything
// and takes its origin from the ledger.
func (f *syncFixture) logoutStep(installation string) pruneStep {
	return pruneStep{
		Conn:      hostedAt(f.t, installation),
		ConfigDir: func() (string, error) { return f.root, nil },
		Out:       f.out,
		Theme:     testTheme,
	}
}

// derive publishes a profile the way a completed login leaves one — the bytes
// a generated profile carries, at its derived name — and returns the ledger
// entry recording it against origin.
func (f *syncFixture) derive(origin, name, id string) rawEntry {
	f.t.Helper()
	content := f.content(id)
	f.writeProfile(name, content)
	return managedEntry(entryOwned, name, id, rawEntry{
		"controlPlane": origin,
		"fingerprint":  fingerprint(content),
	})
}

// TestLogoutRemovesTheProfilesLoginDerived is the ordinary case: the user
// signs out of a profile login wrote, and the profiles derived alongside it
// go with the tokens.
func TestLogoutRemovesTheProfilesLoginDerived(t *testing.T) {
	f := newSyncFixture(t)
	theirs := []byte("// mine\n")
	f.writeProfile("handwritten", theirs)
	f.writeLedger(
		f.derive(testOrigin, nameOne, installOne),
		f.derive(testOrigin, nameTwo, installTwo),
	)
	require.NoError(t, f.store.Use(nameOne))

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

	assert.Equal(t, []string{
		"✓ signed out",
		"✓ removed profile " + nameTwo,
	}, outputMarked(f, "✓ "))
	assert.False(t, f.exists(nameTwo))
	assert.True(t, f.exists(nameOne), "the active profile leaves an auth block to sign back in with")
	assert.Equal(t, theirs, f.read("handwritten"), "a profile formae did not write is never removed")

	kept := outputMarked(f, "! ")
	require.Len(t, kept, 1)
	assert.Contains(t, kept[0], nameOne, "the profile that survives is the one still in use, and says so")
	assert.NotContains(t, kept[0], "is gone",
		"a sign-out revokes nothing upstream, so nothing about the user's access has changed")
	assert.Contains(t, kept[0], "formae profile delete "+nameOne,
		"the remedy is one that removes this profile, rather than a sign-in that derives them all again")

	// The remedy exactly as the message writes it, so the advice is a route the
	// user can actually take.
	require.NoError(t, f.store.Use("default"))
	require.NoError(t, f.store.Delete(nameOne))
	assert.False(t, f.exists(nameOne))
}

// TestLogoutRemovesProfilesForTheOriginTheLedgerRecords pins the rule a
// destructive command lives or dies by: the origin comes from the entry bound
// to the active profile, never from the environment. FORMAE_CLOUD_URL names a
// different control plane throughout, and it neither redirects the removal nor
// gets so much as a request.
func TestLogoutRemovesProfilesForTheOriginTheLedgerRecords(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("FORMAE_CLOUD_URL", srv.URL)
	t.Setenv("FORMAE_CLOUD_ISSUER", srv.URL)

	f := newSyncFixture(t)
	f.writeLedger(
		f.derive(testOtherOrigin, nameOne, installOne),
		f.derive(testOtherOrigin, nameTwo, installTwo),
		f.derive(testOrigin, nameThree, installThree),
	)
	require.NoError(t, f.store.Use(nameOne))

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

	assert.False(t, f.exists(nameTwo), "the origin the active profile is recorded against is pruned")
	assert.True(t, f.exists(nameThree), "another control plane's profiles are not this sign-out's business")
	assert.True(t, f.exists(nameOne))
	assert.Zero(t, requests.Load(), "signing out asks no control plane anything")

	remaining := []string{}
	for _, e := range f.entries() {
		remaining = append(remaining, e.Name)
	}
	assert.Equal(t, []string{nameOne, nameThree}, remaining)
}

// TestLogoutRemovesNothingWithoutAnEntryBoundToTheActiveProfile covers each
// way the binding can fail to hold. A profile file carries no control-plane
// origin, so without an entry that resolves to this very file, for this
// installation, holding the bytes formae wrote, there is no fact on disk
// saying which environment the user signed out of.
func TestLogoutRemovesNothingWithoutAnEntryBoundToTheActiveProfile(t *testing.T) {
	tests := []struct {
		name  string
		setUp func(f *syncFixture)
	}{
		{
			name: "the active profile is not one formae wrote",
			setUp: func(f *syncFixture) {
				f.writeLedger(f.derive(testOrigin, nameTwo, installTwo))
				f.writeProfile(nameOne, []byte("// my own hosted profile\n"))
			},
		},
		{
			name: "the entry for this installation names another file",
			setUp: func(f *syncFixture) {
				// A copy of a derived profile, made the active one: it holds
				// the bytes formae wrote and addresses the same installation,
				// and it is still not the file the entry names.
				f.writeLedger(
					f.derive(testOrigin, nameThree, installOne),
					f.derive(testOrigin, nameTwo, installTwo),
				)
				f.writeProfile(nameOne, f.content(installOne))
			},
		},
		{
			name: "the entry naming this file is for another installation",
			setUp: func(f *syncFixture) {
				entry := f.derive(testOrigin, nameOne, installOne)
				entry["installationId"] = installThree
				f.writeLedger(entry, f.derive(testOrigin, nameTwo, installTwo))
			},
		},
		{
			name: "the file no longer holds the bytes formae wrote",
			setUp: func(f *syncFixture) {
				f.writeLedger(
					f.derive(testOrigin, nameOne, installOne),
					f.derive(testOrigin, nameTwo, installTwo),
				)
				f.writeProfile(nameOne, append(f.content(installOne), []byte("// my note\n")...))
			},
		},
		{
			name: "the entry that would bind is quarantined",
			setUp: func(f *syncFixture) {
				bound := f.derive(testOrigin, nameOne, installOne)
				// A second entry claiming the same name ties both into a
				// conflicting set, which authorises nothing while it stands.
				twin := managedEntry(entryOwned, nameOne, installThree, rawEntry{
					"controlPlane": testOrigin,
					"fingerprint":  fingerprint(f.content(installOne)),
				})
				f.writeLedger(bound, twin, f.derive(testOrigin, nameTwo, installTwo))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			tt.setUp(f)
			require.NoError(t, f.store.Use(nameOne))

			require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

			assert.True(t, f.exists(nameOne))
			assert.True(t, f.exists(nameTwo), "no entry binds, so nothing licenses a removal")
			assert.Contains(t, f.out.String(), "· no profiles were removed: the active profile is not one formae derived",
				"the user is told why the profiles are still there, as a no-op rather than a warning")
		})
	}
}

// TestLogoutRemovesNothingWhenTwoControlPlanesRecordTheActiveProfile covers
// the reachable ambiguity: entries are unique per origin, but two origins may
// record the same name, and nothing on disk says which one the user just
// signed out of. Guessing would prune a whole environment on a coin flip.
func TestLogoutRemovesNothingWhenTwoControlPlanesRecordTheActiveProfile(t *testing.T) {
	f := newSyncFixture(t)
	staging := f.derive(testOtherOrigin, nameOne, installOne)
	f.writeLedger(
		f.derive(testOrigin, nameOne, installOne),
		staging,
		f.derive(testOrigin, nameTwo, installTwo),
		f.derive(testOtherOrigin, nameThree, installThree),
	)
	require.NoError(t, f.store.Use(nameOne))

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

	assert.True(t, f.exists(nameTwo))
	assert.True(t, f.exists(nameThree))
	assert.Len(t, f.entries(), 4, "an ambiguous ledger is carried forward untouched")

	warnings := outputMarked(f, "! ")
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], nameOne)
	assert.Contains(t, warnings[0], testOrigin)
	assert.Contains(t, warnings[0], testOtherOrigin)
}

// TestLogoutSaysNothingAboutProfilesWhenThereWereNeverAny verifies that a
// sign-out on a hosted profile formae never derived anything for is exactly a
// sign-out: a user who has just signed out is not told about profiles when
// there was never anything to remove.
func TestLogoutSaysNothingAboutProfilesWhenThereWereNeverAny(t *testing.T) {
	f := newSyncFixture(t)
	f.writeProfile(nameOne, []byte("// my own hosted profile\n"))
	require.NoError(t, f.store.Use(nameOne))

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

	assert.Equal(t, "✓ signed out\n", f.out.String())
}

// TestLogoutOnAClassicProfileIsUnchanged pins that a classic profile's logout
// is the sign-out and nothing else: no ledger is read, no config directory is
// resolved, and the output is what it was before profiles were derived at all.
func TestLogoutOnAClassicProfileIsUnchanged(t *testing.T) {
	f := newSyncFixture(t)
	f.writeLedger(f.derive(testOrigin, nameOne, installOne))

	step := f.logoutStep(installOne)
	step.Conn = &pkgmodel.ClassicConnection{URL: "https://agent.example", Port: 443, Auth: oidcAuth(t, nil)}
	step.ConfigDir = func() (string, error) {
		t.Fatal("a classic profile's logout resolves no config directory")
		return "", nil
	}

	require.NoError(t, runLogoutAndPrune(signedOut(), step))

	assert.Equal(t, "✓ signed out\n", f.out.String())
	assert.True(t, f.exists(nameOne))
}

// TestLogoutDropsTheTokensBeforeTheProfiles pins the order. The auth plugin's
// config comes from the active profile, so removing profiles first could
// remove the block the sign-out needs to be made with.
func TestLogoutDropsTheTokensBeforeTheProfiles(t *testing.T) {
	f := newSyncFixture(t)
	f.writeLedger(
		f.derive(testOrigin, nameOne, installOne),
		f.derive(testOrigin, nameTwo, installTwo),
	)
	require.NoError(t, f.store.Use(nameOne))

	c := signedOut()
	var profilesAtSignOut bool
	c.onLogout = func() { profilesAtSignOut = f.exists(nameTwo) }

	require.NoError(t, runLogoutAndPrune(c, f.logoutStep(installOne)))

	assert.True(t, profilesAtSignOut, "the tokens are dropped while the profiles are still there")
	assert.False(t, f.exists(nameTwo))
}

// TestLogoutRemovesNothingWhenTheSignOutFails pins the other half of the
// order: a plugin that reports a failure has not established that the user is
// signed out, and deleting profiles while the credential may still work is
// the worse of the two errors.
func TestLogoutRemovesNothingWhenTheSignOutFails(t *testing.T) {
	tests := []struct {
		name string
		c    *stubAuthClient
	}{
		{
			name: "the plugin call failed",
			c:    &stubAuthClient{logoutErr: assert.AnError},
		},
		{
			name: "the plugin reported an error",
			c: &stubAuthClient{logoutResp: &pkgauth.LogoutResponse{
				ErrorCode: pkgauth.ErrorCodeUnsupported,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			f.writeLedger(
				f.derive(testOrigin, nameOne, installOne),
				f.derive(testOrigin, nameTwo, installTwo),
			)
			require.NoError(t, f.store.Use(nameOne))

			require.Error(t, runLogoutAndPrune(tt.c, f.logoutStep(installOne)))

			assert.True(t, f.exists(nameOne))
			assert.True(t, f.exists(nameTwo), "we do not know we are signed out, so nothing is removed")
			assert.Len(t, f.entries(), 2)
		})
	}
}

// TestLogoutLeavesAProfileToSignBackInWith is the round trip the design turns
// on: the active profile survives the sign-out, and the next login re-derives
// every profile that went with it.
func TestLogoutLeavesAProfileToSignBackInWith(t *testing.T) {
	f := newSyncFixture(t)
	f.answer(installation(installOne, "prod", stateActive), installation(installTwo, "staging", stateActive))
	require.NoError(t, f.sync().Fatal)
	require.NoError(t, f.store.Use(nameOne))
	f.out.Reset()

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))
	require.True(t, f.exists(nameOne))
	require.False(t, f.exists(nameTwo))
	f.out.Reset()

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), f.loginStep(), false))

	assert.True(t, f.exists(nameTwo), "signing back in re-derives what the sign-out removed")
	assert.Equal(t, f.content(installTwo), f.read(nameTwo))
	assert.True(t, f.exists(nameOne))
}

// TestLogoutFailsWhenAnotherProcessIsUpdatingTheProfiles verifies that a
// contended ledger is reported as the sign-out it was — the tokens are gone —
// with the profiles named as the part that did not happen.
func TestLogoutFailsWhenAnotherProcessIsUpdatingTheProfiles(t *testing.T) {
	f := newSyncFixture(t)
	f.writeLedger(
		f.derive(testOrigin, nameOne, installOne),
		f.derive(testOrigin, nameTwo, installTwo),
	)
	require.NoError(t, f.store.Use(nameOne))

	held, err := lockLedger(f.store.ManagedLockPath())
	require.NoError(t, err)
	defer func() { _ = held.Unlock() }()

	err = runLogoutAndPrune(signedOut(), f.logoutStep(installOne))

	require.Error(t, err)
	assert.ErrorIs(t, err, errLedgerLocked,
		"a caller telling a contended ledger from a real failure has the sentinel to test against")
	assert.Contains(t, err.Error(), "you are signed out")
	assert.Contains(t, err.Error(), "another formae process")
	assert.NotContains(t, err.Error(), f.store.ManagedLockPath(), "a lock path is not something the user can act on")
	assert.True(t, f.exists(nameTwo))
}

// TestLogoutFailsWhenTheConfigDirectoryCannotBeFound verifies the sync half is
// not run against a config directory that could not be resolved: the ledger
// and the profiles would otherwise be looked for relative to whatever
// directory the command was run in.
func TestLogoutFailsWhenTheConfigDirectoryCannotBeFound(t *testing.T) {
	f := newSyncFixture(t)
	step := f.logoutStep(installOne)
	step.ConfigDir = func() (string, error) { return "", assert.AnError }

	err := runLogoutAndPrune(signedOut(), step)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "you are signed out")
	assert.Equal(t, []string{"✓ signed out"}, lines(f.out))
}

// TestLogoutNoticesALedgerItCannotRead verifies that records belonging to a
// newer formae stop the removal and are reported, rather than being rewritten
// or ignored. Signing out still succeeded, so it is a notice and a zero exit.
func TestLogoutNoticesALedgerItCannotRead(t *testing.T) {
	f := newSyncFixture(t)
	f.writeLedger(f.derive(testOrigin, nameOne, installOne), f.derive(testOrigin, nameTwo, installTwo))
	require.NoError(t, f.store.Use(nameOne))
	raw, err := os.ReadFile(f.store.ManagedLedgerPath())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.store.ManagedLedgerPath(),
		[]byte(strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":2`, 1)), 0o600))

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

	assert.True(t, f.exists(nameTwo), "records this formae cannot read authorise nothing")
	assert.Contains(t, f.out.String(), "no profile was removed")
}

// TestLogoutShowsAWarningTheUserCanActOn verifies that what the ledger refused
// to believe reaches the terminal on the paths that remove nothing: a
// conflicting set is exactly why no profile was removed, and the message names
// the file to remove to reset it.
func TestLogoutShowsAWarningTheUserCanActOn(t *testing.T) {
	f := newSyncFixture(t)
	f.writeProfile(nameOne, []byte("// my own hosted profile\n"))
	f.writeLedger(
		f.derive(testOrigin, nameTwo, installTwo),
		f.derive(testOrigin, nameThree, installTwo),
	)
	require.NoError(t, f.store.Use(nameOne))

	require.NoError(t, runLogoutAndPrune(signedOut(), f.logoutStep(installOne)))

	warnings := outputMarked(f, "! ")
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], f.store.ManagedLedgerPath())
	assert.True(t, f.exists(nameTwo))
	assert.True(t, f.exists(nameThree))
}

// TestLogoutCmdTakesNoControlPlaneFlags pins the flag surface: a destructive
// command that took its target from a flag or an environment variable could be
// pointed at an environment the user never signed out of.
func TestLogoutCmdTakesNoControlPlaneFlags(t *testing.T) {
	cmd := LogoutCmd()

	assert.Nil(t, cmd.Flags().Lookup("cloud"))
	assert.Nil(t, cmd.Flags().Lookup("cloud-issuer"))
}

// outputMarked returns the output lines carrying a marker.
func outputMarked(f *syncFixture, marker string) []string {
	f.t.Helper()
	var found []string
	for _, line := range lines(f.out) {
		if strings.HasPrefix(line, marker) {
			found = append(found, line)
		}
	}
	return found
}
