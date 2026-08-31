// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_local

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// authoredRotatingGeneratorForma is the forma this test applies: a
// PasswordGenerator declaring `rotation { every = 1.s }` and a plugin resource
// whose secret-bearing property is bound to that generator's `value` output.
const authoredRotatingGeneratorForma = "internal/schema/pkl/testdata/forma/generator_rotation_test.pkl"

// The stack, generator and destination the authored forma declares.
const (
	rotatingFormaStack       = "generator-rotation-stack"
	rotatingFormaGenerator   = "db-password"
	rotatingFormaDestination = "db"
	rotatingFormaLength      = 24
)

// A cadence authored in PKL rotates the credential it governs. The forma is
// evaluated rather than hand-built, so the whole path a forma author actually
// writes is exercised: `rotation { every = ... }` on a generator, a resource
// property bound to that generator's output, and the scheduler moving the
// generation forward once the interval has elapsed.
//
// The two halves of this are each covered on their own — a binding authored in
// PKL applies, and a scheduler sweep rotates a generator built from model
// structs — and neither says the crossing works. A cadence that never reaches
// the model, or a destination the eval path renders in a shape the rotator
// cannot plan, fails here and nowhere else.
//
// The rotation is asserted through the value the provider received, not only
// through the generation counter: the second value has to be a fresh draw of
// the authored length, so neither a build that advanced the counter without
// writing anything nor one that wrote the same value twice can pass.
func TestApplyForma_PklAuthoredRotatingGenerator_RotatesOnItsDeclaredCadence(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logCapture := test_helpers.SetupTestLogger()

		forma, err := pkl.PKL{}.Evaluate(
			authoredRotatingGeneratorForma,
			pkgmodel.CommandApply,
			pkgmodel.FormaApplyModeReconcile,
			nil,
		)
		require.NoError(t, err, "the authored forma must evaluate")
		require.Len(t, forma.Resources, 1)
		require.Len(t, forma.Generators, 1)
		require.True(t, gjson.GetBytes(forma.Resources[0].Properties, "SecretString.$gen").Bool(),
			"precondition: eval must render the binding as a $gen envelope")
		require.Equal(t, int64(1), gjson.GetBytes(forma.Generators[0], "Rotation.EverySeconds").Int(),
			"precondition: the authored cadence must reach the model as an interval in seconds")

		delivered := newDeliveredValues()
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		created := delivered.createdWith(rotatingFormaDestination)
		require.Len(t, created, 1, "the destination must be created with a drawn value")
		assert.Len(t, created[0], rotatingFormaLength,
			"the provider receives the drawn value, at the authored length")
		assert.NotContains(t, created[0], "$gen",
			"the provider must receive the drawn value, never the envelope naming it")

		digestBefore := storedBinding(t, m, rotatingFormaStack, rotatingFormaDestination).Get("$value").String()
		require.NotEmpty(t, digestBefore)
		identityBefore, err := m.Datastore.GetGeneratorIdentity(rotatingFormaGenerator, rotatingFormaStack)
		require.NoError(t, err)
		require.NotEmpty(t, identityBefore.GenerationID)

		// Waiting first is what makes the sweep below say something about the
		// scheduler rather than about the clock.
		waitUntilRotationDue(t, m, rotatingFormaStack, rotatingFormaGenerator)
		rotation := sweepUntilRotated(t, m)
		require.Equal(t, forma_command.CommandStateSuccess, rotation.State,
			"the rotation of a PKL-authored cadence must succeed")
		assert.Equal(t, forma_command.SourceGeneratorRotator, rotation.Source)
		waitForApplyComplete(t, m)

		updates := delivered.updatedWith(rotatingFormaDestination)
		require.Len(t, updates, 1, "the rotation must write the destination exactly once")
		assert.Len(t, updates[0], rotatingFormaLength,
			"the rotated value must be drawn at the authored length")
		assert.NotEqual(t, created[0], updates[0],
			"the rotation must deliver a second, freshly drawn value")

		bindingAfter := storedBinding(t, m, rotatingFormaStack, rotatingFormaDestination)
		assert.True(t, bindingAfter.Get("$hashed").Bool(),
			"the rewritten destination must store a digest")
		assert.NotEqual(t, digestBefore, bindingAfter.Get("$value").String(),
			"the destination's stored digest must move with the rotated value")
		assert.Equal(t, pkgmodel.ComputeValueHash(updates[0]), bindingAfter.Get("$value").String(),
			"the stored digest must be the digest of the value the provider received")

		identityAfter, err := m.Datastore.GetGeneratorIdentity(rotatingFormaGenerator, rotatingFormaStack)
		require.NoError(t, err)
		assert.NotEqual(t, identityBefore.GenerationID, identityAfter.GenerationID,
			"the rotation must advance the generator's generation")

		for _, value := range append(created, updates...) {
			assertNoPlaintextInResourceUpdates(t, m, rotation.ID, value)
			assertNoPlaintextInLogs(t, logCapture, value)
		}
		resources, err := m.Datastore.LoadResourcesByStack(rotatingFormaStack)
		require.NoError(t, err)
		for i := range resources {
			for _, value := range append(created, updates...) {
				assert.NotContains(t, string(resources[i].Properties), value,
					"resources.properties leaked a rotated value for %s", resources[i].Label)
			}
		}
	})
}
