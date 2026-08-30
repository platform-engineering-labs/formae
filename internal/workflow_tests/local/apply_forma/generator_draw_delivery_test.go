// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// capturedCreates records the properties each Create reached the provider
// with, so a test can assert on the value a destination was actually written
// with. Returning nil falls through to FakeAWS's own handling.
type capturedCreates struct {
	mu         sync.Mutex
	byLabel    map[string]string
	fieldPath  string
	labelField string
}

func newCapturedCreates(fieldPath, labelField string) *capturedCreates {
	return &capturedCreates{byLabel: map[string]string{}, fieldPath: fieldPath, labelField: labelField}
}

func (c *capturedCreates) overrides() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			props := gjson.ParseBytes(req.Properties)
			c.byLabel[props.Get(c.labelField).String()] = props.Get(c.fieldPath).String()
			return nil, nil
		},
	}
}

func (c *capturedCreates) valueFor(label string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.byLabel[label]
	return v, ok
}

// A secret bound to a generator is created with the value that generator
// drew: the draw runs before its destination dispatches, the value is
// delivered into the destination's $gen envelope, and the plugin receives the
// plain credential rather than the envelope.
//
// The drawn value must then exist nowhere durable in plaintext — not in the
// command record, not on the generator's own row. The generator holds the
// spec a value was drawn under and the generation's identity; it never holds
// the value.
func TestApplyForma_GeneratorBoundSecret_DrawnValueReachesTheProviderAndNothingElse(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
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
		require.Equal(t, forma_command.CommandStateSuccess, cmd.State,
			"a generator that draws lets its destination be written")

		secretUpdate := findResourceUpdate(cmd.ResourceUpdates, "db")
		require.NotNil(t, secretUpdate)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, secretUpdate.State)

		drawnValue, ok := captured.valueFor("db")
		require.True(t, ok, "the provider must have been called for the bound secret")
		assert.Len(t, drawnValue, 24, "the provider receives the drawn value, at its declared length")
		assert.NotContains(t, drawnValue, "$gen",
			"the provider must receive the drawn value, never the envelope naming it")

		// The value exists in the cloud object and in nothing formae stored.
		assertDrawnValueIsNotStored(t, m, cmd.ID, drawnValue, "db-password", stack)
	})
}

// assertDrawnValueIsNotStored re-reads the durable records a drawn value
// could leak into and asserts none of them holds it in plaintext.
func assertDrawnValueIsNotStored(t *testing.T, m *metastructure.Metastructure, commandID, drawnValue, generatorLabel, stack string) {
	t.Helper()
	require.NotEmpty(t, drawnValue)

	assertNoPlaintextInResourceUpdates(t, m, commandID, drawnValue)

	stored, err := m.Datastore.GetFormaCommandByCommandID(commandID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	encodedCommand, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(encodedCommand), drawnValue),
		"the command record must never hold the drawn value")

	identity, err := m.Datastore.GetGeneratorIdentity(generatorLabel, stack)
	require.NoError(t, err)
	require.NotEmpty(t, identity.GenerationID,
		"a delivered draw must leave its generation recorded")
	assert.False(t, strings.Contains(string(identity.GenerationSpec), drawnValue),
		"the recorded generation spec must never hold the drawn value")

	generator, err := m.Datastore.GetGenerator(generatorLabel, stack)
	require.NoError(t, err)
	require.NotNil(t, generator)
	encodedGenerator, err := json.Marshal(generator)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(encodedGenerator), drawnValue),
		"the generator row must never hold the value drawn from it")
}

// An identical re-apply of a generator-bound secret must not redraw. The
// destination carries the generation the value it holds was drawn from, so
// the occurrence classifies stable, nothing is planned, and the credential
// in the cloud object is left alone.
//
// Planning nothing is asserted twice over: the simulation reports no change
// AND no second command reaches the datastore. Only the first would still
// pass against a build that plans a harmless no-op update, which would still
// redraw.
func TestApplyForma_GeneratorBoundSecret_IdenticalReapplyDoesNotRedraw(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		captured := newCapturedCreates("SecretString", "Name")

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, captured.overrides(), cfg)
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
		firstCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, firstCmd)
		require.Equal(t, forma_command.CommandStateSuccess, firstCmd.State,
			"precondition: the first apply must draw and write")

		drawn, ok := captured.valueFor("db")
		require.True(t, ok, "precondition: the provider must have been called with the drawn value")

		firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID, "precondition: the draw must be recorded")

		// The stored destination carries resolution provenance: a well-formed
		// digest, never the value.
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		var secretRow *pkgmodel.Resource
		for i := range resources {
			if resources[i].Label == "db" {
				secretRow = resources[i]
			}
		}
		require.NotNil(t, secretRow)
		resolvedFrom := gjson.GetBytes(secretRow.Properties, "SecretString.$resolvedFrom").String()
		assert.True(t, provenance.Valid(resolvedFrom),
			"the drawn destination must carry a current-domain provenance digest, got %q", resolvedFrom)
		assert.Equal(t, provenance.DigestOfString(firstGeneration.GenerationID), resolvedFrom,
			"the destination must be stamped with the generation its value was drawn from")

		resp, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired,
			"an identical re-apply of a generator-bound secret must plan nothing")

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, cmds, 1, "no second command may be created")

		secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"the re-apply must not advance the generation")

		later, ok := captured.valueFor("db")
		require.True(t, ok)
		assert.Equal(t, drawn, later, "the credential in the cloud object must be untouched")
	})
}
