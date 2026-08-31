// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
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

// RunCreateGeneratorHonorsPreAssignedID verifies that CreateGenerator uses a
// pre-assigned ID (set via Generator.SetID, the way
// generator_update.GenerateGeneratorUpdates assigns the KSUID
// resource_update's translation phase already resolved for a $gen reference
// to this generator) rather than minting an unrelated one — mirroring
// StoreResource's identical handling of a pre-assigned Resource.Ksuid. This
// is what makes a same-command generator-and-consumer apply end up with one
// shared KSUID instead of two independently minted ones.
func RunCreateGeneratorHonorsPreAssignedID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CreateGenerator_HonorsPreAssignedID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-preassigned-id")
		gen := testPasswordGenerator("db-password", stack, 24)
		gen.SetID("2preassignedksuid00000000000")

		_, err := ds.CreateGenerator(gen, "cmd-create")
		require.NoError(t, err)

		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, "2preassignedksuid00000000000", identity.ID,
			"the persisted row's KSUID must equal the pre-assigned one, not an independently minted one")
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

// RunDeleteGeneratorAfterRenameDeletesOnlyTheCurrentRow verifies
// DeleteGenerator's windowing: after label A is renamed to B (same row, same
// id) and a fresh generator is created under the now-free label A, deleting
// A must remove only the fresh row. Filtering by label before windowing to
// the latest version per id would instead find the renamed row's own stale
// A-labelled version — still the only row under that label in a
// label-first filter — and soft-delete the live, renamed generator B.
func RunDeleteGeneratorAfterRenameDeletesOnlyTheCurrentRow(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteGenerator_AfterRenameDeletesOnlyTheCurrentRow", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-delete-after-rename")

		_, err := ds.CreateGenerator(testPasswordGenerator("A", stack, 16), "cmd-create-a")
		require.NoError(t, err)

		renamed := testPasswordGenerator("B", stack, 16)
		renamed.Alias = "A"
		_, err = ds.UpdateGenerator(renamed, "cmd-rename")
		require.NoError(t, err)

		_, err = ds.CreateGenerator(testPasswordGenerator("A", stack, 20), "cmd-create-a-again")
		require.NoError(t, err)

		_, err = ds.DeleteGenerator("A", stack.Label)
		require.NoError(t, err)

		stillB, err := ds.GetGenerator("B", stack.Label)
		require.NoError(t, err)
		assert.NotNil(t, stillB, "the renamed generator must survive deleting the label it no longer has")

		goneA, err := ds.GetGenerator("A", stack.Label)
		require.NoError(t, err)
		assert.Nil(t, goneA, "the fresh generator created under the reused label must be deleted")
	})
}

// RunGeneratorHasNoGenerationUntilOneIsDrawn: a newly created generator holds
// no generation, so a destination bound to it has nothing to resolve against.
func RunGeneratorHasNoGenerationUntilOneIsDrawn(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GeneratorHasNoGenerationUntilOneIsDrawn", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		_, err := ds.CreateGenerator(testPasswordGenerator("db-password", stack, 32), "cmd-1")
		require.NoError(t, err)

		id, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.NotEmpty(t, id.ID)
		assert.Empty(t, id.GenerationID)
		assert.Nil(t, id.GenerationSpec)
	})
}

// RunAdvanceGenerationRecordsIdentityAndDrawingSpec verifies that
// AdvanceGeneration records both the generation id and the spec it was drawn
// under, while preserving the generator's own KSUID identity.
func RunAdvanceGenerationRecordsIdentityAndDrawingSpec(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AdvanceGenerationRecordsIdentityAndDrawingSpec", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		before, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(before.ID, "generation-1", spec))

		after, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, before.ID, after.ID, "advancing a generation must preserve the generator KSUID")
		assert.Equal(t, "generation-1", after.GenerationID)
		assert.JSONEq(t, string(spec), string(after.GenerationSpec))
	})
}

// RunGenerationSurvivesASpecUpdate: editing the spec writes a new version row
// but must not disturb the generation the generator currently holds.
func RunGenerationSurvivesASpecUpdate(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GenerationSurvivesASpecUpdate", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		before, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(before.ID, "generation-1", spec))

		_, err = ds.UpdateGenerator(testPasswordGenerator("db-password", stack, 40), "cmd-2")
		require.NoError(t, err)

		after, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, before.ID, after.ID, "a spec edit must preserve the generator KSUID")
		assert.Equal(t, "generation-1", after.GenerationID, "a spec edit must not drop the generation the generator currently holds")
		assert.JSONEq(t, string(spec), string(after.GenerationSpec))

		got, err := ds.GetGenerator("db-password", stack.Label)
		require.NoError(t, err)
		pw, ok := got.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, 40, pw.Length, "the spec edit itself must still take effect")
	})
}

// RunGenerationSurvivesARename: an alias rename writes a new version row; the
// generation must not move, because a moved generation rotates a live credential.
func RunGenerationSurvivesARename(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GenerationSurvivesARename", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("old-label", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		before, err := ds.GetGeneratorIdentity("old-label", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(before.ID, "generation-1", spec))

		renamed := testPasswordGenerator("new-label", stack, 32)
		renamed.Alias = "old-label"
		_, err = ds.UpdateGenerator(renamed, "cmd-2")
		require.NoError(t, err)

		after, err := ds.GetGeneratorIdentity("new-label", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, before.ID, after.ID, "a rename must preserve the generator KSUID")
		assert.Equal(t, "generation-1", after.GenerationID, "a rename must not move the generation the generator currently holds")
		assert.JSONEq(t, string(spec), string(after.GenerationSpec))
	})
}

// RunGetGeneratorIdentityByIDFindsTheLiveRow verifies that
// GetGeneratorIdentityByID resolves the current (latest, non-deleted) row for
// a generator id directly, without a stack lookup, and reflects a generation
// copied forward by a later update rather than the row the id was minted on.
func RunGetGeneratorIdentityByIDFindsTheLiveRow(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorIdentityByIDFindsTheLiveRow", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		before, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(before.ID, "generation-1", spec))

		// Write a further version row so the row GetGeneratorIdentityByID
		// must resolve is not the one the id was originally minted on.
		_, err = ds.UpdateGenerator(testPasswordGenerator("db-password", stack, 40), "cmd-2")
		require.NoError(t, err)

		byID, err := ds.GetGeneratorIdentityByID(before.ID)
		require.NoError(t, err)
		assert.Equal(t, before.ID, byID.ID)
		assert.Equal(t, "generation-1", byID.GenerationID, "the generation copied forward by the update must still be visible by id")
		assert.JSONEq(t, string(spec), string(byID.GenerationSpec))
	})
}

// RunGetGeneratorIdentityAbsentReturnsZeroValue verifies that looking up an
// identity that was never created returns a zero GeneratorIdentity and a nil
// error, matching GetGenerator's absent-is-not-an-error convention, for both
// the label-scoped and the id-scoped lookup.
func RunGetGeneratorIdentityAbsentReturnsZeroValue(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorIdentityAbsentReturnsZeroValue", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "generator-identity-empty")

		byLabel, err := ds.GetGeneratorIdentity("never-created", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, datastore.GeneratorIdentity{}, byLabel)

		byID, err := ds.GetGeneratorIdentityByID("nonexistent-id")
		require.NoError(t, err)
		assert.Equal(t, datastore.GeneratorIdentity{}, byID)
	})
}

// RunGeneratorIdentityOldLabelIsGoneAfterRename verifies that after a
// rename, the identity lookup on the OLD label returns the zero value: the
// live row now carries the new label, and windowing-before-label-match must
// not let the superseded row under the old label answer for it.
func RunGeneratorIdentityOldLabelIsGoneAfterRename(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GeneratorIdentityOldLabelIsGoneAfterRename", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		_, err := ds.CreateGenerator(testPasswordGenerator("old-label", stack, 32), "cmd-1")
		require.NoError(t, err)

		renamed := testPasswordGenerator("new-label", stack, 32)
		renamed.Alias = "old-label"
		_, err = ds.UpdateGenerator(renamed, "cmd-2")
		require.NoError(t, err)

		gone, err := ds.GetGeneratorIdentity("old-label", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, datastore.GeneratorIdentity{}, gone, "the previous label must no longer resolve an identity after rename")
	})
}

// RunGeneratorIdentityGoneAfterDelete verifies that after a delete, both the
// label-scoped and the id-scoped identity lookups return the zero value: the
// tombstone row must not answer for either.
func RunGeneratorIdentityGoneAfterDelete(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GeneratorIdentityGoneAfterDelete", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		_, err := ds.CreateGenerator(testPasswordGenerator("db-password", stack, 32), "cmd-1")
		require.NoError(t, err)

		before, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		require.NotEmpty(t, before.ID)

		_, err = ds.DeleteGenerator("db-password", stack.Label)
		require.NoError(t, err)

		byLabel, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, datastore.GeneratorIdentity{}, byLabel, "a deleted generator must not resolve an identity by label")

		byID, err := ds.GetGeneratorIdentityByID(before.ID)
		require.NoError(t, err)
		assert.Equal(t, datastore.GeneratorIdentity{}, byID, "a deleted generator must not resolve an identity by id")
	})
}

// RunGenerationSurvivesRenameBackToOriginalLabel verifies that renaming a
// generator back to a label it previously held keeps the generation it
// currently holds, resolving by the live row's latest version rather than
// an older row that happens to share the label.
func RunGenerationSurvivesRenameBackToOriginalLabel(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GenerationSurvivesRenameBackToOriginalLabel", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("A", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		toB := testPasswordGenerator("B", stack, 32)
		toB.Alias = "A"
		_, err = ds.UpdateGenerator(toB, "cmd-2")
		require.NoError(t, err)

		before, err := ds.GetGeneratorIdentity("B", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(before.ID, "generation-1", spec))

		backToA := testPasswordGenerator("A", stack, 32)
		backToA.Alias = "B"
		_, err = ds.UpdateGenerator(backToA, "cmd-3")
		require.NoError(t, err)

		after, err := ds.GetGeneratorIdentity("A", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, before.ID, after.ID, "renaming back to the original label must preserve the generator KSUID")
		assert.Equal(t, "generation-1", after.GenerationID, "renaming back to a previously held label must not drop the generation")
		assert.JSONEq(t, string(spec), string(after.GenerationSpec))
	})
}

// RunAdvanceGenerationTwiceSecondWins verifies that a second call to
// AdvanceGeneration replaces the first: the identity reflects the latest
// draw, not the first one.
func RunAdvanceGenerationTwiceSecondWins(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AdvanceGenerationTwiceSecondWins", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		id, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		specOne, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(id.ID, "generation-1", specOne))

		gen.Length = 40
		specTwo, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(id.ID, "generation-2", specTwo))

		after, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, id.ID, after.ID)
		assert.Equal(t, "generation-2", after.GenerationID, "the second draw must win over the first")
		assert.JSONEq(t, string(specTwo), string(after.GenerationSpec))
	})
}

// RunAdvanceGenerationDoesNotAffectOtherGenerator verifies that advancing
// one generator's generation on a stack leaves a second, unrelated
// generator on the same stack untouched.
func RunAdvanceGenerationDoesNotAffectOtherGenerator(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AdvanceGenerationDoesNotAffectOtherGenerator", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		genA := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(genA, "cmd-1")
		require.NoError(t, err)
		genB := testPasswordGenerator("api-key", stack, 16)
		_, err = ds.CreateGenerator(genB, "cmd-2")
		require.NoError(t, err)

		idA, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		idB, err := ds.GetGeneratorIdentity("api-key", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(genA)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(idA.ID, "generation-1", spec))

		afterA, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, "generation-1", afterA.GenerationID)

		afterB, err := ds.GetGeneratorIdentity("api-key", stack.Label)
		require.NoError(t, err)
		assert.Equal(t, idB.ID, afterB.ID, "the untouched generator's id must be unaffected")
		assert.Empty(t, afterB.GenerationID, "advancing one generator must not draw a generation for another")
		assert.Nil(t, afterB.GenerationSpec)
	})
}

// RunAdvanceGenerationOnDeletedGeneratorFailsWithoutResurrecting verifies
// AdvanceGeneration's tombstone guard: called against a deleted generator's
// id, it must return an error AND must not write a new row. An implementation
// that errors but still inserts a version row would resurrect the generator
// with an unparseable generator_data ('{}' copied from the tombstone),
// leaving GetGenerator permanently failing with "unknown generator type:" —
// so this checks both read paths stay at the zero value / nil, not just the
// error.
func RunAdvanceGenerationOnDeletedGeneratorFailsWithoutResurrecting(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AdvanceGenerationOnDeletedGeneratorFailsWithoutResurrecting", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		id, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(id.ID, "generation-1", spec))

		_, err = ds.DeleteGenerator("db-password", stack.Label)
		require.NoError(t, err)

		err = ds.AdvanceGeneration(id.ID, "generation-2", spec)
		assert.Error(t, err, "advancing a deleted generator must fail")

		byID, err := ds.GetGeneratorIdentityByID(id.ID)
		require.NoError(t, err)
		assert.Equal(t, datastore.GeneratorIdentity{}, byID, "the deleted generator must not be resurrected by id")

		got, err := ds.GetGenerator("db-password", stack.Label)
		require.NoError(t, err, "a resurrected row with an unparseable spec would fail here instead of returning nil")
		assert.Nil(t, got, "the deleted generator must not be resurrected by label")
	})
}

// RunAdvanceGenerationRejectsMalformedSpecAndEmptyGenerationID verifies that
// AdvanceGeneration validates both of its inputs before writing anything: a
// drawnUnder spec that is not valid JSON is rejected the same way an empty
// one is, and an empty generationID is rejected too, rather than being
// written and read back indistinguishably from "no generation drawn".
func RunAdvanceGenerationRejectsMalformedSpecAndEmptyGenerationID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("AdvanceGenerationRejectsMalformedSpecAndEmptyGenerationID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "durable")
		gen := testPasswordGenerator("db-password", stack, 32)
		_, err := ds.CreateGenerator(gen, "cmd-1")
		require.NoError(t, err)

		id, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		validSpec, err := json.Marshal(gen)
		require.NoError(t, err)

		err = ds.AdvanceGeneration(id.ID, "generation-1", json.RawMessage(`{not valid json`))
		assert.Error(t, err, "a malformed drawnUnder spec must be rejected")

		err = ds.AdvanceGeneration(id.ID, "", validSpec)
		assert.Error(t, err, "an empty generationID must be rejected")

		after, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		assert.Empty(t, after.GenerationID, "neither rejected call may have written a generation")
	})
}
