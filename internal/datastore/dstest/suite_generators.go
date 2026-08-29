// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createGeneratorStack creates a stack for the generator suite tests and
// returns it with its generated ID populated. A generator is always inline
// to a stack — unlike a policy it has no standalone form — so every test
// needs one, and generators.stack_id stores the stack's KSUID, so tests need
// the ID as well as the label.
func createGeneratorStack(t *testing.T, ds datastore.Datastore, label string) *pkgmodel.Stack {
	t.Helper()
	stack := &pkgmodel.Stack{Label: label, Description: "generator lookup"}
	_, err := ds.CreateStack(stack, "cmd-stack")
	require.NoError(t, err)
	require.NotEmpty(t, stack.ID)
	return stack
}

// testPasswordGenerator returns a password generator on the given stack, with
// Length as the one field the suite varies to observe an update. StackID is
// set directly from the resolved stack, the way the policy suite sets
// StackID on a TTLPolicy — the label-to-ID resolution a real apply performs
// is out of scope here.
func testPasswordGenerator(label string, stack *pkgmodel.Stack, length int) *pkgmodel.PasswordGenerator {
	return &pkgmodel.PasswordGenerator{
		Label:                   label,
		Stack:                   stack.Label,
		StackID:                 stack.ID,
		Length:                  length,
		Uppercase:               true,
		Lowercase:               true,
		Digits:                  true,
		RequireEachIncludedType: true,
	}
}

// RunCreateGeneratorThenGet verifies that a created generator is retrievable
// by label and stack, with its data intact.
func RunCreateGeneratorThenGet(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CreateGenerator_ThenGet", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-owner")
		gen := testPasswordGenerator("db-password", stack, 24)

		version, err := ds.CreateGenerator(gen, "cmd-create")
		require.NoError(t, err)
		require.NotEmpty(t, version)

		got, err := ds.GetGenerator("db-password", stack.Label)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "db-password", got.GetLabel())
		assert.Equal(t, "password", got.GetType())
		assert.Equal(t, stack.Label, got.GetStack())
		pw, ok := got.(*pkgmodel.PasswordGenerator)
		require.True(t, ok, "GetGenerator must return the concrete password generator type")
		assert.Equal(t, 24, pw.Length)
	})
}

// RunGetGeneratorAbsentReturnsNil verifies that looking up a generator that
// was never created returns nil, nil rather than an error.
func RunGetGeneratorAbsentReturnsNil(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGenerator_AbsentReturnsNil", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-empty")

		got, err := ds.GetGenerator("never-created", stack.Label)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

// RunUpdateGeneratorBumpsVersionAndReadBackReflectsIt verifies that updating a
// generator returns a new version distinct from creation, and that a
// subsequent GetGenerator reflects the updated data.
func RunUpdateGeneratorBumpsVersionAndReadBackReflectsIt(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateGenerator_BumpsVersionAndReadBackReflectsIt", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-updated")
		createVersion, err := ds.CreateGenerator(testPasswordGenerator("api-key", stack, 16), "cmd-create")
		require.NoError(t, err)

		updateVersion, err := ds.UpdateGenerator(testPasswordGenerator("api-key", stack, 32), "cmd-update")
		require.NoError(t, err)

		assert.NotEqual(t, createVersion, updateVersion, "an update must mint a new version")

		got, err := ds.GetGenerator("api-key", stack.Label)
		require.NoError(t, err)
		require.NotNil(t, got)
		pw, ok := got.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, 32, pw.Length, "the read-back generator must reflect the update")
	})
}

// RunDeleteGeneratorThenGetReturnsNil verifies that a deleted generator is no
// longer returned by GetGenerator.
func RunDeleteGeneratorThenGetReturnsNil(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteGenerator_ThenGetReturnsNil", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-deleted")
		_, err := ds.CreateGenerator(testPasswordGenerator("temp-secret", stack, 20), "cmd-create")
		require.NoError(t, err)

		version, err := ds.DeleteGenerator("temp-secret", stack.Label)
		require.NoError(t, err)
		require.NotEmpty(t, version)

		got, err := ds.GetGenerator("temp-secret", stack.Label)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

// RunLoadGeneratorsByStackReturnsOnlyThatStacksGenerators verifies that
// loading by stack does not leak another stack's generators.
func RunLoadGeneratorsByStackReturnsOnlyThatStacksGenerators(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("LoadGeneratorsByStack_ReturnsOnlyThatStacksGenerators", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		owner := createGeneratorStack(t, ds, "generator-load-owner")
		other := createGeneratorStack(t, ds, "generator-load-other")

		_, err := ds.CreateGenerator(testPasswordGenerator("owner-a", owner, 12), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreateGenerator(testPasswordGenerator("owner-b", owner, 12), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreateGenerator(testPasswordGenerator("other-a", other, 12), "cmd-create")
		require.NoError(t, err)

		generators, err := ds.LoadGeneratorsByStack(owner.Label)
		require.NoError(t, err)

		labels := make([]string, 0, len(generators))
		for _, g := range generators {
			labels = append(labels, g.GetLabel())
			assert.Equal(t, owner.Label, g.GetStack())
		}
		assert.ElementsMatch(t, []string{"owner-a", "owner-b"}, labels)
	})
}

// RunGeneratorKSUIDStableAcrossUpdate verifies that a generator's internal
// KSUID identity does not change when it is updated — only the label and
// stack are looked up, and the same id is carried forward onto the new
// version row. This is load-bearing: a later slice derives generator cadence
// per id, so a rename must not read as a delete plus a fresh generator.
func RunGeneratorKSUIDStableAcrossUpdate(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("Generator_KSUIDStableAcrossUpdate", func(t *testing.T) {
		td := newDS(t)
		if td.GeneratorIDForTest == nil {
			t.Skip("backend does not provide GeneratorIDForTest")
		}
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-ksuid-stable")
		_, err := ds.CreateGenerator(testPasswordGenerator("stable-id", stack, 16), "cmd-create")
		require.NoError(t, err)

		idBeforeUpdate, err := td.GeneratorIDForTest("stable-id", stack.Label)
		require.NoError(t, err)
		require.NotEmpty(t, idBeforeUpdate)

		_, err = ds.UpdateGenerator(testPasswordGenerator("stable-id", stack, 40), "cmd-update")
		require.NoError(t, err)

		idAfterUpdate, err := td.GeneratorIDForTest("stable-id", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, idBeforeUpdate, idAfterUpdate, "the generator's KSUID identity must survive an update")
	})
}

// RunGeneratorKSUIDStableAcrossRename verifies that a generator's KSUID
// identity survives a rename: UpdateGenerator is called with a new label and
// Alias set to the previous one, and the row found by falling back to the
// alias lookup is updated in place — same id, new label — rather than a miss
// that would force the caller to fall back to Create and mint a fresh id.
func RunGeneratorKSUIDStableAcrossRename(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("Generator_KSUIDStableAcrossRename", func(t *testing.T) {
		td := newDS(t)
		if td.GeneratorIDForTest == nil {
			t.Skip("backend does not provide GeneratorIDForTest")
		}
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-ksuid-rename")
		_, err := ds.CreateGenerator(testPasswordGenerator("old-label", stack, 16), "cmd-create")
		require.NoError(t, err)

		idBeforeRename, err := td.GeneratorIDForTest("old-label", stack.Label)
		require.NoError(t, err)
		require.NotEmpty(t, idBeforeRename)

		renamed := testPasswordGenerator("new-label", stack, 16)
		renamed.Alias = "old-label"
		_, err = ds.UpdateGenerator(renamed, "cmd-rename")
		require.NoError(t, err)

		idAfterRename, err := td.GeneratorIDForTest("new-label", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, idBeforeRename, idAfterRename, "the generator's KSUID identity must survive a rename")

		oldLabelGone, err := ds.GetGenerator("old-label", stack.Label)
		require.NoError(t, err)
		assert.Nil(t, oldLabelGone, "the previous label must no longer resolve after rename")

		current, err := ds.GetGenerator("new-label", stack.Label)
		require.NoError(t, err)
		require.NotNil(t, current)
	})
}
