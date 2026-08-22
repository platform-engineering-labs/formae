// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
)

// A sign-in that publishes a profile onto a store with no active pointer has to
// point the pointer at it. Nothing else does: the sync reads the active profile
// only to protect it from rename and prune, so before this a hosted user
// finished sign-in with a profile on disk, no pointer, and the next command
// bootstrapping a classic default beside it.
//
// The rule (2026-08-22): a sign-in that publishes profiles points the pointer at
// what it published — signing in IS the request to use what you signed in to.
// Before this, a hosted sign-in on a machine with a classic active profile left
// that profile active, and the next connect resolved the classic profile and
// refused. The pointer stays put only when it already names a published profile,
// and a switch always says what it moved from.

// cleanStoreFixture is a sync fixture whose store has no profiles and no active
// pointer, which is the state a machine that has never run formae is in.
// newSyncFixture deliberately seeds a default profile and points active at it,
// so it cannot express this.
func cleanStoreFixture(t *testing.T) *syncFixture {
	t.Helper()
	root := t.TempDir()
	return &syncFixture{
		t:        t,
		root:     root,
		store:    store.New(root),
		client:   &stubCloudClient{},
		verifier: &stubVerifier{},
		out:      &bytes.Buffer{},
	}
}

// cloudStep is a cloud sign-in against the fixture, with no profile behind it.
func cloudStep(t *testing.T, f *syncFixture) syncStep {
	t.Helper()
	_, block, err := cloudAuthBlock(testIssuer)
	require.NoError(t, err)

	step := f.loginStep()
	step.Entry = syncFromFlags(block)
	return step
}

func TestActive_APublishedProfileBecomesActiveOnACleanStore(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

	name := cloudProfileName()
	assert.FileExists(t, f.store.ProfilePath(name))

	active, err := f.store.Active()
	require.NoError(t, err)
	assert.Equal(t, name, active)
}

// The classic default is what this whole path exists to avoid, so it is asserted
// directly rather than inferred from the pointer naming something else.
func TestActive_NoClassicDefaultIsCreated(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

	assert.NoFileExists(t, f.store.ProfilePath("default"))
}

// A sign-in moves the pointer onto what it published, naming what it moved
// from: signing in is the request to use what you signed in to. The profile
// the pointer left behind is untouched on disk.
func TestActive_TheSignedInProfileBecomesActive(t *testing.T) {
	f := cleanStoreFixture(t)
	require.NoError(t, os.MkdirAll(f.store.ProfilesDir(), 0o755))
	require.NoError(t, os.WriteFile(f.store.ProfilePath("mine"), []byte(store.StubTemplate), 0o644))
	require.NoError(t, f.store.Use("mine"))

	f.answer(installation(installOne, "prod", stateActive))
	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

	active, err := f.store.Active()
	require.NoError(t, err)
	assert.Equal(t, cloudProfileName(), active)
	assert.Contains(t, f.out.String(), "was mine", "the switch names what it moved from")
	assert.FileExists(t, f.store.ProfilePath("mine"), "the previous profile is untouched on disk")
}

// A pointer already naming a published profile stays put: re-signing in to what
// you already use is a no-op, not a switch to the run's first published profile.
func TestActive_AnActivePublishedProfileStaysActive(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", stateActive),
	)
	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

	// Point at the other published profile, then sign in again.
	published, err := f.store.Active()
	require.NoError(t, err)
	other := nameTwo
	if published == other {
		other = nameOne
	}
	require.NoError(t, f.store.Use(other))

	f.answer(
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", stateActive),
	)
	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

	active, err := f.store.Active()
	require.NoError(t, err)
	assert.Equal(t, other, active, "an active published profile is never yanked to the run's first")
}

// A run that published nothing writes no pointer. There is nothing to point at,
// and inventing a target would name a profile that does not exist.
func TestActive_NothingPublishedWritesNoPointer(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer() // the grants cover no installations.

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

	_, err := f.store.Active()
	require.Error(t, err, "a pointer was written with nothing to point at")
	assert.NoFileExists(t, filepath.Join(f.root, "active"))
}

// Two installations resolve deterministically: the pointer names the first in the
// run's own order, the same one on every repeat. At this stage of the journey the
// grants cover zero or one installation, so the tie is not reachable today —
// ordering it makes the outcome a decision rather than a map-iteration accident.
func TestActive_TwoInstallationsResolveDeterministically(t *testing.T) {
	first := ""
	for range 5 {
		f := cleanStoreFixture(t)
		f.answer(
			installation(installOne, "prod", stateActive),
			installation(installTwo, "staging", stateActive),
		)
		require.NoError(t, runLoginAndSync(context.Background(), signedIn(), cloudStep(t, f), false))

		active, err := f.store.Active()
		require.NoError(t, err)
		if first == "" {
			first = active
		}
		assert.Equal(t, first, active, "the active profile is not stable across runs")
	}
}

// A profile sync through a hosted profile also benefits: a user whose pointer is
// missing for any reason gets one, and the rule does not care how the sign-in was
// entered. What it cares about is that no pointer exists.
func TestActive_AppliesToAProfileSignInToo(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	step := f.loginStep() // the hosted-profile entry, not the cloud one.
	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	active, err := f.store.Active()
	require.NoError(t, err)
	assert.Equal(t, cloudProfileName(), active)
}
