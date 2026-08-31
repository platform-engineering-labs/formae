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
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// These tests pin the reach a draw must have. formae keeps only a hash of a
// drawn value, never the value, so a destination that was not in the command
// when a draw happened can never be brought level afterwards: the only way to
// give it a value is to draw again, which puts its siblings behind. An apply
// that would draw for some of a generator's destinations but not all of them
// is refused, naming the ones it cannot reach.
//
// The refusal turns on which destinations the CHANGESET reaches, never on
// which stack they sit on. One command carries every resource update the
// forma produced, across all of its stacks, so a generator whose destinations
// span stacks is served fine as long as the apply covers them together.

// crossStackGenBoundSecret is a secret in resourceStack whose opaque
// SecretString binds to a generator declared in generatorStack.
func crossStackGenBoundSecret(resourceStack, label, generatorStack, generatorLabel, description string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:  label,
		Type:   "FakeAWS::SecretsManager::Secret",
		Stack:  resourceStack,
		Target: "test-target",
		Schema: secretSchema(),
		Properties: json.RawMessage(`{
			"Name": "` + label + `",
			"Description": "` + description + `",
			"SecretString": {
				"$gen":        true,
				"$label":      "` + generatorLabel + `",
				"$stack":      "` + generatorStack + `",
				"$output":     "value",
				"$visibility": "Opaque"
			}
		}`)}
}

// twoStackGeneratorFixture is the shape every test in this file starts from:
// one generator in generatorStack, one destination beside it, and a second
// destination in another stack entirely.
type twoStackGeneratorFixture struct {
	generatorStack string
	otherStack     string
}

func newTwoStackGeneratorFixture() twoStackGeneratorFixture {
	id := util.NewID()
	return twoStackGeneratorFixture{
		generatorStack: "gen-stack-" + id,
		otherStack:     "other-stack-" + id,
	}
}

// bothStacks is the forma that declares the generator and both of its
// destinations, with the generator at the given length so a test can move the
// spec and make every destination unstable.
func (f twoStackGeneratorFixture) bothStacks(t *testing.T, length int, alphaDescription string) *pkgmodel.Forma {
	t.Helper()
	return &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: f.generatorStack}, {Label: f.otherStack}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: f.generatorStack,
			Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		})},
		Resources: []pkgmodel.Resource{
			crossStackGenBoundSecret(f.generatorStack, "alpha", f.generatorStack, "db-password", alphaDescription),
			crossStackGenBoundSecret(f.otherStack, "beta", f.generatorStack, "db-password", "beta"),
		},
	}
}

// generatorStackOnly is the same forma with the other stack's destination left
// out entirely — the partial apply the refusal exists to stop.
func (f twoStackGeneratorFixture) generatorStackOnly(t *testing.T, length int, alphaDescription string) *pkgmodel.Forma {
	t.Helper()
	return &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: f.generatorStack}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: f.generatorStack,
			Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		})},
		Resources: []pkgmodel.Resource{
			crossStackGenBoundSecret(f.generatorStack, "alpha", f.generatorStack, "db-password", alphaDescription),
		},
	}
}

// A generator's destinations may live on separate stacks. One command carries
// every resource update the forma produced, so an apply covering both stacks
// reaches both destinations and must succeed, with both holding the one value
// the single draw produced.
//
// The second apply is what pins the rule. By then both destinations are live
// and indexed against the generator, and moving the spec makes both draw, so
// a refusal that read reach per stack rather than per command would reject an
// apply that in fact reaches everything.
func TestApplyForma_GeneratorBoundSecret_DestinationsInTwoStacksApplyTogether(t *testing.T) {
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
		require.NoError(t, err, "a generator whose destinations span stacks must apply when the command covers them all")
		waitForApplyComplete(t, m)

		alphaCreates := delivered.createdWith("alpha")
		betaCreates := delivered.createdWith("beta")
		require.Len(t, alphaCreates, 1, "the destination beside the generator must be written")
		require.Len(t, betaCreates, 1, "the destination in the other stack must be written")
		assert.Equal(t, alphaCreates[0], betaCreates[0],
			"one draw serves every destination bound to it, whatever stack it sits on")

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID, "the apply must draw")

		// Both destinations are stored and indexed now. Moving the spec makes
		// both of them draw, and the command reaches both.
		_, err = m.ApplyForma(f.bothStacks(t, 40, "after"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "a redraw that reaches every destination must not be refused because they span stacks")
		waitForApplyComplete(t, m)

		alphaUpdates := delivered.updatedWith("alpha")
		betaUpdates := delivered.updatedWith("beta")
		require.Len(t, alphaUpdates, 1, "the destination beside the generator must receive the redraw")
		require.Len(t, betaUpdates, 1, "the destination in the other stack must receive the redraw")
		assert.Len(t, alphaUpdates[0], 40, "the new value must be drawn under the edited spec")
		assert.Equal(t, alphaUpdates[0], betaUpdates[0],
			"one redraw serves every destination bound to it, whatever stack it sits on")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		assert.NotEqual(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"the spec edit must draw a new generation")
	})
}

// An apply that would draw but reaches only some of the generator's
// destinations is refused, and the refusal names the destination it cannot
// reach: its stack, its label and its type.
//
// Nothing may be drawn before the refusal. Observing that no destination was
// written would not tell a value that was never drawn from one that was drawn
// and thrown away, so the generation identity itself is asserted unchanged.
func TestApplyForma_GeneratorBoundSecret_PartialApplyThatWouldDrawIsRefused(t *testing.T) {
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

		// Moving the generator's spec makes every destination unstable, so
		// this apply would draw — for alpha only, since beta's stack is not
		// declared.
		_, err = m.ApplyForma(f.generatorStackOnly(t, 40, "before"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "an apply that draws for only some of a generator's destinations must be refused")

		var unreachable apimodel.FormaGeneratorDestinationsUnreachableError
		require.ErrorAs(t, err, &unreachable)
		require.Len(t, unreachable.Unreachable, 1)
		assert.Equal(t, "beta", unreachable.Unreachable[0].Label,
			"the refusal must name the destination it cannot reach")
		assert.Equal(t, f.otherStack, unreachable.Unreachable[0].Stack)
		assert.Equal(t, "FakeAWS::SecretsManager::Secret", unreachable.Unreachable[0].Type)
		assert.Equal(t, "db-password", unreachable.Unreachable[0].GeneratorLabel)
		assert.Equal(t, f.generatorStack, unreachable.Unreachable[0].GeneratorStack)

		refusedGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		assert.Equal(t, firstGeneration.GenerationID, refusedGeneration.GenerationID,
			"the refusal must land before anything is drawn")

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, cmds, 1, "a refused apply must persist no command")
	})
}

// Simulation is refused on the same terms. The neighbouring cascade aborts let
// a simulation through so the CLI can render the cascade and the operator can
// elevate on confirmation; no confirmation makes a split draw correct, so
// rendering a plan that can never be applied would be worse than the refusal.
func TestApplyForma_GeneratorBoundSecret_PartialApplySimulationIsRefused(t *testing.T) {
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

		_, err = m.ApplyForma(f.generatorStackOnly(t, 40, "before"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "test-client-id", "", "")
		require.Error(t, err, "a simulation of a split draw must be refused, not rendered")

		var unreachable apimodel.FormaGeneratorDestinationsUnreachableError
		require.ErrorAs(t, err, &unreachable)
		require.Len(t, unreachable.Unreachable, 1)
		assert.Equal(t, "beta", unreachable.Unreachable[0].Label)
	})
}

// A generator whose in-command destinations are all stable draws nothing, so
// there is nothing a destination outside the command can fall behind on and
// the partial apply proceeds. Without this the refusal would fail every
// partial apply of a generator-bound stack.
func TestApplyForma_GeneratorBoundSecret_PartialApplyThatDrawsNothingIsAllowed(t *testing.T) {
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

		// The generator's spec is untouched and only a field beside the
		// binding moves, so alpha is planned with a stable occurrence and no
		// draw happens.
		_, err = m.ApplyForma(f.generatorStackOnly(t, 24, "after"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "a partial apply that draws nothing must not be refused")
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 2, "the edit must produce a command of its own")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", f.generatorStack)
		require.NoError(t, err)
		assert.Equal(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"an apply that plans only stable bindings must not draw")
		assert.Len(t, delivered.updatedWith("alpha"), 1, "the unrelated edit must still reach the provider")
		assert.Empty(t, delivered.updatedWith("beta"), "the untouched stack must be left alone")
	})
}

// A destination the command is tearing down is reached by the command; it is
// simply owed nothing. It must not count as unreachable, or removing a
// consumer of a generator whose spec also moved would be impossible.
func TestApplyForma_GeneratorBoundSecret_DestinationBeingDeletedDoesNotBlockADraw(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		formaWith := func(length int, resources ...pkgmodel.Resource) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:  []pkgmodel.Stack{{Label: stack}},
				Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: stack,
					Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				})},
				Resources: resources,
			}
		}
		alpha := crossStackGenBoundSecret(stack, "alpha", stack, "db-password", "alpha")
		beta := crossStackGenBoundSecret(stack, "beta", stack, "db-password", "beta")

		_, err = m.ApplyForma(formaWith(24, alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		require.Len(t, delivered.createdWith("beta"), 1)

		// The spec moves (so alpha draws) and beta is dropped from the forma,
		// which a reconcile turns into a delete.
		_, err = m.ApplyForma(formaWith(40, alpha),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "a destination present in the command as a delete is reached, not unreachable")
		waitForApplyComplete(t, m)

		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1, "the surviving destination must receive the redraw")
		assert.Len(t, alphaUpdates[0], 40, "the value must be drawn under the edited spec")

		remaining, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		for i := range remaining {
			assert.NotEqual(t, "beta", remaining[i].Label, "the dropped destination must be gone")
		}
	})
}

// literalSecret is the same secret with a plain authored value in place of the
// $gen envelope: the shape a forma takes once an operator unbinds a
// destination from its generator.
func literalSecret(stack, label, value string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:  label,
		Type:   "FakeAWS::SecretsManager::Secret",
		Stack:  stack,
		Target: "test-target",
		Schema: secretSchema(),
		Properties: json.RawMessage(`{
			"Name": "` + label + `",
			"Description": "` + label + `",
			"SecretString": "` + value + `"
		}`)}
}

// A destination the command unbinds from the generator is reached by that
// command: its update is a node in the changeset, whatever its desired
// document now says about the binding. Reach is a property of the graph, not
// of the document, so an apply that both unbinds one destination and makes the
// generator draw for another must go through.
func TestApplyForma_GeneratorBoundSecret_UnbindingADestinationWhileDrawingIsAllowed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		formaWith := func(length int, resources ...pkgmodel.Resource) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:  []pkgmodel.Stack{{Label: stack}},
				Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: stack,
					Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				})},
				Resources: resources,
			}
		}
		boundAlpha := crossStackGenBoundSecret(stack, "alpha", stack, "db-password", "alpha")
		beta := crossStackGenBoundSecret(stack, "beta", stack, "db-password", "beta")

		_, err = m.ApplyForma(formaWith(24, boundAlpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		require.Len(t, delivered.createdWith("alpha"), 1)
		require.Len(t, delivered.createdWith("beta"), 1)

		// alpha's binding is replaced by a literal while the generator's spec
		// moves, so the generator draws for beta and alpha is planned without
		// binding it any more.
		_, err = m.ApplyForma(formaWith(40, literalSecret(stack, "alpha", "unbound-literal"), beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "unbinding a destination must not make it unreachable")
		waitForApplyComplete(t, m)

		betaUpdates := delivered.updatedWith("beta")
		require.Len(t, betaUpdates, 1, "the destination that kept its binding must receive the draw")
		assert.Len(t, betaUpdates[0], 40, "the new value must be drawn under the edited spec")

		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1, "the unbound destination must be written")
		assert.NotEqual(t, betaUpdates[0], alphaUpdates[0],
			"an unbound destination must not receive the generator's value")

		unbound := storedBinding(t, m, stack, "alpha")
		assert.False(t, unbound.Get("$gen").Exists(),
			"the unbound row must no longer carry the binding")
	})
}
