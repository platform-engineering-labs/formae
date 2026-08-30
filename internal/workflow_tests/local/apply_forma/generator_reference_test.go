// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// passwordGeneratorFor is the generator every test in this file declares: the
// only generator arm that exists, with a length the schema accepts.
func passwordGeneratorFor(t *testing.T, label, stack string) json.RawMessage {
	t.Helper()
	return rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
		Label: label, Stack: stack,
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	})
}

// genBoundSecret is a secret whose opaque SecretString is bound to a
// generator output through an authored $gen envelope (the shape PKL renders:
// label plus stack, never a KSUID).
func genBoundSecret(stack, label, generatorLabel, output string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:  label,
		Type:   "FakeAWS::SecretsManager::Secret",
		Stack:  stack,
		Target: "test-target",
		Schema: secretSchema(),
		Properties: json.RawMessage(`{
			"Name": "` + label + `",
			"SecretString": {
				"$gen":        true,
				"$label":      "` + generatorLabel + `",
				"$stack":      "` + stack + `",
				"$output":     "` + output + `",
				"$visibility": "Opaque"
			}
		}`),
	}
}

// Re-applying an identical forma whose secret is bound to a generator plans
// nothing. The binding must survive a second translation as the SAME
// generator identity: the envelope's $generator is the generator's own live
// KSUID, and it does not move between applies, so the occurrence's source
// identity is unchanged and no second command is created.
func TestApplyForma_GeneratorBoundSecret_IdenticalReapplyPlansNothing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		buildForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources:  []pkgmodel.Resource{genBoundSecret(stack, "db", "db-password", "value")},
			}
		}

		_, err = m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"precondition: the create apply must succeed")

		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identity.ID, "precondition: the declared generator must have a row")

		storedGenerator := func() string {
			resources, loadErr := m.Datastore.LoadResourcesByStack(stack)
			require.NoError(t, loadErr)
			for i := range resources {
				if resources[i].Label == "db" {
					return gjson.GetBytes(resources[i].Properties, "SecretString.$generator").String()
				}
			}
			return ""
		}
		require.Equal(t, identity.ID, storedGenerator(),
			"the stored envelope must name the generator's own KSUID")

		resp, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired,
			"an identical re-apply of a generator-bound secret must plan nothing")

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, cmds, 1, "no second command may be created")

		identityAfter, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, identity.ID, identityAfter.ID,
			"the generator's KSUID must be stable across applies")
		assert.Equal(t, identity.ID, storedGenerator(),
			"the re-apply must not repoint the binding at a freshly minted KSUID")
	})
}

// A $gen naming an output the generator does not produce is rejected out of
// ApplyForma as the typed reference error, naming the offending output, and
// nothing is persisted.
func TestApplyForma_GeneratorReference_UnknownOutputIsRejected(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "db", "db-password", "passphrase")},
		}

		resp, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "an output the generator does not produce must not be applied")
		assert.Nil(t, resp, "a rejected forma must not return a submission")

		var notFound apimodel.FormaReferencedGeneratorsNotFoundError
		require.ErrorAs(t, err, &notFound,
			"the rejection must reach the caller as the typed reference error")
		require.Len(t, notFound.Missing, 1)
		assert.Equal(t, "passphrase", notFound.Missing[0].Output)
		assert.Equal(t, "db-password", notFound.Missing[0].Label)
		assert.Equal(t, stack, notFound.Missing[0].Stack)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Empty(t, cmds, "a rejected forma must not persist a command")
		generators, err := m.Datastore.LoadGeneratorsByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, generators, "a rejected forma must not persist its generator either")
	})
}

// A $gen naming no live generator is a hard error out of ApplyForma, never
// silently absorbed: the reference resolves against neither the command's own
// declarations nor the datastore, and nothing is persisted.
func TestApplyForma_GeneratorReference_DanglingIsRejected(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		// The forma declares "db-password" and the secret binds to
		// "phantom-password": a generator no declaration and no row names.
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "db", "phantom-password", "value")},
		}

		resp, err := m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.Error(t, err, "a dangling generator reference must not be applied")
		assert.Nil(t, resp, "a rejected forma must not return a submission")

		var notFound apimodel.FormaReferencedGeneratorsNotFoundError
		require.ErrorAs(t, err, &notFound,
			"the rejection must reach the caller as the typed reference error")
		require.Len(t, notFound.Missing, 1)
		assert.Equal(t, "phantom-password", notFound.Missing[0].Label)
		assert.Equal(t, stack, notFound.Missing[0].Stack)
		assert.Equal(t, "value", notFound.Missing[0].Output)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Empty(t, cmds, "a rejected forma must not persist a command")
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, resources, "a rejected forma must not persist a partial plan")
	})
}

// A $ref consumer reading a secret that is itself generator-fed still
// resolves through the generator-bound source: the consumer is created, its
// plugin is called, and the stored envelope carries a well-formed
// current-domain resolution provenance digest rather than being dropped
// because the source's own value came from a generator.
func TestApplyForma_ConsumerDownstreamOfGeneratorFedSecret_StillPropagates(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var consumerUpdateCalls atomic.Int32
		var consumerUpdateProps atomic.Value
		base := secretConsumerOverrides(&consumerUpdateCalls, &consumerUpdateProps)
		var consumerCreateCalls atomic.Int32
		var consumerCreateProps atomic.Value
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				if req.ResourceType == "FakeAWS::S3::Bucket" {
					consumerCreateCalls.Add(1)
					consumerCreateProps.Store(append(json.RawMessage(nil), req.Properties...))
				}
				return base.Create(req)
			},
			Update: base.Update,
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources: []pkgmodel.Resource{
				genBoundSecret(stack, "my-secret", "db-password", "value"),
				secretConsumer(stack, "my-secret"),
			},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State,
			"a consumer downstream of a generator-fed secret must apply cleanly")

		plannedLabels := map[string]bool{}
		updates, err := m.Datastore.LoadResourceUpdates(createCmd.ID)
		require.NoError(t, err)
		for _, ru := range updates {
			plannedLabels[ru.DesiredState.Label] = true
		}
		assert.True(t, plannedLabels["my-secret"], "the generator-fed secret must be planned, got %v", plannedLabels)
		assert.True(t, plannedLabels["my-bucket"], "the downstream consumer must be planned, got %v", plannedLabels)

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		var consumerRow, secretRow *pkgmodel.Resource
		for i := range resources {
			switch resources[i].Label {
			case "my-bucket":
				consumerRow = resources[i]
			case "my-secret":
				secretRow = resources[i]
			}
		}
		require.NotNil(t, secretRow, "the generator-fed secret must be persisted")
		require.NotNil(t, consumerRow, "the downstream consumer must be persisted")

		// The secret's own binding survives at rest as a generator reference,
		// not as a flattened value.
		assert.True(t, gjson.GetBytes(secretRow.Properties, "SecretString.$gen").Bool(),
			"the source secret must keep its generator binding at rest, got %s", secretRow.Properties)

		// The consumer's binding survives as a resource reference at the
		// source secret's property, carrying resolution provenance.
		consumerRef := gjson.GetBytes(consumerRow.Properties, "DbPassword")
		assert.True(t, consumerRef.Get("$ref").Exists(),
			"the consumer must keep its reference envelope at rest, got %s", consumerRow.Properties)
		resolvedFrom := consumerRef.Get("$resolvedFrom").String()
		assert.True(t, provenance.Valid(resolvedFrom),
			"the consumer's occurrence must carry a current-domain provenance digest, got %q", resolvedFrom)

		require.Greater(t, consumerCreateCalls.Load(), int32(0),
			"the consumer plugin must be invoked, so the chain actually reached the provider")
		delivered, _ := consumerCreateProps.Load().(json.RawMessage)
		assert.Equal(t, "my-bucket", gjson.GetBytes(delivered, "BucketName").String(),
			"the consumer's plain fields must reach the plugin alongside the referenced one")
		assert.True(t, gjson.GetBytes(delivered, "DbPassword").Exists(),
			"the referenced field must not be dropped from the plugin payload, got %s", delivered)
	})
}
