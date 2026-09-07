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
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A draw that cannot produce a value fails, and every destination waiting on
// it fails with it rather than dispatching the envelope that names the value.
// The failure is recorded: the command reaches a terminal failed state, both
// destinations are stored failed, and nothing was written on either side of
// the boundary — no provider call, no resource row, and no generation recorded
// for a generator that never produced one.
//
// The spec asks for three character classes in a value of two characters,
// which nothing can satisfy. It is admitted, because what a spec can produce
// is decided where the value is drawn and not at the door.
func TestApplyForma_GeneratorDrawFailure_FailsItsDestinationsAndIsRecorded(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: stack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: stack,
				Length: 2, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			})},
			Resources: []pkgmodel.Resource{
				genBoundSecret(stack, "alpha", "db-password", "value"),
				genBoundSecret(stack, "beta", "db-password", "value"),
			},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err, "an undrawable spec is admitted and fails where the value is drawn")
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)
		assert.Equal(t, forma_command.CommandStateFailed, cmd.State,
			"a command whose draw failed must not be reported as applied")

		// Reloaded from the datastore rather than read off the in-memory
		// command: a failure the command reports but never stores is a failure
		// nothing can be asked about afterwards.
		stored, err := m.Datastore.GetFormaCommandByCommandID(cmd.ID)
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, forma_command.CommandStateFailed, stored.State)
		for _, label := range []string{"alpha", "beta"} {
			ru := findResourceUpdate(stored.ResourceUpdates, label)
			require.NotNil(t, ru, "%s must be in the stored command", label)
			assert.Equal(t, resource_update.ResourceUpdateStateFailed, ru.State,
				"%s waits on the failed draw and must fail with it", label)
		}

		assert.Empty(t, delivered.createdWith("alpha"),
			"a destination whose value was never drawn must not reach the provider")
		assert.Empty(t, delivered.createdWith("beta"),
			"a destination whose value was never drawn must not reach the provider")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, resources, "a refused write must leave no resource behind")

		identity, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Empty(t, identity.GenerationID,
			"a generator that produced no value must record no generation")
	})
}

// A destination that failed because the draw failed carries the draw's reason,
// so the apply explains itself. The reason is read back off the stored command
// through MostRecentFailureMessage, which is what the API projection puts on
// ResourceUpdate.ErrorMessage and what every status surface renders.
func TestApplyForma_GeneratorDrawFailure_ExplainsWhyItsDestinationsFailed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: stack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: stack,
				Length: 2, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			})},
			Resources: []pkgmodel.Resource{
				genBoundSecret(stack, "alpha", "db-password", "value"),
				genBoundSecret(stack, "beta", "db-password", "value"),
			},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		cmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, cmd)

		stored, err := m.Datastore.GetFormaCommandByCommandID(cmd.ID)
		require.NoError(t, err)
		require.NotNil(t, stored)
		for _, label := range []string{"alpha", "beta"} {
			ru := findResourceUpdate(stored.ResourceUpdates, label)
			require.NotNil(t, ru, "%s must be in the stored command", label)
			assert.Contains(t, ru.MostRecentFailureMessage(),
				"its specification admits no value",
				"%s failed because the draw failed and must say so", label)
		}

		// The same reason read back through the status API, which is the
		// projection every operator-facing surface renders.
		status, err := m.ListFormaCommandStatus("", querier.Caller{ClientID: "test-client-id"}, 1, apimodel.CommandScopeClient)
		require.NoError(t, err)
		require.Len(t, status.Commands, 1)
		reported := 0
		for _, ru := range status.Commands[0].ResourceUpdates {
			if ru.ResourceLabel != "alpha" && ru.ResourceLabel != "beta" {
				continue
			}
			reported++
			assert.Contains(t, ru.ErrorMessage, "its specification admits no value",
				"%s must report why it failed", ru.ResourceLabel)
		}
		assert.Equal(t, 2, reported, "both destinations must appear in the status projection")
	})
}
