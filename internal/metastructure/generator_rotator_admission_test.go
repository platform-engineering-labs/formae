// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func rotationTestDatastore(t *testing.T) datastore.Datastore {
	t.Helper()
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}, "test")
	require.NoError(t, err)
	return ds
}

// createRotatingGenerator persists a generator declaring a cadence and returns
// the rotation info a sweep would have read for it.
func createRotatingGenerator(t *testing.T, ds datastore.Datastore, stackLabel, label string, everySeconds int) datastore.GeneratorRotationInfo {
	t.Helper()
	stack := &pkgmodel.Stack{Label: stackLabel}
	_, err := ds.CreateStack(stack, "cmd-stack")
	require.NoError(t, err)

	_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{
		Label: label, Stack: stackLabel, StackID: stack.ID,
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		Rotation: &pkgmodel.RotationSpec{EverySeconds: everySeconds},
	}, "cmd-generator")
	require.NoError(t, err)

	infos, err := ds.GetGeneratorsWithRotation()
	require.NoError(t, err)
	for _, info := range infos {
		if info.Label == label && info.StackLabel == stackLabel {
			return info
		}
	}
	t.Fatalf("no rotation info for generator %q on stack %q", label, stackLabel)
	return datastore.GeneratorRotationInfo{}
}

// A generator the sweep read and a user then deleted is caught at admission.
// The sweep's view is a snapshot; planning against it would draw a value for a
// generator that no longer exists and try to advance a tombstoned row.
func TestAdmitRotation_RefusesAGeneratorDeletedAfterTheSweep(t *testing.T) {
	ds := rotationTestDatastore(t)
	info := createRotatingGenerator(t, ds, "secrets", "db-password", 3600)

	admitted, err := admitRotation(ds, info)
	require.NoError(t, err)
	require.NotNil(t, admitted, "precondition: the generator is admitted while it exists")

	_, err = ds.DeleteGenerator("db-password", "secrets")
	require.NoError(t, err)

	admitted, err = admitRotation(ds, info)
	require.NoError(t, err)
	assert.Nil(t, admitted, "a generator deleted after the sweep must not be rotated")
}

// A generator whose cadence a user removed after the sweep is caught at
// admission: nothing rotates a credential unless its current declaration says
// how often to.
func TestAdmitRotation_RefusesAGeneratorWhoseCadenceWasRemoved(t *testing.T) {
	ds := rotationTestDatastore(t)
	info := createRotatingGenerator(t, ds, "secrets", "db-password", 3600)

	stack, err := ds.GetStackByLabel("secrets")
	require.NoError(t, err)
	_, err = ds.UpdateGenerator(&pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "secrets", StackID: stack.ID,
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	}, "cmd-user-edit")
	require.NoError(t, err)

	admitted, err := admitRotation(ds, info)
	require.NoError(t, err)
	assert.Nil(t, admitted, "a generator whose cadence was removed must not be rotated")
}

// A generator whose cadence a user widened after the sweep is caught at
// admission: it was due on the interval the sweep read, and is not due on the
// interval that is now declared.
func TestAdmitRotation_RefusesAGeneratorWhoseCadenceWasWidened(t *testing.T) {
	ds := rotationTestDatastore(t)
	info := createRotatingGenerator(t, ds, "secrets", "db-password", 60)
	// The sweep saw a one-minute cadence last satisfied an hour ago, so it is
	// due.
	info.LastRotationAt = time.Now().UTC().Add(-time.Hour)
	require.True(t, rotationIsDue(info, time.Now().UTC()), "precondition: due on the cadence the sweep read")

	stack, err := ds.GetStackByLabel("secrets")
	require.NoError(t, err)
	_, err = ds.UpdateGenerator(&pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "secrets", StackID: stack.ID,
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		Rotation: &pkgmodel.RotationSpec{EverySeconds: 30 * 24 * 3600},
	}, "cmd-user-edit")
	require.NoError(t, err)

	admitted, err := admitRotation(ds, info)
	require.NoError(t, err)
	assert.Nil(t, admitted, "a generator whose cadence was widened past its due date must not be rotated")
}

// The value is drawn under the spec that is declared NOW, not the one the
// sweep read: a user apply that edits the generator between the two must not
// have a value drawn under the spec it replaced.
func TestAdmitRotation_DrawsUnderTheCurrentSpec(t *testing.T) {
	ds := rotationTestDatastore(t)
	info := createRotatingGenerator(t, ds, "secrets", "db-password", 3600)

	stack, err := ds.GetStackByLabel("secrets")
	require.NoError(t, err)
	_, err = ds.UpdateGenerator(&pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "secrets", StackID: stack.ID,
		Length: 48, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		Rotation: &pkgmodel.RotationSpec{EverySeconds: 3600},
	}, "cmd-user-edit")
	require.NoError(t, err)

	admitted, err := admitRotation(ds, info)
	require.NoError(t, err)
	require.NotNil(t, admitted)

	password, ok := admitted.(*pkgmodel.PasswordGenerator)
	require.True(t, ok)
	assert.Equal(t, 48, password.Length, "the draw must run under the edited spec")
	assert.Equal(t, info.GeneratorID, admitted.GetID(),
		"the generator must carry the identity the generation is recorded against")
	assert.Equal(t, "secrets", admitted.GetStack(),
		"the generator must carry the stack the draw op is filed under")
}

// A label reused by a different generator is not the generator the sweep read.
// Identity is the row's KSUID, so a delete followed by a create under the same
// label must not be rotated on the deleted generator's schedule.
func TestAdmitRotation_RefusesAReusedLabel(t *testing.T) {
	ds := rotationTestDatastore(t)
	info := createRotatingGenerator(t, ds, "secrets", "db-password", 3600)

	_, err := ds.DeleteGenerator("db-password", "secrets")
	require.NoError(t, err)

	stack, err := ds.GetStackByLabel("secrets")
	require.NoError(t, err)
	_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "secrets", StackID: stack.ID,
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		Rotation: &pkgmodel.RotationSpec{EverySeconds: 3600},
	}, "cmd-recreate")
	require.NoError(t, err)

	replacement, err := ds.GetGeneratorIdentity("db-password", "secrets")
	require.NoError(t, err)
	require.NotEqual(t, info.GeneratorID, replacement.ID, "precondition: the replacement is a different row")

	admitted, err := admitRotation(ds, info)
	require.NoError(t, err)
	assert.Nil(t, admitted, "a label now held by a different generator must not be rotated")
}

// A generator nothing binds has no credential in place, so there is nothing to
// rotate and no command to submit. Drawing anyway would advance the generation
// and write the value nowhere.
func TestPrepareRotation_SkipsAGeneratorWithNoDestination(t *testing.T) {
	ds := rotationTestDatastore(t)
	info := createRotatingGenerator(t, ds, "secrets", "db-password", 3600)

	result, err := prepareRotation(ds, info)
	require.NoError(t, err)
	assert.Nil(t, result, "a generator with no destination must produce no command")
}
