// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
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

// A property bound to a generator holds a reference to a value, and the apply
// that writes it is what turns the reference into one. The generator draws
// before its destination dispatches, the provider is handed the drawn
// credential itself, and what lands at rest is a digest of that credential
// under the generation it came from — never the credential and never the
// envelope naming it.
func TestApplyForma_GeneratorBoundSecret_DrawnValueIsWrittenAndStoredHashed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logCapture := test_helpers.SetupTestLogger()
		captured := newCapturedCreates("SecretString", "Name")

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, captured.overrides(), cfg)
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
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)
		assert.Equal(t, forma_command.CommandStateSuccess, cmd.State,
			"a generator that draws lets its destination be written")

		secretUpdate := findResourceUpdate(cmd.ResourceUpdates, "db")
		require.NotNil(t, secretUpdate, "the bound secret must be planned and written")
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, secretUpdate.State)
		assert.Empty(t, secretUpdate.FailureReason)

		// Delivery: the provider is handed the credential, at the length the
		// spec declares, rather than the envelope that names it.
		drawn, ok := captured.valueFor("db")
		require.True(t, ok, "the provider must have been called for the bound secret")
		assert.Len(t, drawn, 24, "the provider receives the drawn value, at its declared length")
		assert.NotContains(t, drawn, "$gen",
			"the provider must receive the drawn value, never the envelope naming it")

		// At rest: a digest of the credential, marked a digest, stamped with
		// the generation it was drawn from.
		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identity.GenerationID, "a delivered draw must leave its generation recorded")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 1, "the destination must be written")
		for i := range resources {
			assert.NotContains(t, string(resources[i].Properties), drawn,
				"resources.properties leaked the drawn value for %s", resources[i].Label)
		}
		binding := storedBinding(t, m, stack, "db")
		assert.True(t, binding.Get("$hashed").Bool(),
			"the drawn value must be stored as a digest")
		assert.Equal(t, pkgmodel.ComputeValueHash(drawn), binding.Get("$value").String(),
			"the stored digest must be the digest of the value the provider received")
		resolvedFrom := binding.Get("$resolvedFrom").String()
		assert.True(t, provenance.Valid(resolvedFrom),
			"the drawn destination must carry a current-domain provenance digest, got %q", resolvedFrom)
		assert.Equal(t, provenance.DigestOfString(identity.GenerationID), resolvedFrom,
			"the destination must be stamped with the generation its value was drawn from")

		// The value exists in the cloud object and in nothing formae stored.
		assertDrawnValueIsNotStored(t, m, cmd.ID, drawn, "db-password", stack)
		assertProvenanceCarriersAreWellFormed(t, m, cmd.ID)
		assertNoPlaintextInLogs(t, logCapture, drawn)
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

// capturedChainCreates records what each of the two resource types in a
// generator-fed reference chain was created with: the secret's own bound
// property and the consumer's referencing one. The consumer answers its own
// create so it carries a NativeID of its own; the secret falls through to
// FakeAWS, whose SecretsManager handling reads the value back bare.
type capturedChainCreates struct {
	mu       sync.Mutex
	secret   string
	consumer string
	seen     map[string]int
}

func newCapturedChainCreates() *capturedChainCreates {
	return &capturedChainCreates{seen: map[string]int{}}
}

func (c *capturedChainCreates) overrides() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			props := gjson.ParseBytes(req.Properties)
			c.mu.Lock()
			c.seen[req.ResourceType]++
			if req.ResourceType == "FakeAWS::S3::Bucket" {
				c.consumer = props.Get("DbPassword").String()
			} else {
				c.secret = props.Get("SecretString").String()
			}
			c.mu.Unlock()
			if req.ResourceType != "FakeAWS::S3::Bucket" {
				return nil, nil
			}
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusSuccess,
				NativeID:        "bucket-native-1",
			}}, nil
		},
	}
}

func (c *capturedChainCreates) values() (string, string, map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]int{}
	for k, v := range c.seen {
		seen[k] = v
	}
	return c.secret, c.consumer, seen
}

// A $ref consumer reading a generator-fed secret is planned as a chain: the
// consumer's reference resolves to the secret's property, and the secret's own
// property is the generator binding. The draw at the root of that chain is
// what makes the whole of it writable — the secret is written with the drawn
// credential and the consumer is written with the same one, resolved from the
// source that has just received it.
//
// The chain is a second place the credential could escape, so every sink is
// swept for it: the two rows, the command's resource updates, the simulate
// response, and the captured log output.
func TestApplyForma_ConsumerDownstreamOfGeneratorFedSecret_ReceivesTheDrawnValue(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logCapture := test_helpers.SetupTestLogger()
		captured := newCapturedChainCreates()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, captured.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		// A forma is translated in place, so each submission gets its own:
		// re-submitting the document a previous command rewrote would name
		// identities that command minted rather than the ones this one does.
		buildForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources: []pkgmodel.Resource{
					genBoundSecret(stack, "my-secret", "db-password", "value"),
					secretConsumer(stack, "my-secret"),
				},
			}
		}

		// The plan carries the whole chain: the secret's generator binding and
		// the consumer's reference to that secret's property.
		sim, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{
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

		_, err = m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)
		assert.Equal(t, forma_command.CommandStateSuccess, cmd.State,
			"a chain rooted in a generator that draws must apply")
		for _, label := range []string{"my-secret", "my-bucket"} {
			ru := findResourceUpdate(cmd.ResourceUpdates, label)
			require.NotNil(t, ru, "%s must be in the command", label)
			assert.Equal(t, resource_update.ResourceUpdateStateSuccess, ru.State, "%s must be written", label)
		}

		secretValue, consumerValue, seen := captured.values()
		assert.Equal(t, map[string]int{
			"FakeAWS::SecretsManager::Secret": 1,
			"FakeAWS::S3::Bucket":             1,
		}, seen, "both ends of the chain must be created exactly once")
		require.Len(t, secretValue, 24, "the source must be written with the drawn value")
		assert.Equal(t, secretValue, consumerValue,
			"the consumer must receive the value its source was just given")
		assert.NotContains(t, consumerValue, "$ref",
			"the consumer must receive a value, never the reference naming it")

		// Both rows hold digests and provenance, never the credential.
		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identity.GenerationID)

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 2, "both ends of the chain must be written")
		var consumerRow *pkgmodel.Resource
		for i := range resources {
			assert.NotContains(t, string(resources[i].Properties), secretValue,
				"resources.properties leaked the drawn value for %s", resources[i].Label)
			if resources[i].Label == "my-bucket" {
				consumerRow = resources[i]
			}
		}
		require.NotNil(t, consumerRow)

		sourceBinding := storedBinding(t, m, stack, "my-secret")
		assert.True(t, sourceBinding.Get("$hashed").Bool(), "the source must store a digest")
		assert.Equal(t, pkgmodel.ComputeValueHash(secretValue), sourceBinding.Get("$value").String(),
			"the source's stored digest must be the digest of the drawn value")
		assert.Equal(t, provenance.DigestOfString(identity.GenerationID),
			sourceBinding.Get("$resolvedFrom").String(),
			"the source must be stamped with the generation its value was drawn from")

		consumerResolvedFrom := gjson.GetBytes(consumerRow.Properties, "DbPassword.$resolvedFrom").String()
		assert.True(t, provenance.Valid(consumerResolvedFrom),
			"the consumer must carry a current-domain provenance digest, got %q", consumerResolvedFrom)

		// The simulate response is presentation data: neither the credential
		// nor its at-rest digest may appear in it.
		simJSON, err := json.Marshal(sim)
		require.NoError(t, err)
		assert.NotContains(t, string(simJSON), secretValue,
			"the simulate response must never carry the drawn value")
		assert.NotContains(t, string(simJSON), pkgmodel.ComputeValueHash(secretValue),
			"digests live at rest, never in a response")

		assertDrawnValueIsNotStored(t, m, cmd.ID, secretValue, "db-password", stack)
		assertProvenanceCarriersAreWellFormed(t, m, cmd.ID)
		assertNoPlaintextInLogs(t, logCapture, secretValue)
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
