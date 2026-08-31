// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func lookupTestDatastore(t *testing.T) datastore.Datastore {
	t.Helper()
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}, "test")
	require.NoError(t, err)
	return ds
}

// createGeneratorOn persists a generator on a freshly created stack and
// returns its KSUID.
func createGeneratorOn(t *testing.T, ds datastore.Datastore, stackLabel, generatorLabel string, length int) string {
	t.Helper()
	stack := &pkgmodel.Stack{Label: stackLabel}
	_, err := ds.CreateStack(stack, "cmd-stack")
	require.NoError(t, err)
	require.NotEmpty(t, stack.ID)

	_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{
		Label: generatorLabel, Stack: stackLabel, StackID: stack.ID,
		Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	}, "cmd-generator")
	require.NoError(t, err)

	identity, err := ds.GetGeneratorIdentity(generatorLabel, stackLabel)
	require.NoError(t, err)
	require.NotEmpty(t, identity.ID)
	return identity.ID
}

// A generator that has drawn is reachable from its KSUID alone, whatever
// stack it lives on. GeneratorIdentity carries no label and no stack, but its
// GenerationSpec is the serialized generator, which carries both — so a
// destination on one stack bound to a generator on another still resolves
// without enumerating anything.
func TestGeneratorLookupForResume_ResolvesADrawnGeneratorOnAnotherStack(t *testing.T) {
	ds := lookupTestDatastore(t)
	ksuid := createGeneratorOn(t, ds, "secrets", "db-password", 24)

	require.NoError(t, ds.AdvanceGeneration(ksuid, "generation-1",
		json.RawMessage(`{"Type":"password","Label":"db-password","Stack":"secrets","Length":24}`)))

	lookup := generatorLookup(ds)
	require.NotNil(t, lookup)

	generator, err := lookup(ksuid)
	require.NoError(t, err)
	require.NotNil(t, generator, "a drawn generator must be reachable by KSUID from any stack")
	assert.Equal(t, "db-password", generator.GetLabel())
	assert.Equal(t, "secrets", generator.GetStack())
}

// The spec a draw runs under is the generator as it stands now, not the spec
// the last generation happened to be drawn under: an edited generator must
// produce a value at its edited length.
func TestGeneratorLookupForResume_ReturnsTheCurrentSpecNotTheGenerationsSpec(t *testing.T) {
	ds := lookupTestDatastore(t)
	ksuid := createGeneratorOn(t, ds, "secrets", "db-password", 24)

	require.NoError(t, ds.AdvanceGeneration(ksuid, "generation-1",
		json.RawMessage(`{"Type":"password","Label":"db-password","Stack":"secrets","Length":24}`)))
	secrets, err := ds.GetStackByLabel("secrets")
	require.NoError(t, err)
	require.NotNil(t, secrets)
	_, err = ds.UpdateGenerator(&pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "secrets", StackID: secrets.ID, Length: 40,
		Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	}, "cmd-edit")
	require.NoError(t, err)

	lookup := generatorLookup(ds)
	generator, err := lookup(ksuid)
	require.NoError(t, err)
	require.NotNil(t, generator)

	spec, ok := generator.(*pkgmodel.PasswordGenerator)
	require.True(t, ok)
	assert.Equal(t, 40, spec.Length, "the draw must run under the spec the row holds now")
}

// A generator that has never drawn carries no generation spec, so the KSUID
// route cannot reach it. The fallback finds it by walking the inventory,
// which is the case a resume actually hits: an interrupted first apply.
func TestGeneratorLookupForResume_FindsANeverDrawnGeneratorOnItsOwnStack(t *testing.T) {
	ds := lookupTestDatastore(t)
	ksuid := createGeneratorOn(t, ds, "app", "db-password", 24)

	identity, err := ds.GetGeneratorIdentityByID(ksuid)
	require.NoError(t, err)
	require.Empty(t, identity.GenerationID, "precondition: nothing has been drawn")

	lookup := generatorLookup(ds)
	generator, err := lookup(ksuid)
	require.NoError(t, err)
	require.NotNil(t, generator, "a never-drawn generator must still be found")
	assert.Equal(t, "db-password", generator.GetLabel())
	assert.Equal(t, "app", generator.GetStack())
}

// A generator belongs to one stack and is meant to be referenced from others,
// so a never-drawn generator is reached whatever stack it sits on relative to
// the destination that names it, and it comes back carrying the stack the
// draw op is filed under.
func TestGeneratorLookupForResume_FindsANeverDrawnGeneratorOnAnotherStack(t *testing.T) {
	ds := lookupTestDatastore(t)
	ksuid := createGeneratorOn(t, ds, "secrets", "db-password", 24)
	_, err := ds.CreateStack(&pkgmodel.Stack{Label: "app"}, "cmd-stack")
	require.NoError(t, err)

	lookup := generatorLookup(ds)
	generator, err := lookup(ksuid)
	require.NoError(t, err)
	require.NotNil(t, generator, "a never-drawn generator on another stack must be found by KSUID")
	assert.Equal(t, "db-password", generator.GetLabel())
	assert.Equal(t, "secrets", generator.GetStack())
}

// A KSUID no generator holds resolves to nothing and does not error: the
// synthesis logs it and the destination is refused at the provider boundary,
// naming the property.
func TestGeneratorLookupForResume_UnknownKsuidResolvesToNothing(t *testing.T) {
	ds := lookupTestDatastore(t)
	createGeneratorOn(t, ds, "secrets", "db-password", 24)

	lookup := generatorLookup(ds)
	generator, err := lookup("2ZqXo0nEXAMPLEksuidNOTREAL0")
	require.NoError(t, err)
	assert.Nil(t, generator)
}
