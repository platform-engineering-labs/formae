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
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// What a destination is planned against is the generation its generator holds
// now, compared with the generation the destination was last written under. A
// generator whose generation has moved on owes its destinations a value; one
// whose generation still matches owes nothing, and pulling its destinations in
// would rotate a live credential nobody asked to rotate.
//
// Two generators are what makes this a gate rather than an observation. With a
// single generator the same run passes against a build that plans every $gen
// destination it can see, and against one that plans on the generation
// comparison; the two are only told apart by a second generator in the same
// forma, bound to a second destination, whose generation did not move. The two
// halves are equally load-bearing: the planned destination alone would also be
// satisfied by planning everything, and the suppressed one alone would also be
// satisfied by planning nothing.
//
// The generation is advanced directly on the generator's row rather than by
// editing its spec. A spec edit moves two things at once (the declared spec
// and, through it, the acceptability of the held generation), so a build that
// planned off the spec diff and never read the generation would pass. Moving
// only the generation leaves the declared spec byte-identical, so the
// comparison against what the destination holds is the only thing left that
// can decide it.
func TestApplyForma_MovedGeneration_PlansOnlyItsOwnDestinations(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logCapture := test_helpers.SetupTestLogger()
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		generator := func(label string) json.RawMessage {
			return rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
				Label: label, Stack: stack,
				Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
			})
		}
		buildForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{generator("moved-password"), generator("steady-password")},
				Resources: []pkgmodel.Resource{
					genBoundSecret(stack, "alpha", "moved-password", "value"),
					genBoundSecret(stack, "beta", "steady-password", "value"),
				},
			}
		}

		_, err = m.ApplyForma(buildForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		movedBefore, err := m.Datastore.GetGeneratorIdentity("moved-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, movedBefore.GenerationID, "precondition: each generator must have drawn")
		steadyBefore, err := m.Datastore.GetGeneratorIdentity("steady-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, steadyBefore.GenerationID)

		alphaFirst := delivered.createdWith("alpha")
		betaFirst := delivered.createdWith("beta")
		require.Len(t, alphaFirst, 1, "precondition: alpha must be created with its generator's draw")
		require.Len(t, betaFirst, 1, "precondition: beta must be created with its generator's draw")
		require.NotEqual(t, alphaFirst[0], betaFirst[0],
			"precondition: two generators draw two values")
		betaBinding := storedBinding(t, m, stack, "beta")
		require.True(t, betaBinding.Get("$hashed").Bool())
		betaDigestBefore := betaBinding.Get("$value").String()
		betaResolvedBefore := betaBinding.Get("$resolvedFrom").String()
		require.Equal(t, provenance.DigestOfString(steadyBefore.GenerationID), betaResolvedBefore)

		// One generator's generation moves on. Its declared spec is untouched,
		// and the other generator is not touched at all.
		movedGenerationID := util.NewID()
		require.NoError(t, m.Datastore.AdvanceGeneration(
			movedBefore.ID, movedGenerationID, movedBefore.GenerationSpec))
		steadyUnmoved, err := m.Datastore.GetGeneratorIdentity("steady-password", stack)
		require.NoError(t, err)
		require.Equal(t, steadyBefore.GenerationID, steadyUnmoved.GenerationID,
			"precondition: advancing one generator must not move the other")

		sim, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test-client-id", "", "")
		require.NoError(t, err)
		require.True(t, sim.Simulation.ChangesRequired,
			"a destination holding a superseded generation must be planned")
		plannedLabels := map[string]bool{}
		for _, ru := range sim.Simulation.Command.ResourceUpdates {
			plannedLabels[ru.ResourceLabel] = true
		}
		assert.True(t, plannedLabels["alpha"],
			"the destination of the generator whose generation moved must be planned, got %v", plannedLabels)
		assert.False(t, plannedLabels["beta"],
			"the destination of the generator whose generation stood still must not be planned, got %v", plannedLabels)

		// The response is presentation data: neither destination's value nor
		// its at-rest digest may appear in it.
		simJSON, err := json.Marshal(sim)
		require.NoError(t, err)
		for _, value := range []string{alphaFirst[0], betaFirst[0]} {
			assert.NotContains(t, string(simJSON), value,
				"the simulate response must never carry a drawn value")
			assert.NotContains(t, string(simJSON), pkgmodel.ComputeValueHash(value),
				"digests live at rest, never in a response")
		}

		// Applying it carries the plan through: alpha is rewritten under a
		// freshly drawn value, beta is left exactly as it was.
		_, err = m.ApplyForma(buildForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 2, "the second apply must produce exactly one further command")
		var second *forma_command.FormaCommand
		for _, c := range cmds {
			require.Equal(t, forma_command.CommandStateSuccess, c.State)
			if second == nil || c.StartTs.After(second.StartTs) {
				second = c
			}
		}
		require.NotNil(t, second)

		alphaUpdates := delivered.updatedWith("alpha")
		require.Len(t, alphaUpdates, 1, "the planned destination must be written")
		assert.Len(t, alphaUpdates[0], 24)
		assert.NotEqual(t, alphaFirst[0], alphaUpdates[0],
			"the planned destination must move to a freshly drawn value")
		assert.Empty(t, delivered.updatedWith("beta"),
			"a generator whose generation stood still must not have its destination written")

		movedAfter, err := m.Datastore.GetGeneratorIdentity("moved-password", stack)
		require.NoError(t, err)
		assert.NotEqual(t, movedGenerationID, movedAfter.GenerationID,
			"writing the destination draws its own generation")
		steadyAfter, err := m.Datastore.GetGeneratorIdentity("steady-password", stack)
		require.NoError(t, err)
		assert.Equal(t, steadyBefore.GenerationID, steadyAfter.GenerationID,
			"the untouched generator must not draw")

		alphaBinding := storedBinding(t, m, stack, "alpha")
		assert.True(t, alphaBinding.Get("$hashed").Bool(),
			"the rewritten destination must store a digest")
		assert.Equal(t, pkgmodel.ComputeValueHash(alphaUpdates[0]), alphaBinding.Get("$value").String(),
			"the stored digest must be the digest of the value the provider received")
		assert.Equal(t, provenance.DigestOfString(movedAfter.GenerationID),
			alphaBinding.Get("$resolvedFrom").String(),
			"the rewritten destination must be stamped with the generation it was drawn from")

		betaAfter := storedBinding(t, m, stack, "beta")
		assert.Equal(t, betaDigestBefore, betaAfter.Get("$value").String(),
			"the untouched destination must keep the credential it held")
		assert.Equal(t, betaResolvedBefore, betaAfter.Get("$resolvedFrom").String(),
			"the untouched destination must keep the generation it was drawn from")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 2)
		for i := range resources {
			for _, value := range []string{alphaFirst[0], alphaUpdates[0], betaFirst[0]} {
				assert.NotContains(t, string(resources[i].Properties), value,
					"resources.properties leaked a drawn value for %s", resources[i].Label)
			}
		}

		for _, cmd := range cmds {
			for _, value := range []string{alphaFirst[0], alphaUpdates[0], betaFirst[0]} {
				assertNoPlaintextInResourceUpdates(t, m, cmd.ID, value)
			}
			assertProvenanceCarriersAreWellFormed(t, m, cmd.ID)
		}
		for _, value := range []string{alphaFirst[0], alphaUpdates[0], betaFirst[0]} {
			assertNoPlaintextInLogs(t, logCapture, value)
		}
	})
}
