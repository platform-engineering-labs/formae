// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
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

// countingCreates records, per resource type, how many times a plugin Create
// was actually reached. Returning nil falls through to FakeAWS's own handling,
// so the count is the only behaviour it adds.
func countingCreates(counts map[string]*atomic.Int32) *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			if c, ok := counts[req.ResourceType]; ok {
				c.Add(1)
			}
			return nil, nil
		},
	}
}

// findResourceUpdate returns the update for a label, or nil.
func findResourceUpdate(updates []resource_update.ResourceUpdate, label string) *resource_update.ResourceUpdate {
	for i := range updates {
		if updates[i].DesiredState.Label == label {
			return &updates[i]
		}
	}
	return nil
}

// A property bound to a generator holds a reference to a value, never the
// value itself. Until that value has been drawn there is nothing to write, so
// the apply fails at the plugin boundary and the provider is never called:
// formae refuses to put the reference into the cloud object in the value's
// place. This is a property of the write path, not of the current state of
// generator support — a draw that fails must leave the destination unwritten
// for the same reason.
func TestApplyForma_GeneratorBoundSecret_UndrawnValueIsNeverWritten(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var secretCreates atomic.Int32
		overrides := countingCreates(map[string]*atomic.Int32{
			"FakeAWS::SecretsManager::Secret": &secretCreates,
		})

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
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "db", "db-password", "value")},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "the forma is well-formed, so it is admitted and fails at execution")
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)
		assert.Equal(t, forma_command.CommandStateFailed, cmd.State,
			"an undrawn generator binding must not be reported as applied")

		secretUpdate := findResourceUpdate(cmd.ResourceUpdates, "db")
		require.NotNil(t, secretUpdate, "the bound secret must be planned and then refused")
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, secretUpdate.State)
		assert.Contains(t, secretUpdate.FailureReason, "bound to a generator whose value has not been drawn",
			"the refusal must say what is wrong with the resource, not just that a request could not be built")

		assert.Zero(t, secretCreates.Load(),
			"the provider must never be called with a value formae does not have")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, resources, "a refused write must leave no resource behind")
	})
}

// A generator declared by one command and referenced by a later one resolves
// to the KSUID the generator's own row already carries: the binding names one
// identity, not a freshly minted one per command.
//
// The second command's translation is observed through what it plans. A
// binding translated to a freshly minted KSUID would name a different source
// than the stored envelope does, which is a repoint and always plans; that
// the identical second command plans nothing for the bound secret is
// therefore the identity holding across commands.
func TestApplyForma_GeneratorBoundSecret_BindingNamesTheGeneratorsOwnIdentity(t *testing.T) {
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

		// The first command establishes the generator's row and writes the
		// bound secret with the value drawn for it.
		_, err = m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identity.ID, "precondition: the declared generator must have a row")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		var secretRow *pkgmodel.Resource
		for i := range resources {
			if resources[i].Label == "db" {
				secretRow = resources[i]
			}
		}
		require.NotNil(t, secretRow, "precondition: the bound secret must be written")
		assert.Equal(t, identity.ID,
			gjson.GetBytes(secretRow.Properties, "SecretString.$generator").String(),
			"the binding must name the generator's own KSUID, not one minted for this command")

		resp, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test-client-id", "", "")
		require.NoError(t, err)

		for _, ru := range resp.Simulation.Command.ResourceUpdates {
			assert.NotEqual(t, "db", ru.ResourceLabel,
				"a second command that named a fresh KSUID would repoint the binding and plan it")
		}

		identityAfter, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, identity.ID, identityAfter.ID,
			"a second command must not move the generator's identity")
		generators, err := m.Datastore.LoadGeneratorsByStack(stack)
		require.NoError(t, err)
		assert.Len(t, generators, 1, "one declared generator is one live generator")
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

// A $ref consumer reading a generator-fed secret is planned as a chain: the
// consumer's reference resolves to the secret's property, and the secret's own
// property is the generator binding. Nothing in that chain is writable until
// the value has been drawn, so the whole command is refused at the plugin
// boundary — the consumer is not written from a source that was itself never
// written, and neither provider is called.
func TestApplyForma_ConsumerDownstreamOfGeneratorFedSecret_IsRejectedBeforeAnyWrite(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var secretCreates, consumerCreates atomic.Int32
		overrides := countingCreates(map[string]*atomic.Int32{
			"FakeAWS::SecretsManager::Secret": &secretCreates,
			"FakeAWS::S3::Bucket":             &consumerCreates,
		})

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

		// The plan carries the whole chain: the secret's generator binding and
		// the consumer's reference to that secret's property.
		sim, err := m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test-client-id", "", "")
		require.NoError(t, err)
		planned := map[string]apimodel.ResourceUpdate{}
		for _, ru := range sim.Simulation.Command.ResourceUpdates {
			planned[ru.ResourceLabel] = ru
		}
		secretPlan, ok := planned["my-secret"]
		require.True(t, ok, "the generator-fed secret must be planned, got %v", planned)
		consumerPlan, ok := planned["my-bucket"]
		require.True(t, ok, "the downstream consumer must be planned, got %v", planned)
		assert.True(t, gjson.GetBytes(secretPlan.Properties, "SecretString.$gen").Bool(),
			"the source's property must be the generator binding, got %s", secretPlan.Properties)
		consumerRef := gjson.GetBytes(consumerPlan.Properties, "DbPassword.$ref").String()
		assert.True(t, strings.Contains(consumerRef, secretPlan.ResourceID) &&
			strings.HasSuffix(consumerRef, "#/SecretString"),
			"the consumer must reference the generator-fed secret's property, got %q", consumerRef)

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)
		assert.Equal(t, forma_command.CommandStateFailed, cmd.State,
			"a chain rooted in an undrawn generator must not be reported as applied")

		assert.Zero(t, secretCreates.Load(), "the source secret must never be written")
		assert.Zero(t, consumerCreates.Load(),
			"the consumer must never be written from a source that was itself refused")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, resources, "a refused chain must leave nothing behind")
	})
}

// A target's configuration reaches every plugin call made against that
// target, so a credential bound to a generator is refused there for the same
// reason a bound resource property is: the envelope names a value to be
// drawn and is never that value. The resource itself is plain here, so the
// only unresolved binding in the command is on the target.
func TestApplyForma_TargetConfigBoundToGenerator_UndrawnValueIsNeverWritten(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var anchorCreates atomic.Int32
		overrides := countingCreates(map[string]*atomic.Int32{
			"FakeAWS::Versioned::Parent": &anchorCreates,
		})

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "api-token", stack)},
			Targets: []pkgmodel.Target{{
				Label:     "test-target",
				Namespace: "test-namespace",
				Config: json.RawMessage(`{
					"Region": "us-east-1",
					"Token": {
						"$gen":        true,
						"$label":      "api-token",
						"$stack":      "` + stack + `",
						"$output":     "value",
						"$visibility": "Opaque"
					}
				}`),
			}},
			Resources: []pkgmodel.Resource{{
				Label:      "anchor",
				Type:       "FakeAWS::Versioned::Parent",
				Stack:      stack,
				Target:     "test-target",
				Properties: json.RawMessage(`{"Name": "anchor", "Value": "v1"}`),
			}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "the forma is well-formed, so it is admitted and fails at execution")
		waitForApplyComplete(t, m)

		// The plugin count is the assertion that matters: a build that hands
		// the envelope over reports the command successful, so command state
		// alone would not see it.
		assert.Zero(t, anchorCreates.Load(),
			"no plugin call may be made through a target formae cannot authenticate")

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)
		assert.Equal(t, forma_command.CommandStateFailed, cmd.State,
			"an undrawn target credential must not be reported as applied")

		anchorUpdate := findResourceUpdate(cmd.ResourceUpdates, "anchor")
		require.NotNil(t, anchorUpdate)
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, anchorUpdate.State)
		assert.Contains(t, anchorUpdate.FailureReason, "target's configuration is bound to a generator",
			"the refusal must name the target's configuration, not the resource's own properties")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, resources, "a refused write must leave no resource behind")
	})
}
