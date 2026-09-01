// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit || integration

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// rotatingGenerator renders a password generator that declares a cadence, in
// the shape PKL produces.
func rotatingGenerator(t *testing.T, label, stack string, everySeconds int) json.RawMessage {
	t.Helper()
	return rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
		Label: label, Stack: stack,
		Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
		Rotation: &pkgmodel.RotationSpec{EverySeconds: everySeconds},
	})
}

// sweepRotations drives one rotation sweep. The rotator's own timer is far
// coarser than a test wants to wait for, so the tick is sent directly: the
// sweep is the actor's whole decision, and driving it is what makes these
// tests about rotation rather than about how long they slept.
func sweepRotations(t *testing.T, m *metastructure.Metastructure) {
	t.Helper()
	require.NoError(t, m.Node.Send(
		gen.ProcessID{Name: actornames.GeneratorRotator, Node: m.Node.Name()},
		metastructure.CheckGeneratorRotations{},
	))
}

// commandCount returns how many forma commands the datastore holds.
func commandCount(t *testing.T, m *metastructure.Metastructure) int {
	t.Helper()
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	return len(commands)
}

// rotationCommands returns the commands a generator rotation produced.
func rotationCommands(t *testing.T, m *metastructure.Metastructure) []*forma_command.FormaCommand {
	t.Helper()
	commands, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	var rotations []*forma_command.FormaCommand
	for _, command := range commands {
		if command.Source == forma_command.SourceGeneratorRotator {
			rotations = append(rotations, command)
		}
	}
	return rotations
}

// sweepUntilRotated drives sweeps until one rotation command has run to a
// terminal state, then returns it.
func sweepUntilRotated(t *testing.T, m *metastructure.Metastructure) *forma_command.FormaCommand {
	t.Helper()
	var rotation *forma_command.FormaCommand
	require.Eventually(t, func() bool {
		sweepRotations(t, m)
		rotations := rotationCommands(t, m)
		if len(rotations) == 0 {
			return false
		}
		rotation = rotations[0]
		return rotation.State == forma_command.CommandStateSuccess ||
			rotation.State == forma_command.CommandStateFailed
	}, 20*time.Second, 200*time.Millisecond, "a rotation must be submitted and reach a terminal state")
	return rotation
}

// A generator whose cadence has elapsed is rotated, and the destination bound
// to it is rewritten with a freshly drawn value: the provider receives new
// material, and the digest and the generation stamp the destination holds at
// rest both move.
func TestGeneratorRotation_RotatesOnScheduleAndRewritesTheDestination(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		logCapture := test_helpers.SetupTestLogger()
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rotatingGenerator(t, "db-password", stack, 1)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
		}

		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		created := delivered.createdWith("alpha")
		require.Len(t, created, 1, "precondition: the destination was created with a drawn value")
		bindingBefore := storedBinding(t, m, stack, "alpha")
		digestBefore := bindingBefore.Get("$value").String()
		resolvedBefore := bindingBefore.Get("$resolvedFrom").String()
		require.NotEmpty(t, digestBefore)
		require.NotEmpty(t, resolvedBefore)
		identityBefore, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, identityBefore.GenerationID)

		rotation := sweepUntilRotated(t, m)
		assert.Equal(t, forma_command.CommandStateSuccess, rotation.State)
		waitForApplyComplete(t, m)

		updates := delivered.updatedWith("alpha")
		require.Len(t, updates, 1, "the rotation must write the destination exactly once")
		assert.Len(t, updates[0], 24, "the rewritten value must match the generator's spec")
		assert.NotEqual(t, created[0], updates[0], "the rotation must deliver a freshly drawn value")

		bindingAfter := storedBinding(t, m, stack, "alpha")
		assert.True(t, bindingAfter.Get("$hashed").Bool(), "the rewritten destination must store a digest")
		assert.Equal(t, pkgmodel.ComputeValueHash(updates[0]), bindingAfter.Get("$value").String(),
			"the stored digest must be the digest of the value the provider received")
		assert.NotEqual(t, resolvedBefore, bindingAfter.Get("$resolvedFrom").String(),
			"the destination must be stamped with the generation it was rotated to")

		identityAfter, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.NotEqual(t, identityBefore.GenerationID, identityAfter.GenerationID,
			"the rotation must advance the generator's generation")

		// The rotation is not a user command: it must not be attributed to one,
		// and it must not become the stack's reconcile baseline.
		assert.Equal(t, forma_command.SourceGeneratorRotator, rotation.Source)

		for _, value := range append(created, updates...) {
			assertNoPlaintextInResourceUpdates(t, m, rotation.ID, value)
			assertNoPlaintextInLogs(t, logCapture, value)
		}
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		for i := range resources {
			for _, value := range append(created, updates...) {
				assert.NotContains(t, string(resources[i].Properties), value,
					"resources.properties leaked a rotated value for %s", resources[i].Label)
			}
		}
	})
}

// The rotation leaves nothing behind that a later apply reads as a change. A
// last-rotation instant kept on the generator would be part of its desired
// spec, so the very next apply of the unchanged forma would plan a generator
// update and, through it, rewrite the credential again.
func TestGeneratorRotation_LeavesNoSpuriousChangeBehind(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		buildForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: []json.RawMessage{rotatingGenerator(t, "db-password", stack, 1)},
				Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
			}
		}

		_, err = m.ApplyForma(buildForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		rotation := sweepUntilRotated(t, m)
		require.Equal(t, forma_command.CommandStateSuccess, rotation.State)
		waitForApplyComplete(t, m)

		sim, err := m.ApplyForma(buildForma(), &config.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyModeReconcile,
			Simulate: true,
		}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, sim.Simulation.ChangesRequired,
			"a rotation must leave the declared state unchanged, got %d resource updates and %d generator updates",
			len(sim.Simulation.Command.ResourceUpdates), len(sim.Simulation.Command.GeneratorUpdates))

		// The instant of the last rotation must not be readable off the
		// generator's stored spec, which is what a diff compares.
		stored, err := m.Datastore.GetGenerator("db-password", stack)
		require.NoError(t, err)
		require.NotNil(t, stored)
		storedSpec, err := json.Marshal(stored)
		require.NoError(t, err)
		declaredSpec := rotatingGenerator(t, "db-password", stack, 1)
		declared, err := pkgmodel.ParseGenerator(declaredSpec)
		require.NoError(t, err)
		declared.SetStack(stack)
		declaredBytes, err := json.Marshal(declared)
		require.NoError(t, err)
		assert.JSONEq(t, string(declaredBytes), string(storedSpec),
			"the stored generator spec must be exactly what was declared, with no rotation bookkeeping added")
	})
}

// The cadence survives a restart, because it is derived from the datastore
// rather than held in the rotator's memory.
//
// This is the assertion an implementation that kept the last-rotation instant
// in actor memory fails and no other assertion catches: such an
// implementation leaves the declared state clean and produces no spurious
// diff, and only losing its memory reveals that it never knew when the
// credential was last rotated.
func TestGeneratorRotation_DerivedCadenceSurvivesARestart(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		// The database has to outlive the first agent, so it is created here
		// and cleaned up here. "no-reset" in the path is what stops the test
		// helper's own cleanup removing the file when the first agent stops.
		dbFile, err := os.CreateTemp("", "formae_test_no-reset_*.db")
		require.NoError(t, err)
		require.NoError(t, dbFile.Close())
		defer func() { _ = os.Remove(dbFile.Name()) }()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		cfg.Agent.Datastore = pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.SqliteDatastore,
			Sqlite:        pkgmodel.SqliteConfig{FilePath: dbFile.Name()},
		}

		stack := "test-stack-" + util.NewID()
		forma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:  []pkgmodel.Stack{{Label: stack}},
				Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				// An hour's cadence, so the generator is decidedly NOT due
				// again once the first apply has drawn for it.
				Generators: []json.RawMessage{rotatingGenerator(t, "db-password", stack, 3600)},
				Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
			}
		}

		first, stopFirst, err := newRotationAgent(t, delivered.overrides(), cfg)
		require.NoError(t, err)

		_, err = first.ApplyForma(forma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, first)
		require.Len(t, delivered.createdWith("alpha"), 1, "precondition: the destination was drawn for")
		commandsBefore := commandCount(t, first)
		bindingBefore := storedBinding(t, first, stack, "alpha").Get("$value").String()
		require.NotEmpty(t, bindingBefore)

		stopFirst()

		// A new agent on the same datastore: nothing about the previous one's
		// memory survives.
		second, stopSecond, err := newRotationAgent(t, delivered.overrides(), cfg)
		require.NoError(t, err)
		defer stopSecond()

		for i := 0; i < 5; i++ {
			sweepRotations(t, second)
			time.Sleep(100 * time.Millisecond)
		}
		waitForApplyComplete(t, second)

		assert.Empty(t, rotationCommands(t, second),
			"a restarted agent must not rotate a credential whose cadence has not elapsed")
		assert.Equal(t, commandsBefore, commandCount(t, second),
			"a restart must produce no further commands")
		assert.Empty(t, delivered.updatedWith("alpha"),
			"the destination must not be rewritten by a restart")
		assert.Equal(t, bindingBefore, storedBinding(t, second, stack, "alpha").Get("$value").String(),
			"the credential at rest must be untouched by a restart")
	})
}

// newRotationAgent starts an agent on the datastore the config names, without
// the test helper's file cleanup: the caller owns the database file so it can
// be handed to a second agent.
func newRotationAgent(t *testing.T, overrides *plugin.ResourcePluginOverrides, cfg *pkgmodel.Config) (*metastructure.Metastructure, func(), error) {
	t.Helper()
	db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
	if err != nil {
		return nil, func() {}, err
	}
	return test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
}

// consumerLabel is the label secretConsumer gives the resource that holds an
// opaque reference to the generated secret's value.
const consumerLabel = "my-bucket"

// waitUntilRotationDue blocks until the generator's cadence has certainly
// elapsed, so that a sweep which produces no rotation says something about the
// sweep rather than about the clock. It waits two full intervals past the
// derived anchor, which strictly covers the jitter (a tenth of the interval).
//
// Without this a "rotation was refused" assertion passes on a generator that
// was simply not due yet, which is the same result for the wrong reason.
func waitUntilRotationDue(t *testing.T, m *metastructure.Metastructure, stack, label string) {
	t.Helper()
	infos, err := m.Datastore.GetGeneratorsWithRotation()
	require.NoError(t, err)
	for _, info := range infos {
		if info.Label != label || info.StackLabel != stack {
			continue
		}
		require.False(t, info.LastRotationAt.IsZero(),
			"precondition: the generator must have a committed draw to measure from")
		interval := time.Duration(info.IntervalSeconds) * time.Second
		if wait := time.Until(info.LastRotationAt.Add(2 * interval)); wait > 0 {
			time.Sleep(wait)
		}
		return
	}
	t.Fatalf("no rotation info for generator %q on stack %q", label, stack)
}

// A rotation is an ordinary update, so it confronts drift rather than writing
// over it. Both halves of a generated credential's blast radius are covered:
// the destination formae writes the value into, and a resource beside it that
// consumes the same secret.
func TestGeneratorRotation_RefusesADriftedStack(t *testing.T) {
	for _, drifted := range []string{"alpha", consumerLabel} {
		t.Run("drifted_"+drifted, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				delivered := newDeliveredValues()

				cfg := test_helpers.NewTestMetastructureConfig()
				cfg.Agent.Synchronization.Enabled = false
				m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
				defer def()
				require.NoError(t, err)

				stack := "test-stack-" + util.NewID()
				_, err = m.ApplyForma(driftableRotationForma(t, stack),
					&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
				require.NoError(t, err)
				waitForApplyComplete(t, m)
				require.Len(t, delivered.createdWith("alpha"), 1, "precondition: the destination was drawn for")

				// A patch records a modification since the last reconcile,
				// which is the drift a soft reconcile confronts.
				driftStack(t, m, stack, drifted)
				waitUntilRotationDue(t, m, stack, "db-password")

				for i := 0; i < 10; i++ {
					sweepRotations(t, m)
					time.Sleep(100 * time.Millisecond)
				}
				waitForApplyComplete(t, m)

				assert.Empty(t, rotationCommands(t, m),
					"rotation must refuse while %s carries out-of-band change", drifted)
				assert.Empty(t, delivered.updatedWith("alpha"),
					"a refused rotation must not write the destination")
			})
		})
	}
}

// A stack carrying an auto-reconcile policy has already opted in to having
// out-of-band change overwritten, so rotation proceeds there rather than
// asking again.
func TestGeneratorRotation_ProceedsOnADriftedAutoReconciledStack(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, delivered.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := driftableRotationForma(t, stack)
		// A long interval, so the auto-reconciler itself does not run inside
		// the test window: the policy is here as the operator's standing
		// instruction, not to have it act.
		forma.Stacks[0].Policies = []json.RawMessage{
			json.RawMessage(`{"Type":"auto-reconcile","Label":"stay-level","IntervalSeconds":86400}`),
		}

		_, err = m.ApplyForma(forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		require.Len(t, delivered.createdWith("alpha"), 1, "precondition: the destination was drawn for")

		policies, err := m.Datastore.GetStacksWithAutoReconcilePolicy()
		require.NoError(t, err)
		var attached bool
		for _, policy := range policies {
			if policy.StackLabel == stack {
				attached = true
			}
		}
		require.True(t, attached, "precondition: the stack carries an auto-reconcile policy")

		driftStack(t, m, stack, consumerLabel)

		rotation := sweepUntilRotated(t, m)
		assert.Equal(t, forma_command.CommandStateSuccess, rotation.State,
			"rotation must proceed on a stack whose auto-reconcile policy already overwrites drift")
		waitForApplyComplete(t, m)
		assert.Len(t, delivered.updatedWith("alpha"), 1,
			"the destination must be rewritten with the rotated credential")
	})
}

// driftableRotationForma is a rotating generator, the secret its value lands
// in, and a consumer holding an opaque reference to that secret's property.
// Either resource can be moved out of band to produce the drift a rotation has
// to confront: the source the credential is written into, and a consumer of the
// same credential.
//
// The consumer is not part of a rotation command — a rotation plans the
// generator's own destinations — which is exactly why drift on it has to be
// confronted rather than silently written past.
func driftableRotationForma(t *testing.T, stack string) *pkgmodel.Forma {
	t.Helper()
	return &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: stack}},
		Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		Generators: []json.RawMessage{rotatingGenerator(t, "db-password", stack, 1)},
		Resources: []pkgmodel.Resource{
			genBoundSecret(stack, "alpha", "db-password", "value"),
			secretConsumer(stack, "alpha"),
		},
	}
}

// driftStack moves one resource out of band, through a patch apply: the patch
// records a modification since the last reconcile, which is exactly the drift
// a soft reconcile refuses to write over.
func driftStack(t *testing.T, m *metastructure.Metastructure, stack, label string) {
	t.Helper()
	var patched pkgmodel.Resource
	if label == consumerLabel {
		patched = secretConsumer(stack, "alpha")
		patched.Properties = json.RawMessage(strings.Replace(string(patched.Properties),
			`"AccessControl": "Private"`, `"AccessControl": "PublicRead"`, 1))
	} else {
		patched = genBoundSecret(stack, label, "db-password", "value")
		patched.Properties = json.RawMessage(strings.Replace(string(patched.Properties),
			`"Name": "`+label+`"`, `"Name": "`+label+`-moved-out-of-band"`, 1))
	}

	_, err := m.ApplyForma(&pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: stack}},
		Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		Resources: []pkgmodel.Resource{patched},
	}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "test-client-id", "", "")
	require.NoError(t, err)
	waitForApplyComplete(t, m)

	modifications, err := m.Datastore.GetResourceModificationsSinceLastReconcile(stack)
	require.NoError(t, err)
	require.NotEmpty(t, modifications, "precondition: the patch must record out-of-band movement on %s", label)
}

// failingUpdatesFor wraps a fake secret store so that the provider Update for
// the named labels fails. Creates still succeed, so the destinations exist and
// hold a credential before the rotation is attempted.
func failingUpdatesFor(delivered *deliveredValues, labels ...string) *plugin.ResourcePluginOverrides {
	failing := map[string]bool{}
	for _, label := range labels {
		failing[label] = true
	}
	overrides := delivered.overrides()
	succeed := overrides.Update
	overrides.Update = func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
		label := gjson.ParseBytes(req.DesiredProperties).Get("Name").String()
		if failing[label] {
			return nil, errors.New("the provider refused the write")
		}
		return succeed(req)
	}
	return overrides
}

// rotationAnchorFor returns the instant the generator's cadence is currently
// measured from, as the scheduler derives it.
func rotationAnchorFor(t *testing.T, m *metastructure.Metastructure, stack, label string) time.Time {
	t.Helper()
	infos, err := m.Datastore.GetGeneratorsWithRotation()
	require.NoError(t, err)
	for _, info := range infos {
		if info.Label == label && info.StackLabel == stack {
			return info.LastRotationAt
		}
	}
	t.Fatalf("no rotation info for generator %q on stack %q", label, stack)
	return time.Time{}
}

// The cadence advances on one milestone and one only: every authority-side
// update in the rotation succeeded. An authority update that fails leaves the
// cadence measured from the previous success, so the credential is still owed
// a rotation.
//
// The generation is asserted to have moved while the cadence stood still,
// which is what separates this milestone from the draw. An implementation that
// advanced the cadence when the value was drawn would satisfy every other
// assertion here.
func TestGeneratorRotation_AFailedAuthorityUpdateDoesNotAdvanceTheCadence(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, failingUpdatesFor(delivered, "alpha"), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		_, err = m.ApplyForma(&pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rotatingGenerator(t, "db-password", stack, 1)},
			Resources:  []pkgmodel.Resource{genBoundSecret(stack, "alpha", "db-password", "value")},
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		require.Len(t, delivered.createdWith("alpha"), 1, "precondition: the destination was drawn for")

		anchorBefore := rotationAnchorFor(t, m, stack, "db-password")
		require.False(t, anchorBefore.IsZero(), "precondition: the first apply committed a draw")
		generationBefore, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		digestBefore := storedBinding(t, m, stack, "alpha").Get("$value").String()

		waitUntilRotationDue(t, m, stack, "db-password")
		rotation := sweepUntilRotated(t, m)
		require.Equal(t, forma_command.CommandStateFailed, rotation.State,
			"precondition: the authority-side update must have failed")

		generationAfter, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.NotEqual(t, generationBefore.GenerationID, generationAfter.GenerationID,
			"precondition: the value was drawn, so the generation moved")

		assert.Equal(t, anchorBefore, rotationAnchorFor(t, m, stack, "db-password"),
			"a rotation whose authority update failed must not advance the cadence")
		assert.Equal(t, digestBefore, storedBinding(t, m, stack, "alpha").Get("$value").String(),
			"a failed write must leave the credential at rest where it was")
	})
}

// One destination of several failing is the same milestone, reached from a
// different direction: propagation is committed only when EVERY authority-side
// update succeeded, so a rotation that wrote one of two destinations has not
// committed and must not advance the cadence.
//
// This is a distinct failure mode from a single destination failing: an
// implementation that advanced the cadence as soon as any destination was
// written would pass the single-destination test and fail here.
func TestGeneratorRotation_OneFailedDestinationOfSeveralDoesNotAdvanceTheCadence(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		delivered := newDeliveredValues()

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, failingUpdatesFor(delivered, "beta"), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		_, err = m.ApplyForma(&pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{rotatingGenerator(t, "db-password", stack, 1)},
			Resources: []pkgmodel.Resource{
				genBoundSecret(stack, "alpha", "db-password", "value"),
				genBoundSecret(stack, "beta", "db-password", "value"),
			},
		}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		require.Len(t, delivered.createdWith("alpha"), 1, "precondition: both destinations were drawn for")
		require.Len(t, delivered.createdWith("beta"), 1)

		anchorBefore := rotationAnchorFor(t, m, stack, "db-password")
		require.False(t, anchorBefore.IsZero())

		waitUntilRotationDue(t, m, stack, "db-password")
		rotation := sweepUntilRotated(t, m)
		require.Equal(t, forma_command.CommandStateFailed, rotation.State,
			"precondition: one destination's write must have failed")

		assert.NotEmpty(t, delivered.updatedWith("alpha"),
			"precondition: the other destination WAS written, so this is a partial propagation")
		assert.Empty(t, delivered.updatedWith("beta"))

		assert.Equal(t, anchorBefore, rotationAnchorFor(t, m, stack, "db-password"),
			"a rotation that reached only some of its destinations must not advance the cadence")
	})
}
