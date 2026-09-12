//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package workflow_tests_local

import (
	"encoding/json"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestDesiredExtraction_GeneratorExternalOwnerRoundTrip(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		store := newReferencingSecretStore(true)
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, store.overrides(), cfg)
		require.NoError(t, err)
		t.Cleanup(cleanup)
		owner := "owner-" + util.NewID()
		consumer := "consumer-" + util.NewID()
		source := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: owner}, {Label: consumer}}, Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}, Generators: []json.RawMessage{passwordGeneratorFor(t, "credential", owner), passwordGeneratorFor(t, "unused", consumer)}, Resources: []pkgmodel.Resource{genBoundSecret(owner, "owner-secret", "credential", "value"), crossStackGenBoundSecret(consumer, "consumer-secret", owner, "credential", "before")}}
		applyGeneratorBoundForma(t, m, source)
		before := storedGenDigest(t, m, consumer)
		ownerRows, err := m.Datastore.LoadResourcesByStack(owner)
		require.NoError(t, err)
		require.Len(t, ownerRows, 1)
		extracted, err := m.ExtractDesiredStacks("stack:" + consumer)
		require.NoError(t, err)
		require.Len(t, extracted.Stacks, 1)
		require.Len(t, extracted.Extraction.ReferenceGenerators, 1)
		root, err := filepath.Abs(".")
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "desired.pkl")
		options := &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: []string{"local:formae:" + filepath.Join(root, "internal/schema/pkl/schema/PklProject"), "local:fakeaws:" + filepath.Join(root, "internal/testplugin/fakeaws/schema/pkl/PklProject")}}
		_, err = pkl.PKL{}.GenerateSourceCode(extracted, path, nil, options)
		require.NoError(t, err)
		evaluated, err := pkl.PKL{}.Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
		require.NoError(t, err)
		result, err := m.ApplyForma(evaluated, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "", "")
		require.NoError(t, err)
		require.False(t, result.Simulation.ChangesRequired)
		require.Empty(t, result.Simulation.Command.ResourceUpdates)
		// An unrelated addition must leave the generator-bound resource and its owner intact.
		evaluated.Resources = append(evaluated.Resources, pkgmodel.Resource{Label: "new-secret", Stack: consumer, Target: "test-target", Type: "FakeAWS::SecretsManager::Secret", Schema: secretSchema(), Properties: json.RawMessage(`{"Name":"new-secret","Description":"unrelated"}`)})
		result, err = m.ApplyForma(evaluated, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "", "")
		require.NoError(t, err)
		require.Len(t, result.Simulation.Command.ResourceUpdates, 1)
		require.Equal(t, "new-secret", result.Simulation.Command.ResourceUpdates[0].ResourceLabel)
		require.Equal(t, before, storedGenDigest(t, m, consumer))
		afterRows, err := m.Datastore.LoadResourcesByStack(owner)
		require.NoError(t, err)
		require.Equal(t, ownerRows, afterRows)
		require.Empty(t, store.updatedWith("consumer-secret"))
	})
}

func TestDesiredExtraction_TransitiveOpaqueRoundTrip(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		store := newReferencingSecretStore(true)
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, cleanup, err := test_helpers.NewTestMetastructureWithConfig(t, store.overrides(), cfg)
		require.NoError(t, err)
		t.Cleanup(cleanup)
		stack := "chain-" + util.NewID()
		source := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: stack}}, Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}, Generators: []json.RawMessage{passwordGeneratorFor(t, "credential", stack)}, Resources: []pkgmodel.Resource{genBoundSecret(stack, "producer", "credential", "value")}}
		for _, binding := range []struct{ label, producer string }{{"direct", "producer"}, {"transitive", "direct"}} {
			props, err := json.Marshal(map[string]any{"Name": binding.label, "SecretString": map[string]any{"$res": true, "$label": binding.producer, "$stack": stack, "$type": "FakeAWS::SecretsManager::Secret", "$property": "SecretString", "$visibility": "Opaque"}})
			require.NoError(t, err)
			source.Resources = append(source.Resources, pkgmodel.Resource{Label: binding.label, Stack: stack, Target: "test-target", Type: "FakeAWS::SecretsManager::Secret", Schema: secretSchema(), Properties: props})
		}
		source.Resources = append(source.Resources, pkgmodel.Resource{Label: "literal", Stack: stack, Target: "test-target", Type: "FakeAWS::SecretsManager::Secret", Schema: secretSchema(), Properties: json.RawMessage(`{"Name":"literal","SecretString":"keep-this-secret"}`)})
		applyGeneratorBoundForma(t, m, source)
		before, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, before, 4)
		extracted, err := m.ExtractDesiredStacks("stack:" + stack)
		require.NoError(t, err)
		root, err := filepath.Abs(".")
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "desired.pkl")
		options := &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: []string{"local:formae:" + filepath.Join(root, "internal/schema/pkl/schema/PklProject"), "local:fakeaws:" + filepath.Join(root, "internal/testplugin/fakeaws/schema/pkl/PklProject")}}
		_, err = pkl.PKL{}.GenerateSourceCode(extracted, path, nil, options)
		require.NoError(t, err)
		evaluated, err := pkl.PKL{}.Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
		require.NoError(t, err)
		result, err := m.ApplyForma(evaluated, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "", "")
		require.NoError(t, err)
		require.False(t, result.Simulation.ChangesRequired)
		evaluated.Resources = append(evaluated.Resources, pkgmodel.Resource{Label: "unrelated", Stack: stack, Target: "test-target", Type: "FakeAWS::SecretsManager::Secret", Schema: secretSchema(), Properties: json.RawMessage(`{"Name":"unrelated"}`)})
		result, err = m.ApplyForma(evaluated, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "", "")
		require.NoError(t, err)
		require.Len(t, result.Simulation.Command.ResourceUpdates, 1)
		require.Equal(t, "unrelated", result.Simulation.Command.ResourceUpdates[0].ResourceLabel)
		after, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
}
