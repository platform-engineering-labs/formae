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

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A generator belongs to one stack and is meant to be referenced from others,
// so deleting one is a cross-stack event: every resource that binds a property
// to it loses the only thing that can give that property a value. These tests
// pin what happens when a delete of a generator's stack meets a resource
// elsewhere that still binds it, under both --on-dependents settings, and pin
// that the generator's row actually goes when the delete is allowed.

// generatorDestroyFixture is the shape these tests start from: a generator and
// one destination in its own stack, and a second destination in another stack.
type generatorDestroyFixture struct {
	generatorStack string
	otherStack     string
	generator      json.RawMessage
}

func newGeneratorDestroyFixture(t *testing.T) generatorDestroyFixture {
	t.Helper()
	id := util.NewID()
	generatorStack := "gen-destroy-stack-" + id
	return generatorDestroyFixture{
		generatorStack: generatorStack,
		otherStack:     "gen-destroy-other-" + id,
		generator: rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: generatorStack,
			Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		}),
	}
}

func (f generatorDestroyFixture) targets() []pkgmodel.Target {
	return []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}
}

// bothStacks declares the generator, a destination beside it and a
// destination in another stack.
func (f generatorDestroyFixture) bothStacks() *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: f.generatorStack}, {Label: f.otherStack}},
		Targets:    f.targets(),
		Generators: []json.RawMessage{f.generator},
		Resources: []pkgmodel.Resource{
			crossStackGenBoundSecret(f.generatorStack, "alpha", f.generatorStack, "db-password", "alpha"),
			crossStackGenBoundSecret(f.otherStack, "beta", f.generatorStack, "db-password", "beta"),
		},
	}
}

// generatorStackOnly is the destroy input: it names the generator's stack and
// everything in it, and nothing of the other stack.
func (f generatorDestroyFixture) generatorStackOnly() *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: f.generatorStack}},
		Targets:    f.targets(),
		Generators: []json.RawMessage{f.generator},
		Resources: []pkgmodel.Resource{
			crossStackGenBoundSecret(f.generatorStack, "alpha", f.generatorStack, "db-password", "alpha"),
		},
	}
}

// generatorKsuid is the generator's stable identity, read while its stack is
// still alive. Every "is the row gone" assertion has to be made against this
// and never against the label: GetGenerator resolves the stack label to a
// stack id first, so once the stack is tombstoned it answers nil for a
// generator whose row is still very much there.
func generatorKsuid(t *testing.T, m *metastructure.Metastructure, label, stackLabel string) string {
	t.Helper()
	identity, err := m.Datastore.GetGeneratorIdentity(label, stackLabel)
	require.NoError(t, err)
	require.NotEmpty(t, identity.ID, "the generator must be persisted before its identity is read")
	return identity.ID
}

// requireGeneratorRowGone asserts the generator's row is tombstoned, by id.
func requireGeneratorRowGone(t *testing.T, m *metastructure.Metastructure, ksuid string) {
	t.Helper()
	identity, err := m.Datastore.GetGeneratorIdentityByID(ksuid)
	require.NoError(t, err)
	assert.Empty(t, identity.ID,
		"the generator row must be tombstoned, not left behind for a surviving reference to resolve against")
}

func newGeneratorDestroyMetastructure(t *testing.T) (*metastructure.Metastructure, *deliveredValues, func()) {
	t.Helper()
	delivered := newDeliveredValues()
	cfg := test_helpers.NewTestMetastructureConfig()
	cfg.Agent.Synchronization.Enabled = false
	m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
	require.NoError(t, err)
	return m, delivered, def
}

// Destroying the stack that owns a generator, while a resource in another
// stack still binds it, is refused under the default --on-dependents=abort,
// and the refusal names the referencing resource.
func TestDestroyForma_GeneratorBoundFromAnotherStack_IsRefusedUnderAbort(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		f := newGeneratorDestroyFixture(t)
		_, err := m.ApplyForma(f.bothStacks(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", f.generatorStack)

		_, err = m.DestroyForma(f.generatorStackOnly(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "destroying a generator's stack while another stack binds it must be refused")

		var dependents apimodel.FormaGeneratorHasDependentsError
		require.ErrorAs(t, err, &dependents)
		require.Len(t, dependents.Dependents, 1)
		assert.Equal(t, "beta", dependents.Dependents[0].ResourceLabel,
			"the refusal must name the resource that still binds the generator")
		assert.Equal(t, f.otherStack, dependents.Dependents[0].Stack)
		assert.Equal(t, "FakeAWS::SecretsManager::Secret", dependents.Dependents[0].ResourceType)
		assert.Equal(t, "db-password", dependents.Dependents[0].GeneratorLabel)
		assert.Equal(t, f.generatorStack, dependents.Dependents[0].GeneratorStack)

		identity, err := m.Datastore.GetGeneratorIdentityByID(ksuid)
		require.NoError(t, err)
		assert.Equal(t, ksuid, identity.ID, "a refused destroy must leave the generator in place")

		commands, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, commands, 1, "a refused destroy must persist no command")
	})
}

// Under --on-dependents=cascade the referencing resource is destroyed
// alongside the generator's stack, and the generator's row goes with it.
func TestDestroyForma_GeneratorBoundFromAnotherStack_CascadeDestroysTheDependentAndTheGenerator(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		f := newGeneratorDestroyFixture(t)
		_, err := m.ApplyForma(f.bothStacks(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", f.generatorStack)

		_, err = m.DestroyForma(f.generatorStackOnly(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, OnDependents: "cascade"},
			"test-client-id", "", "")
		require.NoError(t, err, "cascade must let the destroy through")
		waitForApplyComplete(t, m)

		requireGeneratorRowGone(t, m, ksuid)

		remaining, err := m.Datastore.LoadResourcesByStack(f.otherStack)
		require.NoError(t, err)
		assert.Empty(t, remaining, "the resource that bound the generator must be destroyed with it")

		bound, err := m.Datastore.FindResourcesReferencingGenerator(ksuid)
		require.NoError(t, err)
		assert.Empty(t, bound, "nothing may be left binding a destroyed generator")
	})
}

// A generator and its only destination in one stack, destroyed together, must
// succeed: the destination is going with the generator, so there is nothing
// left to dangle. Without this a self-contained stack could not be destroyed
// at all.
func TestDestroyForma_GeneratorAndItsDestinationInOneStack_AreDestroyedTogether(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		id := util.NewID()
		stack := "gen-same-stack-" + id
		generator := rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: stack,
			Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		})
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{generator},
			Resources: []pkgmodel.Resource{
				crossStackGenBoundSecret(stack, "alpha", stack, "db-password", "alpha"),
			},
		}

		_, err := m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", stack)

		_, err = m.DestroyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "a stack holding both a generator and its only destination must destroy cleanly")
		waitForApplyComplete(t, m)

		requireGeneratorRowGone(t, m, ksuid)
	})
}

// A generator nothing binds is destroyed with its stack under the default.
func TestDestroyForma_GeneratorWithNoDestinations_IsDestroyedUnderTheDefault(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		id := util.NewID()
		stack := "gen-unbound-stack-" + id
		generator := rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: stack,
			Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		})
		// A stack cannot be created empty, so the generator needs an anchor
		// resource beside it. It binds nothing.
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{generator},
			Resources: []pkgmodel.Resource{{
				Label:      "anchor",
				Type:       "FakeAWS::Versioned::Parent",
				Stack:      stack,
				Target:     "test-target",
				Properties: json.RawMessage(`{"Name": "anchor", "Value": "v1"}`),
			}},
		}

		_, err := m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", stack)

		_, err = m.DestroyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "a generator nothing binds must be destroyed without an opt-in")
		waitForApplyComplete(t, m)

		requireGeneratorRowGone(t, m, ksuid)
	})
}

// Simulating the refused destroy renders the cascade instead of refusing, so
// the CLI can show the operator what would go and elevate to cascade on
// confirmation. The cascade delete of the dependent is marked as a cascade and
// names the generator as its source, which is what the CLI reads to decide it
// has a cascade to confirm.
func TestDestroyForma_GeneratorBoundFromAnotherStack_SimulationSurfacesTheCascade(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		f := newGeneratorDestroyFixture(t)
		_, err := m.ApplyForma(f.bothStacks(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", f.generatorStack)

		res, err := m.DestroyForma(f.generatorStackOnly(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err, "a simulation must render the cascade rather than refuse it")
		require.NotNil(t, res)
		assert.True(t, res.Simulation.ChangesRequired)

		var cascade *apimodel.ResourceUpdate
		for i := range res.Simulation.Command.ResourceUpdates {
			if res.Simulation.Command.ResourceUpdates[i].ResourceLabel == "beta" {
				cascade = &res.Simulation.Command.ResourceUpdates[i]
			}
		}
		require.NotNil(t, cascade, "the simulation must show the dependent being torn down")
		assert.True(t, cascade.IsCascade, "the dependent's delete must be marked a cascade")
		assert.Equal(t, "db-password", cascade.CascadeSource,
			"the cascade must name the generator it follows from")

		identity, err := m.Datastore.GetGeneratorIdentityByID(ksuid)
		require.NoError(t, err)
		assert.Equal(t, ksuid, identity.ID, "a simulation must change nothing")
	})
}

// The query destroy reaches the same conclusion. It is the common CLI path and
// it never carries a generators block — the forma is synthesized from the rows
// the query matched — so the generators a query destroy takes with it can only
// come from the resource deletes, which is where they do come from.
//
// Its resources are declared managed because DestroyByQuery drops unmanaged
// rows before it builds the forma.
func TestDestroyByQuery_GeneratorBoundFromAnotherStack_RefusesThenCascades(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		id := util.NewID()
		generatorStack := "gen-query-stack-" + id
		otherStack := "gen-query-other-" + id
		generator := rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: generatorStack,
			Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		})
		managed := func(stack, label string) pkgmodel.Resource {
			r := crossStackGenBoundSecret(stack, label, generatorStack, "db-password", label)
			r.Managed = true
			return r
		}

		_, err := m.ApplyForma(&pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: generatorStack}, {Label: otherStack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{generator},
			Resources: []pkgmodel.Resource{
				managed(generatorStack, "alpha"),
				managed(otherStack, "beta"),
			},
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", generatorStack)

		_, err = m.DestroyByQuery("stack:"+generatorStack,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "a query destroy of the generator's stack must be refused too")

		var dependents apimodel.FormaGeneratorHasDependentsError
		require.ErrorAs(t, err, &dependents)
		require.Len(t, dependents.Dependents, 1)
		assert.Equal(t, "beta", dependents.Dependents[0].ResourceLabel)
		assert.Equal(t, otherStack, dependents.Dependents[0].Stack)

		_, err = m.DestroyByQuery("stack:"+generatorStack,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, OnDependents: "cascade"},
			"test-client-id", "", "")
		require.NoError(t, err, "cascade must let the query destroy through")
		waitForApplyComplete(t, m)

		requireGeneratorRowGone(t, m, ksuid)

		remaining, err := m.Datastore.LoadResourcesByStack(otherStack)
		require.NoError(t, err)
		assert.Empty(t, remaining, "the resource that bound the generator must be destroyed with it")
	})
}

// An ordinary reconcile that drops a generator declaration is a delete too,
// and it is refused on the same terms while a resource still binds it. The
// resource here is declared and unchanged, so the command plans nothing for
// it: its stored binding is what would be left pointing at nothing.
func TestApplyForma_ReconcileDroppingABoundGenerator_IsRefused(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		f := newGeneratorDestroyFixture(t)
		_, err := m.ApplyForma(f.bothStacks(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", f.generatorStack)

		// The same forma with the generator declaration dropped. Under
		// reconcile that is a delete of the generator, while both
		// destinations stay declared and still bind it.
		withoutGenerator := f.bothStacks()
		withoutGenerator.Generators = nil

		_, err = m.ApplyForma(withoutGenerator,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "dropping a generator a live resource still binds must be refused")

		var dependents apimodel.FormaGeneratorHasDependentsError
		require.ErrorAs(t, err, &dependents)
		labels := make([]string, 0, len(dependents.Dependents))
		for _, d := range dependents.Dependents {
			labels = append(labels, d.ResourceLabel)
		}
		assert.ElementsMatch(t, []string{"alpha", "beta"}, labels,
			"the refusal must name every resource left binding the dropped generator")

		identity, err := m.Datastore.GetGeneratorIdentityByID(ksuid)
		require.NoError(t, err)
		assert.Equal(t, ksuid, identity.ID, "a refused apply must leave the generator in place")
	})
}

// Dropping a generator declaration together with every resource that binds it
// is the consistent counterpart of destroying a self-contained stack: nothing
// is left to dangle, so the reconcile proceeds and the row goes.
func TestApplyForma_ReconcileDroppingAGeneratorAndItsDestinations_Succeeds(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, _, def := newGeneratorDestroyMetastructure(t)
		defer def()

		id := util.NewID()
		stack := "gen-drop-stack-" + id
		generator := rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
			Label: "db-password", Stack: stack,
			Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		})
		anchor := pkgmodel.Resource{
			Label:      "anchor",
			Type:       "FakeAWS::Versioned::Parent",
			Stack:      stack,
			Target:     "test-target",
			Properties: json.RawMessage(`{"Name": "anchor", "Value": "v1"}`),
		}

		_, err := m.ApplyForma(&pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{generator},
			Resources: []pkgmodel.Resource{
				anchor,
				crossStackGenBoundSecret(stack, "alpha", stack, "db-password", "alpha"),
			},
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		ksuid := generatorKsuid(t, m, "db-password", stack)

		// The generator and its destination both go; the anchor keeps the
		// stack alive so this is a generator delete and not a stack teardown.
		_, err = m.ApplyForma(&pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{anchor},
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "dropping a generator and its only destination together must be allowed")
		waitForApplyComplete(t, m)

		requireGeneratorRowGone(t, m, ksuid)
	})
}
