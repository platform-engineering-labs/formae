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
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// assertAuthoredGenEnvelope requires that env is the envelope an author
// writes and nothing more: the generator named by label and stack, and none
// of the internal parts a translated envelope at rest carries. Each absence
// is asserted on its own, because an envelope that names its generator
// correctly AND carries the stored digest alongside it is still a digest
// written into a file the operator commits.
func assertAuthoredGenEnvelope(t *testing.T, env gjson.Result, generatorLabel, generatorStack, output string) {
	t.Helper()
	require.True(t, env.IsObject(), "the property must still be a $gen envelope, got %s", env.Raw)
	assert.True(t, env.Get("$gen").Bool(), "$gen marker")
	assert.Equal(t, generatorLabel, env.Get("$label").String(), "$label names the generator")
	assert.Equal(t, generatorStack, env.Get("$stack").String(), "$stack names the generator's stack")
	assert.Equal(t, output, env.Get("$output").String(), "$output")
	assert.Equal(t, "Opaque", env.Get("$visibility").String(), "$visibility")

	assert.False(t, env.Get("$generator").Exists(), "$generator KSUID must not reach authored output")
	assert.False(t, env.Get("$value").Exists(), "$value digest must not reach authored output")
	assert.False(t, env.Get("$hashed").Exists(), "$hashed must not reach authored output")
	assert.False(t, env.Get("$resolvedFrom").Exists(), "$resolvedFrom must not reach authored output")
	assert.False(t, env.Get("$strategy").Exists(), "$strategy must not reach authored output")
}

// applyGeneratorBoundForma applies forma and blocks until it is done, so each
// test in this file starts from live state rather than a plan.
func applyGeneratorBoundForma(t *testing.T, m *metastructure.Metastructure, forma *pkgmodel.Forma) {
	t.Helper()
	_, err := m.ApplyForma(forma,
		&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		"test-client-id", "", "")
	require.NoError(t, err)
	waitForApplyComplete(t, m)
}

// newExtractTestMetastructure builds a metastructure with synchronization off,
// so nothing rewrites the resource rows between the apply and the extract.
func newExtractTestMetastructure(t *testing.T) *metastructure.Metastructure {
	t.Helper()
	cfg := test_helpers.NewTestMetastructureConfig()
	cfg.Agent.Synchronization.Enabled = false
	m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
	t.Cleanup(def)
	require.NoError(t, err)
	return m
}

// A generator-bound property at rest names its generator by KSUID and carries
// the digest of the drawn value and the provenance it was resolved under.
// None of that is authorable, so extraction has to hand back the envelope the
// author wrote: the generator named by label and stack, and nothing else.
func TestExtractResources_GeneratorBoundSecret_EmitsTheAuthoredEnvelope(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m := newExtractTestMetastructure(t)

		stack := "test-stack-" + util.NewID()
		applyGeneratorBoundForma(t, m, &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
		})

		extracted, err := m.ExtractResources("stack:" + stack)
		require.NoError(t, err)
		require.NotNil(t, extracted)
		require.Len(t, extracted.Resources, 1)

		env := gjson.GetBytes(extracted.Resources[0].Properties, "SecretString")
		assertAuthoredGenEnvelope(t, env, "db-password", stack, "value")
	})
}

// A forma that references a generator has to declare it, or the file it is
// written to cannot be applied on its own.
func TestExtractResources_GeneratorBoundSecret_DeclaresTheGenerator(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m := newExtractTestMetastructure(t)

		stack := "test-stack-" + util.NewID()
		applyGeneratorBoundForma(t, m, &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
		})

		extracted, err := m.ExtractResources("stack:" + stack)
		require.NoError(t, err)
		require.NotNil(t, extracted)
		require.Len(t, extracted.Generators, 1, "the referenced generator must be declared")

		declared, err := pkgmodel.ParseGenerator(extracted.Generators[0])
		require.NoError(t, err)
		assert.Equal(t, "db-password", declared.GetLabel())
		assert.Equal(t, stack, declared.GetStack())
		assert.Equal(t, "password", declared.GetType())
		pw, ok := declared.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, 24, pw.Length, "the declared spec must be the live one")
	})
}

// A generator belongs to one stack and is meant to be bound from others. The
// extract of the destination's stack therefore has to reach a generator no
// resource in the query result sits beside, and emit both the generator and
// the stack it names, or the declaration references a stack that is not there.
func TestExtractResources_CrossStackGeneratorBinding_EmitsTheGeneratorAndItsStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m := newExtractTestMetastructure(t)

		generatorStack := "gen-stack-" + util.NewID()
		resourceStack := "res-stack-" + util.NewID()
		applyGeneratorBoundForma(t, m, &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: generatorStack}, {Label: resourceStack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", generatorStack)},
			Resources: []pkgmodel.Resource{
				crossStackGenBoundSecret(resourceStack, "alpha", generatorStack, "db-password", "cross-stack destination"),
			},
		})

		extracted, err := m.ExtractResources("stack:" + resourceStack)
		require.NoError(t, err)
		require.NotNil(t, extracted)
		require.Len(t, extracted.Resources, 1)

		env := gjson.GetBytes(extracted.Resources[0].Properties, "SecretString")
		assertAuthoredGenEnvelope(t, env, "db-password", generatorStack, "value")

		require.Len(t, extracted.Generators, 1, "the cross-stack generator must be declared")
		declared, err := pkgmodel.ParseGenerator(extracted.Generators[0])
		require.NoError(t, err)
		assert.Equal(t, generatorStack, declared.GetStack())

		stackLabels := make([]string, 0, len(extracted.Stacks))
		for _, s := range extracted.Stacks {
			stackLabels = append(stackLabels, s.Label)
		}
		assert.Contains(t, stackLabels, resourceStack, "the destination's stack")
		assert.Contains(t, stackLabels, generatorStack,
			"the generator's own stack must be emitted, or its declaration names a stack that is not there")
	})
}

// The point of an extract is a forma that can be applied again. A
// generator-bound resource is the case where that had stopped being true, so
// the extracted forma is applied back: it must plan nothing, and then a real
// apply of it must leave the drawn value where it was.
//
// The second half is what pins the authored envelope. Its generator is named
// by label and stack, so an apply has to resolve that pair back to the
// generator the value was drawn from; a pair naming no live generator is
// refused, and one naming a different generator draws again and moves the
// digest.
func TestExtractResources_GeneratorBoundSecret_ExtractedFormaReappliesWithNoPlan(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m := newExtractTestMetastructure(t)

		stack := "test-stack-" + util.NewID()
		applyGeneratorBoundForma(t, m, &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
		})

		digestBefore := storedGenDigest(t, m, stack)

		extracted, err := m.ExtractResources("stack:" + stack)
		require.NoError(t, err)
		require.NotNil(t, extracted)

		resp, err := m.ApplyForma(extracted,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err, "the extracted forma must be appliable")
		require.NotNil(t, resp.Simulation)
		assert.False(t, resp.Simulation.ChangesRequired,
			"re-applying the extracted forma must plan no changes")
		assert.Empty(t, resp.Simulation.Command.ResourceUpdates,
			"re-applying the extracted forma must plan no resource updates")

		applyGeneratorBoundForma(t, m, extracted)

		commands, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		for _, command := range commands {
			assert.NotEqual(t, forma_command.CommandStateFailed, command.State,
				"applying the extracted forma must not fail")
		}
		assert.Equal(t, digestBefore, storedGenDigest(t, m, stack),
			"the extracted envelope must resolve back to the generator the value was drawn from")
	})
}

// storedGenDigest reads the digest the generator-bound property holds at rest
// on the single resource of a stack. A redraw moves it, so it is what says
// whether the value a destination holds is still the one it was given.
func storedGenDigest(t *testing.T, m *metastructure.Metastructure, stack string) string {
	t.Helper()
	resources, err := m.Datastore.LoadResourcesByStack(stack)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	digest := gjson.GetBytes(resources[0].Properties, "SecretString.$value").String()
	require.NotEmpty(t, digest, "the stored envelope must carry the drawn value's digest")
	return digest
}

// Extraction cannot know where a value came from, and a binding is the
// author's deliberate act. An opaque literal that merely happens to hold a
// generated value is emitted as the opaque literal it is.
func TestExtractResources_OpaqueLiteralSecret_InventsNoGeneratorBinding(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m := newExtractTestMetastructure(t)

		stack := "test-stack-" + util.NewID()
		applyGeneratorBoundForma(t, m, &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: stack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{{
				Label:      "literal",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stack,
				Target:     "test-target",
				Schema:     secretSchema(),
				Properties: json.RawMessage(`{"Name":"literal","SecretString":"TestPassword123!"}`),
			}},
		})

		extracted, err := m.ExtractResources("stack:" + stack)
		require.NoError(t, err)
		require.NotNil(t, extracted)
		require.Len(t, extracted.Resources, 1)

		env := gjson.GetBytes(extracted.Resources[0].Properties, "SecretString")
		assert.False(t, env.Get("$gen").Exists(), "no $gen binding may be invented")
		assert.False(t, env.Get("$label").Exists(), "no generator may be named")
		assert.True(t, env.Get("$value").Exists(), "the opaque literal must be left as it is")
		assert.Empty(t, extracted.Generators, "no generator may be declared for a literal")
	})
}

// The single-resource extract reads the same stored rows through its own
// entry point, so it carries the same envelope and must author it the same
// way.
func TestExtractResourceByKsuid_GeneratorBoundSecret_EmitsTheAuthoredEnvelope(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m := newExtractTestMetastructure(t)

		stack := "test-stack-" + util.NewID()
		applyGeneratorBoundForma(t, m, &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
		})

		stored, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.NotEmpty(t, stored[0].Ksuid)

		one, err := m.ExtractResourceByKsuid(stored[0].Ksuid)
		require.NoError(t, err)
		require.NotNil(t, one)

		env := gjson.GetBytes(one.Properties, "SecretString")
		assertAuthoredGenEnvelope(t, env, "db-password", stack, "value")
	})
}
