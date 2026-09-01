// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// rotatingPasswordGenerator returns a password generator that declares a
// rotation cadence.
func rotatingPasswordGenerator(label string, stack *pkgmodel.Stack, everySeconds int) *pkgmodel.PasswordGenerator {
	gen := testPasswordGenerator(label, stack, 24)
	gen.Rotation = &pkgmodel.RotationSpec{EverySeconds: everySeconds}
	return gen
}

// drawCommand stores a forma command in the given state and returns its ID and
// the instant it started. A draw records the command it was drawn by, and
// whether that command succeeded is what decides whether the cadence advanced.
func drawCommand(t *testing.T, ds datastore.Datastore, state forma_command.CommandState, startOffset time.Duration) (string, time.Time) {
	t.Helper()
	startTs := util.TimeNow().Add(startOffset).UTC()
	cmd := &forma_command.FormaCommand{
		ID:         util.NewID(),
		Command:    pkgmodel.CommandApply,
		Config:     config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		State:      state,
		StartTs:    startTs,
		ModifiedTs: startTs,
	}
	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))
	return cmd.ID, startTs
}

// rotationInfoFor picks one generator's rotation info out of the result, or
// fails the test.
func rotationInfoFor(t *testing.T, infos []datastore.GeneratorRotationInfo, generatorID string) datastore.GeneratorRotationInfo {
	t.Helper()
	for _, info := range infos {
		if info.GeneratorID == generatorID {
			return info
		}
	}
	t.Fatalf("no rotation info for generator %s in %+v", generatorID, infos)
	return datastore.GeneratorRotationInfo{}
}

// RunGetGeneratorsWithRotation_CadenceAndStackLabel verifies the happy path: a
// generator that declares a cadence comes back with it, named by the stack
// that owns it.
func RunGetGeneratorsWithRotation_CadenceAndStackLabel(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_CadenceAndStackLabel", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-owner")
		gen := rotatingPasswordGenerator("db-password", stack, 3600)
		_, err := ds.CreateGenerator(gen, "cmd-create")
		require.NoError(t, err)

		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)

		info := rotationInfoFor(t, infos, identity.ID)
		assert.Equal(t, "db-password", info.Label)
		assert.Equal(t, stack.Label, info.StackLabel)
		assert.Equal(t, 3600, info.IntervalSeconds)
		assert.True(t, info.LastRotationAt.IsZero(),
			"a generator that has never rotated must carry no last-rotation instant, got %s", info.LastRotationAt)
	})
}

// RunGetGeneratorsWithRotation_WithoutCadenceExcluded verifies that a
// generator declaring no cadence never appears: nothing rotates on its own.
func RunGetGeneratorsWithRotation_WithoutCadenceExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_WithoutCadenceExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-none")
		_, err := ds.CreateGenerator(testPasswordGenerator("db-password", stack, 24), "cmd-create")
		require.NoError(t, err)

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		for _, info := range infos {
			assert.NotEqual(t, "db-password", info.Label,
				"a generator with no declared cadence must not be scheduled")
		}
	})
}

// RunGetGeneratorsWithRotation_LastRotationFromCommittedDraw verifies the
// derivation: the last-rotation instant is the start of the successful command
// that drew the generation, read back from command history rather than from
// anything stored on the generator.
func RunGetGeneratorsWithRotation_LastRotationFromCommittedDraw(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_LastRotationFromCommittedDraw", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-committed")
		gen := rotatingPasswordGenerator("db-password", stack, 3600)
		_, err := ds.CreateGenerator(gen, "cmd-create")
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		commandID, startTs := drawCommand(t, ds, forma_command.CommandStateSuccess, -30*time.Minute)
		spec, err := json.Marshal(gen)
		require.NoError(t, err)
		require.NoError(t, ds.AdvanceGeneration(identity.ID, "generation-1", commandID, spec))

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		info := rotationInfoFor(t, infos, identity.ID)
		assert.WithinDuration(t, startTs, info.LastRotationAt, 2*time.Second,
			"the last-rotation instant must be the committed draw's command")
	})
}

// RunGetGeneratorsWithRotation_FailedCommandAdvancesNothing verifies that a
// draw whose command did not succeed leaves the cadence where it was. The
// generation row exists — the value was drawn — but nothing propagated it, so
// the interval must not restart from it.
func RunGetGeneratorsWithRotation_FailedCommandAdvancesNothing(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_FailedCommandAdvancesNothing", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-failed")
		gen := rotatingPasswordGenerator("db-password", stack, 3600)
		_, err := ds.CreateGenerator(gen, "cmd-create")
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		spec, err := json.Marshal(gen)
		require.NoError(t, err)

		failedID, _ := drawCommand(t, ds, forma_command.CommandStateFailed, -10*time.Minute)
		require.NoError(t, ds.AdvanceGeneration(identity.ID, "generation-1", failedID, spec))

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		info := rotationInfoFor(t, infos, identity.ID)
		assert.True(t, info.LastRotationAt.IsZero(),
			"a draw whose command failed must advance no cadence, got %s", info.LastRotationAt)

		// A later successful draw does advance it, so the assertion above is
		// about the command's outcome and not about the query finding nothing.
		successID, successTs := drawCommand(t, ds, forma_command.CommandStateSuccess, -5*time.Minute)
		require.NoError(t, ds.AdvanceGeneration(identity.ID, "generation-2", successID, spec))

		infos, err = ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		info = rotationInfoFor(t, infos, identity.ID)
		assert.WithinDuration(t, successTs, info.LastRotationAt, 2*time.Second)
	})
}

// RunGetGeneratorsWithRotation_LatestCommittedDrawWins verifies that the
// cadence is measured from the most recent committed draw, not the first.
func RunGetGeneratorsWithRotation_LatestCommittedDrawWins(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_LatestCommittedDrawWins", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-latest")
		gen := rotatingPasswordGenerator("db-password", stack, 3600)
		_, err := ds.CreateGenerator(gen, "cmd-create")
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)
		spec, err := json.Marshal(gen)
		require.NoError(t, err)

		oldID, _ := drawCommand(t, ds, forma_command.CommandStateSuccess, -3*time.Hour)
		require.NoError(t, ds.AdvanceGeneration(identity.ID, "generation-1", oldID, spec))
		newID, newTs := drawCommand(t, ds, forma_command.CommandStateSuccess, -1*time.Hour)
		require.NoError(t, ds.AdvanceGeneration(identity.ID, "generation-2", newID, spec))

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		info := rotationInfoFor(t, infos, identity.ID)
		assert.WithinDuration(t, newTs, info.LastRotationAt, 2*time.Second)
	})
}

// RunGetGeneratorsWithRotation_PerGeneratorLastRotation verifies that one
// generator's committed draw says nothing about another's: two generators on
// one stack keep separate cadences.
func RunGetGeneratorsWithRotation_PerGeneratorLastRotation(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_PerGeneratorLastRotation", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-per-generator")
		drawn := rotatingPasswordGenerator("drawn-password", stack, 3600)
		_, err := ds.CreateGenerator(drawn, "cmd-create")
		require.NoError(t, err)
		untouched := rotatingPasswordGenerator("untouched-password", stack, 7200)
		_, err = ds.CreateGenerator(untouched, "cmd-create")
		require.NoError(t, err)

		drawnIdentity, err := ds.GetGeneratorIdentity("drawn-password", stack.Label)
		require.NoError(t, err)
		untouchedIdentity, err := ds.GetGeneratorIdentity("untouched-password", stack.Label)
		require.NoError(t, err)

		spec, err := json.Marshal(drawn)
		require.NoError(t, err)
		commandID, startTs := drawCommand(t, ds, forma_command.CommandStateSuccess, -20*time.Minute)
		require.NoError(t, ds.AdvanceGeneration(drawnIdentity.ID, "generation-1", commandID, spec))

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		assert.WithinDuration(t, startTs, rotationInfoFor(t, infos, drawnIdentity.ID).LastRotationAt, 2*time.Second)
		assert.True(t, rotationInfoFor(t, infos, untouchedIdentity.ID).LastRotationAt.IsZero(),
			"advancing one generator must not restart another's cadence")
		assert.Equal(t, 7200, rotationInfoFor(t, infos, untouchedIdentity.ID).IntervalSeconds)
	})
}

// RunGetGeneratorsWithRotation_DeletedGeneratorExcluded verifies that a
// deleted generator is not scheduled.
func RunGetGeneratorsWithRotation_DeletedGeneratorExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_DeletedGeneratorExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-deleted")
		_, err := ds.CreateGenerator(rotatingPasswordGenerator("db-password", stack, 3600), "cmd-create")
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		rotationInfoFor(t, infos, identity.ID)

		_, err = ds.DeleteGenerator("db-password", stack.Label)
		require.NoError(t, err)

		infos, err = ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		for _, info := range infos {
			assert.NotEqual(t, identity.ID, info.GeneratorID,
				"a deleted generator must not be scheduled")
		}
	})
}

// RunGetGeneratorsWithRotation_RemovedCadenceExcluded verifies that dropping
// the rotation block from a generator takes it off the schedule, read from the
// generator's latest version rather than from any version that ever declared
// a cadence.
func RunGetGeneratorsWithRotation_RemovedCadenceExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_RemovedCadenceExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-removed")
		_, err := ds.CreateGenerator(rotatingPasswordGenerator("db-password", stack, 3600), "cmd-create")
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		_, err = ds.UpdateGenerator(testPasswordGenerator("db-password", stack, 24), "cmd-update")
		require.NoError(t, err)

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		for _, info := range infos {
			assert.NotEqual(t, identity.ID, info.GeneratorID,
				"a generator whose cadence was removed must come off the schedule")
		}
	})
}

// RunGetGeneratorsWithRotation_ChangedCadenceReadFromLatest verifies that an
// edited cadence is the one that comes back.
func RunGetGeneratorsWithRotation_ChangedCadenceReadFromLatest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_ChangedCadenceReadFromLatest", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createGeneratorStack(t, ds, "rotation-changed")
		_, err := ds.CreateGenerator(rotatingPasswordGenerator("db-password", stack, 3600), "cmd-create")
		require.NoError(t, err)
		identity, err := ds.GetGeneratorIdentity("db-password", stack.Label)
		require.NoError(t, err)

		_, err = ds.UpdateGenerator(rotatingPasswordGenerator("db-password", stack, 86400), "cmd-update")
		require.NoError(t, err)

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		assert.Equal(t, 86400, rotationInfoFor(t, infos, identity.ID).IntervalSeconds)
	})
}

// RunGetGeneratorsWithRotation_Empty verifies the contract on a fresh
// datastore: no rows, no error.
func RunGetGeneratorsWithRotation_Empty(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetGeneratorsWithRotation_Empty", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		infos, err := ds.GetGeneratorsWithRotation()
		require.NoError(t, err)
		assert.Empty(t, infos)
	})
}
