// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// These tests pin the scope of a generator delivery: WHETHER a generator
// draws is decided by the destinations' stability, but once a draw happens
// every live non-delete destination of that generator in the changeset
// receives it. The four shapes that rule has to get right are:
//
//  1. an ordinary re-apply, nothing moved — no draw at all. Pinned by
//     TestApplyForma_GeneratorBoundSecret_IdenticalReapplyDoesNotRedraw.
//  2. a second consumer added — one destination is new, so a draw happens,
//     and every destination of that generator in the changeset must end up on
//     the new generation, the already-applied one included.
//  3. the generator's spec edited — every destination is unstable, draws,
//     and every one gets the new value.
//  4. an unrelated field edited on a bound resource — nothing about the
//     binding moved, so no draw, and the stable binding is frozen rather
//     than refused at the provider boundary.
//
// Shape 2 is the one that decides convergence. Giving the new generation only
// to the new destination leaves the two consumers of one credential holding
// different values, and the next apply repairs the one and breaks the other,
// forever.
//
// "In the changeset" is load-bearing in shape 2. A consumer that this command
// plans no operation for — because nothing about it moved — is not a node in
// the graph, so no delivery can reach it, and it cannot be caught up later
// either: formae keeps only a hash of a generated value, never the value.
// Such a consumer is left on its older generation.

// deliveredValues is a per-label fake secret store that records every value
// each label was written with, per operation, so a test can see which
// destinations a draw actually reached.
//
// It has to stand in for FakeAWS's own SecretsManager handling rather than
// fall through to it, because FakeAWS mints a single constant NativeID
// ("5678") for every resource it creates. Two secrets in one stack would then
// share a cloud identity and the second would be read as a rename of the
// first. Everything else mirrors FakeAWS: what a write sends is what a later
// Read returns, secret values bare rather than enveloped.
type deliveredValues struct {
	mu      sync.Mutex
	creates map[string][]string
	updates map[string][]string
	patches map[string][]string
	stored  map[string]json.RawMessage
}

func newDeliveredValues() *deliveredValues {
	return &deliveredValues{
		creates: map[string][]string{},
		updates: map[string][]string{},
		patches: map[string][]string{},
		stored:  map[string]json.RawMessage{},
	}
}

func nativeIDFor(label string) string { return "native-" + label }

func (d *deliveredValues) overrides() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			props := gjson.ParseBytes(req.Properties)
			label := props.Get("Name").String()
			nativeID := nativeIDFor(label)
			d.mu.Lock()
			d.creates[label] = append(d.creates[label], props.Get("SecretString").String())
			d.stored[nativeID] = req.Properties
			d.mu.Unlock()
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				NativeID:           nativeID,
				ResourceProperties: req.Properties,
			}}, nil
		},
		Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
			props := gjson.ParseBytes(req.DesiredProperties)
			label := props.Get("Name").String()
			patch := ""
			if req.PatchDocument != nil {
				patch = string(*req.PatchDocument)
			}
			d.mu.Lock()
			d.updates[label] = append(d.updates[label], props.Get("SecretString").String())
			d.patches[label] = append(d.patches[label], patch)
			d.stored[req.NativeID] = req.DesiredProperties
			d.mu.Unlock()
			return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
				Operation:          resource.OperationUpdate,
				OperationStatus:    resource.OperationStatusSuccess,
				NativeID:           req.NativeID,
				ResourceProperties: req.DesiredProperties,
			}}, nil
		},
		Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
			d.mu.Lock()
			stored := d.stored[req.NativeID]
			d.mu.Unlock()
			if len(stored) == 0 {
				stored = json.RawMessage("{}")
			}
			return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(stored)}, nil
		},
	}
}

func (d *deliveredValues) createdWith(label string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.creates[label]...)
}

func (d *deliveredValues) updatedWith(label string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.updates[label]...)
}

// patchesFor returns the PatchDocument each update carried. A plugin is
// entitled to drive its write from the patch rather than from the whole
// desired document, so a delivered value that is missing from the patch is a
// value that plugin never writes.
func (d *deliveredValues) patchesFor(label string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.patches[label]...)
}

// storedResolvedFrom returns the provenance digest a stored destination
// carries for its generator-bound property, which is what the next apply
// classifies against.
func storedResolvedFrom(t *testing.T, m *metastructure.Metastructure, stack, label string) string {
	t.Helper()
	resources, err := m.Datastore.LoadResourcesByStack(stack)
	require.NoError(t, err)
	for i := range resources {
		if resources[i].Label == label {
			return gjson.GetBytes(resources[i].Properties, "SecretString.$resolvedFrom").String()
		}
	}
	t.Fatalf("no stored resource %q in stack %q", label, stack)
	return ""
}

// Shape 2. A draw that happens must reach every destination of its generator
// in the changeset, including one whose own occurrence classified stable.
//
// The second apply adds a consumer AND edits an unrelated field on the
// consumer that was already applied, so the applied one is planned and is a
// node in the changeset. Its $gen occurrence is stable — nothing about the
// binding moved — but the generator is drawing for the new consumer, and
// delivering that draw to only the new consumer would leave the two holding
// different credentials. Every apply after that would repair one and break
// the other, forever.
//
// The convergence is asserted the only way that cannot be faked: a third,
// identical apply must plan nothing at all.
func TestApplyForma_GeneratorBoundSecret_ADrawReachesEveryDestinationInTheChangeset(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		describedSecret := func(label, description string) pkgmodel.Resource {
			secret := genBoundSecret(stack, label, "db-password", "value")
			secret.Properties = json.RawMessage(`{
				"Name": "` + label + `",
				"Description": "` + description + `",
				"SecretString": {
					"$gen":        true,
					"$label":      "db-password",
					"$stack":      "` + stack + `",
					"$output":     "value",
					"$visibility": "Opaque"
				}
			}`)
			return secret
		}
		forma := func(resources ...pkgmodel.Resource) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources:  resources,
			}
		}

		_, err = m.ApplyForma(forma(describedSecret("alpha", "before")),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID, "the first apply must draw")
		require.Len(t, delivered.createdWith("alpha"), 1, "alpha must have been created with the first draw")

		_, err = m.ApplyForma(forma(describedSecret("alpha", "after"), describedSecret("beta", "beta")),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 2, "the second apply must produce a command")
		for _, cmd := range cmds {
			require.Equal(t, forma_command.CommandStateSuccess, cmd.State)
		}

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEqual(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"adding a consumer draws a new generation")

		betaCreates := delivered.createdWith("beta")
		require.Len(t, betaCreates, 1, "beta must have been created")
		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1, "the applied consumer is planned, so it must be written")
		assert.Equal(t, betaCreates[0], alphaUpdates[0],
			"two consumers of one generator must hold the same value")
		assert.NotEqual(t, delivered.createdWith("alpha")[0], alphaUpdates[0],
			"the applied consumer must move to the new generation, not keep the old value")

		alphaPatches := delivered.patchesFor("alpha")
		require.Len(t, alphaPatches, 1)
		assert.Contains(t, alphaPatches[0], "/SecretString",
			"a delivered value must appear in the patch, or a patch-driven plugin never writes it")

		// Both rows must be stamped with the generation that value came from,
		// or the next apply reads one of them as moved and rotates again.
		expected := provenance.DigestOfString(secondGeneration.GenerationID)
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "alpha"))
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "beta"))

		// The convergence proof: a third identical apply plans nothing.
		resp, err := m.ApplyForma(forma(describedSecret("alpha", "after"), describedSecret("beta", "beta")),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired,
			"once both consumers hold the same generation, an identical apply must plan nothing")

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, cmds, 2, "no third command may be created")

		thirdGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, secondGeneration.GenerationID, thirdGeneration.GenerationID,
			"the generation must stop advancing once the destinations agree")
	})
}

// Shape 3. Editing the generator's own spec makes every destination unstable,
// so a draw happens and every destination receives the new value — at the
// length the edited spec declares.
func TestApplyForma_GeneratorBoundSecret_SpecEditRedrawsForEveryConsumer(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		formaWithLength := func(length int) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:  []pkgmodel.Stack{{Label: stack}},
				Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: stack,
					Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				})},
				Resources: []pkgmodel.Resource{
					genBoundSecret(stack, "alpha", "db-password", "value"),
					genBoundSecret(stack, "beta", "db-password", "value"),
				},
			}
		}

		_, err = m.ApplyForma(formaWithLength(24), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		require.Len(t, delivered.createdWith("alpha"), 1)
		require.Len(t, delivered.createdWith("beta"), 1)
		require.Equal(t, delivered.createdWith("alpha")[0], delivered.createdWith("beta")[0],
			"one draw serves every destination bound to it")
		require.Len(t, delivered.createdWith("alpha")[0], 24)

		_, err = m.ApplyForma(formaWithLength(40), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		alphaUpdates := delivered.updatedWith("alpha")
		betaUpdates := delivered.updatedWith("beta")
		require.Len(t, alphaUpdates, 1, "a spec edit must rewrite every destination")
		require.Len(t, betaUpdates, 1, "a spec edit must rewrite every destination")
		assert.Len(t, alphaUpdates[0], 40, "the new value must be drawn under the edited spec")
		assert.Equal(t, alphaUpdates[0], betaUpdates[0],
			"one redraw serves every destination bound to it")

		generation, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		expected := provenance.DigestOfString(generation.GenerationID)
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "alpha"))
		assert.Equal(t, expected, storedResolvedFrom(t, m, stack, "beta"))
	})
}

// Shape 4. Editing a field that has nothing to do with the binding leaves
// every $gen occurrence stable, so no draw happens and the generation does
// not advance. The update still has to apply: the bare envelope is frozen to
// the preserved-value sentinel rather than refused at the provider boundary,
// so the guard protecting the credential does not block the edit beside it.
func TestApplyForma_GeneratorBoundSecret_UnrelatedFieldEditDoesNotRedraw(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		formaWithDescription := func(description string) *pkgmodel.Forma {
			secret := genBoundSecret(stack, "alpha", "db-password", "value")
			secret.Properties = json.RawMessage(`{
				"Name": "alpha",
				"Description": "` + description + `",
				"SecretString": {
					"$gen":        true,
					"$label":      "db-password",
					"$stack":      "` + stack + `",
					"$output":     "value",
					"$visibility": "Opaque"
				}
			}`)
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
				Resources:  []pkgmodel.Resource{secret},
			}
		}

		_, err = m.ApplyForma(formaWithDescription("before"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID)

		_, err = m.ApplyForma(formaWithDescription("after"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 2)
		for _, cmd := range cmds {
			assert.Equal(t, forma_command.CommandStateSuccess, cmd.State,
				"a stable binding must not block the edit beside it")
		}

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"an edit that leaves every binding stable must not draw")

		updates := delivered.updatedWith("alpha")
		require.Len(t, updates, 1, "the unrelated edit must reach the provider")
		assert.NotContains(t, updates[0], "$gen",
			"a stable binding is frozen to the preserved sentinel, never dispatched as a bare envelope")
	})
}
