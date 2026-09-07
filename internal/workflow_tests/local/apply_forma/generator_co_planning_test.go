// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// These tests pin what a generator's draw obliges the command to plan. A draw
// reaches every destination that is a node in the changeset and nothing else,
// and formae keeps only a hash of a drawn value, so a destination left out
// cannot be caught up afterwards. Adding a second destination to a generator
// therefore has to plan the destination already applied beside it, even though
// nothing about that destination moved: the draw the new one needs is the only
// way either of them can get a value, so they move together or they diverge.
//
// The obligation is bounded by the same fact in the other direction. A
// generator that is not drawing owes nothing, so nothing may be dragged into
// the command on its account, or every apply would rewrite every
// generator-bound resource and rotate credentials that nobody asked to rotate.

// describedGenBoundSecret is genBoundSecret with a Description field, so a test
// can move something that has nothing to do with the binding.
func describedGenBoundSecret(stack, label, generatorLabel, description string) pkgmodel.Resource {
	secret := genBoundSecret(stack, label, generatorLabel, "value")
	secret.Properties = json.RawMessage(`{
		"Name": "` + label + `",
		"Description": "` + description + `",
		"SecretString": {
			"$gen":        true,
			"$label":      "` + generatorLabel + `",
			"$stack":      "` + stack + `",
			"$output":     "value",
			"$visibility": "Opaque"
		}
	}`)
	return secret
}

// Adding a second destination to a generator that already has one draws a new
// value, and both destinations must end up holding it. The forma text of the
// applied destination is untouched, so nothing about it moved and the ordinary
// planning pass suppresses it; it has to be co-planned onto the strength of
// the draw alone, or the new destination takes the new generation and the
// applied one keeps the old.
//
// "No error" is not the assertion. A build that plans nothing would also
// return no error and leave the two destinations holding different values, so
// what is asserted is the values themselves: the same one on both, different
// from what the applied destination held before, under a generation that
// advanced.
func TestApplyForma_GeneratorBoundSecret_AddingASecondDestinationRotatesBoth(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := func(resources ...pkgmodel.Resource) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources:  resources,
			}
		}
		alpha := genBoundSecret(stack, "alpha", "db-password", "value")
		beta := genBoundSecret(stack, "beta", "db-password", "value")

		_, err = m.ApplyForma(forma(alpha),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID, "the first apply must draw")
		alphaCreates := delivered.createdWith("alpha")
		require.Len(t, alphaCreates, 1, "alpha must have been created with the first draw")

		// The second apply adds beta and changes nothing else. alpha's forma
		// text is byte-identical to what was applied a moment ago.
		_, err = m.ApplyForma(forma(alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "adding a destination to an existing generator must be appliable")
		waitForApplyComplete(t, m)

		betaCreates := delivered.createdWith("beta")
		require.Len(t, betaCreates, 1, "beta must have been created")
		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1,
			"the applied destination must be planned and written, though nothing about it moved")
		assert.Equal(t, betaCreates[0], alphaUpdates[0],
			"two destinations of one generator must hold the same value")
		assert.NotEqual(t, alphaCreates[0], alphaUpdates[0],
			"the applied destination must move to the new value, not keep the old one")

		alphaPatches := delivered.patchesFor("alpha")
		require.Len(t, alphaPatches, 1)
		assert.Contains(t, alphaPatches[0], "/SecretString",
			"a delivered value must appear in the patch, or a patch-driven plugin never writes it")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEqual(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"adding a destination draws a new generation")

		// Both rows must be stamped with the generation the value came from,
		// or the next apply reads one of them as moved and rotates again.
		expected := provenance.DigestOfString(secondGeneration.GenerationID)
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "alpha"))
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "beta"))
	})
}

// The same, with the two destinations on separate stacks that the applied
// forma declares together. One command carries every resource update the forma
// produced, so the destination in the other stack is reachable and must be
// co-planned exactly as a sibling in one stack would be.
func TestApplyForma_GeneratorBoundSecret_AddingADestinationInAnotherDeclaredStackRotatesBoth(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		f := newTwoStackGeneratorFixture()
		generatorOnly := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: f.generatorStack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: f.generatorStack,
				Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			})},
			Resources: []pkgmodel.Resource{
				crossStackGenBoundSecret(f.generatorStack, "alpha", f.generatorStack, "db-password", "before"),
			},
		}

		_, err = m.ApplyForma(generatorOnly,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID)
		alphaCreates := delivered.createdWith("alpha")
		require.Len(t, alphaCreates, 1)

		// The second apply declares both stacks and adds beta to the other
		// one. alpha's forma text is unchanged.
		_, err = m.ApplyForma(f.bothStacks(t, 24, "before"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "a destination added in another declared stack must be appliable")
		waitForApplyComplete(t, m)

		betaCreates := delivered.createdWith("beta")
		require.Len(t, betaCreates, 1, "the destination in the other stack must be created")
		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1,
			"the applied destination must be planned and written, though nothing about it moved")
		assert.Equal(t, betaCreates[0], alphaUpdates[0],
			"one draw serves every destination bound to it, whatever stack it sits on")
		assert.NotEqual(t, alphaCreates[0], alphaUpdates[0],
			"the applied destination must move to the new value")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		require.NotEqual(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"adding a destination draws a new generation")

		expected := provenance.DigestOfString(secondGeneration.GenerationID)
		assert.Equal(t, expected, storedResolvedFrom(t, m, f.generatorStack, "alpha"))
		assert.Equal(t, expected, storedResolvedFrom(t, m, f.otherStack, "beta"))
	})
}

// Co-planning reaches what the applied forma declares. A destination the forma
// does not declare at all is outside the command whatever planning does, and
// the draw the new destination needs would leave it behind for good, so the
// apply is refused and the refusal names it.
func TestApplyForma_GeneratorBoundSecret_AddingADestinationIsRefusedWhenAnotherIsUndeclared(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		f := newTwoStackGeneratorFixture()
		_, err = m.ApplyForma(f.bothStacks(t, 24, "before"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID)

		// A third destination is added beside the generator while beta's stack
		// is left out of the forma. The new destination makes the generator
		// draw; beta cannot be reached by any amount of co-planning.
		withGamma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: f.generatorStack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: f.generatorStack,
				Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			})},
			Resources: []pkgmodel.Resource{
				crossStackGenBoundSecret(f.generatorStack, "alpha", f.generatorStack, "db-password", "before"),
				crossStackGenBoundSecret(f.generatorStack, "gamma", f.generatorStack, "db-password", "gamma"),
			},
		}

		_, err = m.ApplyForma(withGamma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "a draw that cannot reach an undeclared destination must be refused")

		var unreachable apimodel.FormaGeneratorDestinationsUnreachableError
		require.ErrorAs(t, err, &unreachable)
		require.Len(t, unreachable.Unreachable, 1,
			"only the destination the forma does not declare may be named")
		assert.Equal(t, "beta", unreachable.Unreachable[0].Label)
		assert.Equal(t, f.otherStack, unreachable.Unreachable[0].Stack)
		assert.Equal(t, "FakeAWS::SecretsManager::Secret", unreachable.Unreachable[0].Type)

		refusedGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		assert.Equal(t, firstGeneration.GenerationID, refusedGeneration.GenerationID,
			"the refusal must land before anything is drawn")
		assert.Empty(t, delivered.createdWith("gamma"), "nothing may be written by a refused apply")
	})
}

// Co-planning is owed to a draw and to nothing else. Re-applying the forma
// that added the second destination, unchanged, must plan nothing at all: no
// generator draws, so no destination is dragged in on a draw's account.
//
// A build that co-planned unconditionally would rotate the credential on this
// apply and on every one after it, so what is asserted is that the generation
// stood still and that no second command was created — not merely that the
// simulation reported no changes.
func TestApplyForma_GeneratorBoundSecret_ReapplyAfterAddingADestinationPlansNothing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := func(resources ...pkgmodel.Resource) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources:  resources,
			}
		}
		alpha := genBoundSecret(stack, "alpha", "db-password", "value")
		beta := genBoundSecret(stack, "beta", "db-password", "value")

		_, err = m.ApplyForma(forma(alpha),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		_, err = m.ApplyForma(forma(alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		settledGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, settledGeneration.GenerationID)
		require.Len(t, delivered.updatedWith("alpha"), 1)

		_, err = m.ApplyForma(forma(alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, cmds, 2, "an apply with nothing to do must not create a command")

		reappliedGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, settledGeneration.GenerationID, reappliedGeneration.GenerationID,
			"an apply that plans nothing must not draw")
		assert.Len(t, delivered.updatedWith("alpha"), 1, "no further write may reach the provider")
		assert.Len(t, delivered.updatedWith("beta"), 0, "no further write may reach the provider")
	})
}

// A generator that is not drawing pulls nothing into the command. Editing a
// field beside one destination's binding plans that destination and no other:
// the binding is stable, nothing draws, and the sibling destination's stored
// credential is left exactly as it was.
func TestApplyForma_GeneratorBoundSecret_AnUnrelatedEditPlansOnlyTheEditedDestination(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := func(alphaDescription string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources: []pkgmodel.Resource{
					describedGenBoundSecret(stack, "alpha", "db-password", alphaDescription),
					describedGenBoundSecret(stack, "beta", "db-password", "beta"),
				},
			}
		}

		_, err = m.ApplyForma(forma("before"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID)
		betaBefore := storedBinding(t, m, stack, "beta")
		require.True(t, betaBefore.Get("$hashed").Bool())
		require.NotEmpty(t, betaBefore.Get("$value").String())

		_, err = m.ApplyForma(forma("after"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		assert.Len(t, delivered.updatedWith("alpha"), 1, "the edited destination must be written")
		assert.Empty(t, delivered.updatedWith("beta"),
			"a generator that is not drawing must not pull its other destinations into the command")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"an edit beside a stable binding must not draw")

		betaAfter := storedBinding(t, m, stack, "beta")
		assert.Equal(t, betaBefore.Get("$value").String(), betaAfter.Get("$value").String(),
			"the untouched destination must keep the credential it held")
		assert.Equal(t, betaBefore.Get("$resolvedFrom").String(), betaAfter.Get("$resolvedFrom").String(),
			"the untouched destination must keep the generation it was drawn from")
	})
}

// The obligation is the same under either apply mode. Reconcile and patch
// build the same three views of the forma and hand
// NewResourceUpdateForExisting the same arguments, so a patch that adds a
// destination to an existing generator carries the destination already applied
// beside it exactly as a reconcile does, and both end on the value the draw
// produced.
//
// What patch does NOT do is the point of running it here: it plans no delete
// for what the forma leaves out, so the co-planned destination is in the
// command because a draw obliged it, not because a reconcile was sweeping the
// stack anyway.
func TestApplyForma_GeneratorBoundSecret_PatchAddingADestinationRotatesBoth(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := func(resources ...pkgmodel.Resource) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources:  resources,
			}
		}
		alpha := genBoundSecret(stack, "alpha", "db-password", "value")
		beta := genBoundSecret(stack, "beta", "db-password", "value")

		_, err = m.ApplyForma(forma(alpha),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID, "the first apply must draw")
		alphaCreates := delivered.createdWith("alpha")
		require.Len(t, alphaCreates, 1)

		_, err = m.ApplyForma(forma(alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "test-client-id", "", "")
		require.NoError(t, err, "a patch that adds a destination to an existing generator must be appliable")
		waitForApplyComplete(t, m)

		betaCreates := delivered.createdWith("beta")
		require.Len(t, betaCreates, 1, "the added destination must be created")
		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1,
			"the applied destination must be co-planned under patch, though nothing about it moved")
		assert.Equal(t, betaCreates[0], alphaUpdates[0],
			"two destinations of one generator must hold the same value")
		assert.NotEqual(t, alphaCreates[0], alphaUpdates[0],
			"the applied destination must move to the new value, not keep the old one")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEqual(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"adding a destination draws a new generation")

		expected := provenance.DigestOfString(secondGeneration.GenerationID)
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "alpha"))
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "beta"))
	})
}
