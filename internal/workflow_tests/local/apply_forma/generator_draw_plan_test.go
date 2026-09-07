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

// A draw is a credential rotation: every destination bound to the generator
// takes the new value, including the ones whose forma text nobody touched.
// These tests pin what a simulate says about that before it happens. The plan
// is the only thing an operator sees between deciding to apply and the
// credential changing, so a draw that the plan does not name is a rotation
// nobody agreed to.
//
// The other half is the same statement in reverse: a plan that names a draw on
// every apply tells an operator nothing, so an ordinary apply beside a
// generator whose binding is stable must show no draw at all.

// generatorUpdatesWithOperation returns the projected generator updates
// carrying one operation, so an assertion can name the operation it is about
// rather than counting a mixed list.
func generatorUpdatesWithOperation(updates []apimodel.GeneratorUpdate, operation string) []apimodel.GeneratorUpdate {
	var matching []apimodel.GeneratorUpdate
	for _, gu := range updates {
		if gu.Operation == operation {
			matching = append(matching, gu)
		}
	}
	return matching
}

// A generator whose spec nobody edited still draws when a destination is added
// to it, and the simulate has to say so. Nothing about the generator's own row
// is changing here, so the declared generator diff is empty and the draw is the
// only generator work in the command.
func TestApplyForma_Simulate_ShowsAnImpendingDraw(t *testing.T) {
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

		res, err := m.ApplyForma(forma(alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		require.True(t, res.Simulation.ChangesRequired)

		draws := generatorUpdatesWithOperation(res.Simulation.Command.GeneratorUpdates, "draw")
		require.Len(t, draws, 1, "the draw the apply will make must appear in the plan")
		assert.Equal(t, "db-password", draws[0].GeneratorLabel)
		assert.Equal(t, "password", draws[0].GeneratorType)
		assert.Equal(t, stack, draws[0].StackLabel)
		assert.Empty(t, generatorUpdatesWithOperation(res.Simulation.Command.GeneratorUpdates, "update"),
			"nothing about the generator's declared spec is changing")
	})
}

// A generator can be edited and draw in the same command: its spec moves and a
// destination is added beside it. The two are separate pieces of work with
// separate consequences — one rewrites the generator's row, the other rotates
// the credential every destination holds — so the plan carries them as two
// entries, each naming its own operation.
func TestApplyForma_Simulate_ShowsADeclaredGeneratorChangeAndItsDrawSeparately(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := func(length int, resources ...pkgmodel.Resource) *pkgmodel.Forma {
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
		alpha := genBoundSecret(stack, "alpha", "db-password", "value")
		beta := genBoundSecret(stack, "beta", "db-password", "value")

		_, err = m.ApplyForma(forma(24, alpha),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		res, err := m.ApplyForma(forma(32, alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		require.True(t, res.Simulation.ChangesRequired)

		updates := generatorUpdatesWithOperation(res.Simulation.Command.GeneratorUpdates, "update")
		require.Len(t, updates, 1, "the edited generator's own row change must appear in the plan")
		assert.Equal(t, "db-password", updates[0].GeneratorLabel)
		assert.Equal(t, stack, updates[0].StackLabel)
		assert.NotEmpty(t, updates[0].GeneratorConfig, "an edited generator's plan entry carries its diff")
		assert.NotEmpty(t, updates[0].OldGeneratorConfig, "an edited generator's plan entry carries its diff")

		draws := generatorUpdatesWithOperation(res.Simulation.Command.GeneratorUpdates, "draw")
		require.Len(t, draws, 1, "the draw is work of its own and must not be folded into the row change")
		assert.Equal(t, "db-password", draws[0].GeneratorLabel)
		assert.Equal(t, "password", draws[0].GeneratorType)
		assert.Equal(t, stack, draws[0].StackLabel)

		// The desired spec projected beside the draw belongs to a generator
		// carrying its KSUID, so this is where a marshalled config could put
		// identity into the plan. It must not: a generator is named to a
		// reader by label, type and stack.
		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identity.ID)
		require.NotEmpty(t, identity.GenerationID)

		projectedGenerators, err := json.Marshal(res.Simulation.Command.GeneratorUpdates)
		require.NoError(t, err)
		assert.NotContains(t, string(projectedGenerators), identity.ID,
			"a generator's KSUID must not reach the plan through a marshalled config")
		assert.NotContains(t, string(projectedGenerators), identity.GenerationID,
			"the generation a generator holds must not reach the plan through a marshalled config")
	})
}

// An apply that touches a field beside a generator binding draws nothing: the
// binding is stable and the credential stands still. The plan must say that by
// showing no draw, or an operator reading it can never tell a rotation from an
// ordinary edit.
func TestApplyForma_Simulate_ShowsNoDrawWhenTheBindingIsStable(t *testing.T) {
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

		res, err := m.ApplyForma(forma("after"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)

		require.True(t, res.Simulation.ChangesRequired, "the edited destination is real work")
		require.NotEmpty(t, res.Simulation.Command.ResourceUpdates, "the edited destination is real work")
		assert.Empty(t, generatorUpdatesWithOperation(res.Simulation.Command.GeneratorUpdates, "draw"),
			"an edit beside a stable binding rotates nothing, and the plan must not say it does")
	})
}

// The plan carries what a person needs to decide and no generator material:
// no credential in the response at all, and no generator identity in the
// generator entries, which describe the work by the generator's label, type
// and stack — the names a forma author writes — and never by its KSUID.
//
// No value has been drawn when a simulate is built, so the value assertion
// holds structurally rather than by filtering; the credential its destinations
// already hold is real and in the datastore while this simulate is built, and
// that is what it is checked against. The identity assertion is scoped to the
// generator entries because a translated $gen envelope carries the generator
// KSUID in the bound resource's own properties, which is the resource
// projection's shape and not this one's.
func TestApplyForma_Simulate_DrawProjectionCarriesNoGeneratorMaterial(t *testing.T) {
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

		alphaCreates := delivered.createdWith("alpha")
		require.Len(t, alphaCreates, 1)
		liveValue := alphaCreates[0]
		require.NotEmpty(t, liveValue)

		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identity.ID)
		require.NotEmpty(t, identity.GenerationID)

		res, err := m.ApplyForma(forma(alpha, beta),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)
		require.Len(t, generatorUpdatesWithOperation(res.Simulation.Command.GeneratorUpdates, "draw"), 1)

		projected, err := json.Marshal(res)
		require.NoError(t, err)
		assert.NotContains(t, string(projected), liveValue,
			"a plan must never carry the credential its destinations hold")

		projectedGenerators, err := json.Marshal(res.Simulation.Command.GeneratorUpdates)
		require.NoError(t, err)
		assert.NotContains(t, string(projectedGenerators), identity.ID,
			"a generator's KSUID is controller state and never names it in the plan")
		assert.NotContains(t, string(projectedGenerators), identity.GenerationID,
			"the generation a generator holds is controller state and never reaches the plan")
	})
}
