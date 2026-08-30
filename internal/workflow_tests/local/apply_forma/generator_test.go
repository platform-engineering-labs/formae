// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func rawPasswordGenerator(t *testing.T, gen *pkgmodel.PasswordGenerator) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(gen)
	require.NoError(t, err)
	return data
}

// TestMetastructure_ApplyFormaPersistsGeneratorThenReapplyIsANoop is the
// generator lifecycle's end-to-end case: applying a forma that declares a
// generator actually persists it, and re-applying the identical forma
// persists nothing new — connecting Forma.Generators to the datastore is
// exactly what this slice adds; nothing read it before.
func TestMetastructure_ApplyFormaPersistsGeneratorThenReapplyIsANoop(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "generator-e2e-stack"}},
			Generators: []json.RawMessage{
				rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: "generator-e2e-stack",
					Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				}),
			},
		}

		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		var commands []*forma_command.FormaCommand
		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incompleteCommands, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(commands) == 1 && len(incompleteCommands) == 0
		}, 5*time.Second, 100*time.Millisecond)

		require.Len(t, commands, 1)

		stored, err := m.Datastore.GetGenerator("db-password", "generator-e2e-stack")
		require.NoError(t, err)
		require.NotNil(t, stored, "the declared generator must be persisted")
		pw, ok := stored.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, 24, pw.Length)

		// Re-applying the identical forma must be a no-op: no new command
		// changes anything about the generator.
		_, err = m.ApplyForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			commands, err = m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			return len(commands) == 1
		}, 5*time.Second, 100*time.Millisecond, "an unchanged generator must not produce a second command")
	})
}

// TestMetastructure_ApplyFormaSimulate_GeneratorOnlyChangeReportsANonEmptyPlan
// pins the defect this task fixes: a forma whose only change is a generator
// must report ChangesRequired together with a plan that actually shows the
// change, never ChangesRequired with an empty Command. The stack is
// established first with no generator, so the second (simulated) apply's
// only change is the generator, and asserts against the in-memory simulate
// response only — a reloaded command has no GeneratorUpdates column yet.
func TestMetastructure_ApplyFormaSimulate_GeneratorOnlyChangeReportsANonEmptyPlan(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		stack := "generator-only-plan-stack"
		target := "test-target"
		// A stack cannot be created empty, and a generator-only second apply
		// needs something in the stack that stays unchanged across both
		// applies. FakeAWS::Versioned::Parent has no opaque or generator-
		// related fields, so it is a plain anchor resource for this purpose.
		anchorResource := pkgmodel.Resource{
			Label:      "anchor",
			Type:       "FakeAWS::Versioned::Parent",
			Stack:      stack,
			Target:     target,
			Properties: json.RawMessage(`{"Name": "anchor", "Value": "v1"}`),
		}
		baseForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Targets:   []pkgmodel.Target{{Label: target, Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{anchorResource},
		}

		_, err = m.ApplyForma(
			baseForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			incompleteCommands, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(incompleteCommands) == 0
		}, 5*time.Second, 100*time.Millisecond, "the stack-creation command must finish before the generator-only apply")

		formaWithGenerator := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: stack}},
			Targets:   []pkgmodel.Target{{Label: target, Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{anchorResource},
			Generators: []json.RawMessage{
				rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: stack,
					Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				}),
			},
		}

		res, err := m.ApplyForma(
			formaWithGenerator,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true},
			"test-client-id", "", "")
		require.NoError(t, err)

		assert.True(t, res.Simulation.ChangesRequired,
			"a generator-only change must report ChangesRequired")
		assert.Empty(t, res.Simulation.Command.ResourceUpdates,
			"a generator-only apply builds no changeset, so there are no resource updates")
		assert.Empty(t, res.Simulation.Command.StackUpdates,
			"the stack already exists and is otherwise unchanged")
		if assert.Len(t, res.Simulation.Command.GeneratorUpdates, 1,
			"ChangesRequired must never be true against an empty plan") {
			gu := res.Simulation.Command.GeneratorUpdates[0]
			assert.Equal(t, "db-password", gu.GeneratorLabel)
			assert.Equal(t, "create", gu.Operation)
		}
	})
}

// TestMetastructure_SameCommandGeneratorAndConsumer_ShareOneKSUID pins the
// KSUID-threading fix: a generator declared in the SAME command as a
// resource's $gen reference to it must translate to the exact KSUID that
// generator's own GeneratorUpdate carries — not two independently minted
// KSUIDs. Calls FormaCommandFromForma directly (build only, no execution),
// which isolates the wiring bug from plugin execution and from
// generation-drawing (a later slice: no value is drawn here, only the
// generator's identity and the $gen envelope's reference to it).
func TestMetastructure_SameCommandGeneratorAndConsumer_ShareOneKSUID(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		stack := "generator-consumer-wiring-stack"
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Generators: []json.RawMessage{
				rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
					Label: "db-password", Stack: stack,
					Length: 24, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
				}),
			},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{{
				Label:  "db",
				Type:   "FakeAWS::SecretsManager::Secret",
				Stack:  stack,
				Target: "test-target",
				Properties: json.RawMessage(`{
					"Name": "db",
					"SecretString": {"$gen": true, "$label": "db-password", "$stack": "` + stack + `", "$output": "value", "$visibility": "Opaque"}
				}`),
			}},
		}

		fc, err := metastructure.FormaCommandFromForma(
			forma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			pkgmodel.CommandApply,
			m.Datastore,
			"test-client-id", "", "",
			resource_update.FormaCommandSourceUser,
			m.Cfg.Agent.Synchronization.Interval,
		)
		require.NoError(t, err)

		require.Len(t, fc.GeneratorUpdates, 1)
		require.Len(t, fc.ResourceUpdates, 1)

		generatorKsuid := fc.GeneratorUpdates[0].Generator.GetID()
		require.NotEmpty(t, generatorKsuid, "the generator update must carry the KSUID translation assigned")

		embedded := gjson.GetBytes(fc.ResourceUpdates[0].DesiredState.Properties, "SecretString.$generator").String()
		assert.Equal(t, generatorKsuid, embedded,
			"the resource's $generator must equal the generator update's own KSUID, not an independently minted one")
	})
}

// generatorOnlyFixture stands up a metastructure with a stack that already
// exists and holds one resource that never changes, so a later apply's only
// work is the generator work. Without the anchor the stack itself would be
// created or emptied by the same command, and a stack update carries its own
// command-state recompute, which is exactly what a generator-only command
// does not have.
type generatorOnlyFixture struct {
	m           *metastructure.Metastructure
	stack       string
	anchorValue string
}

func newGeneratorOnlyFixture(t *testing.T) (*generatorOnlyFixture, func()) {
	t.Helper()
	cfg := test_helpers.NewTestMetastructureConfig()
	cfg.Agent.Synchronization.Enabled = false
	m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
	require.NoError(t, err)

	f := &generatorOnlyFixture{
		m:           m,
		stack:       "generator-only-stack",
		anchorValue: "anchor-v1",
	}
	return f, def
}

// forma builds a forma holding the unchanged anchor plus whatever generators
// are given.
func (f *generatorOnlyFixture) forma(generators ...json.RawMessage) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: f.stack}},
		Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		Resources:  []pkgmodel.Resource{secretResource(f.stack, "anchor", f.anchorValue)},
		Generators: generators,
	}
}

func (f *generatorOnlyFixture) apply(t *testing.T, forma *pkgmodel.Forma) {
	t.Helper()
	_, err := f.m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
	require.NoError(t, err)
	waitForApplyComplete(t, f.m)
}

// requireLatestCommandSucceeded asserts the command count and that the newest
// command reached Success. Success, not merely terminal: a command that hangs
// and a command that fails are both bugs, and only one of them is what this
// pins.
func requireLatestCommandSucceeded(t *testing.T, m *metastructure.Metastructure, want int) {
	t.Helper()
	cmds, err := m.Datastore.LoadFormaCommands()
	require.NoError(t, err)
	require.Len(t, cmds, want)
	latest := cmds[0]
	for _, c := range cmds {
		if c.StartTs.After(latest.StartTs) {
			latest = c
		}
	}
	assert.Equal(t, forma_command.CommandStateSuccess, latest.State,
		"a command whose only work is generator work must reach a terminal state")
}

func generatorOfLength(t *testing.T, stack string, length int) json.RawMessage {
	t.Helper()
	return rawPasswordGenerator(t, &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: stack,
		Length: length, Uppercase: true, Lowercase: true, Digits: true, RequireEachIncludedType: true,
	})
}

// Adding a generator to a stack that already exists is a command whose only
// work is generator work. It must reach a terminal state: the generator write
// is durable before the command is even schedulable, so a command left
// NotStarted would never self-heal and would sit incomplete forever.
func TestApplyForma_GeneratorOnlyCreate_ReachesATerminalState(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		f, def := newGeneratorOnlyFixture(t)
		defer def()

		f.apply(t, f.forma())
		f.apply(t, f.forma(generatorOfLength(t, f.stack, 24)))

		requireLatestCommandSucceeded(t, f.m, 2)

		stored, err := f.m.Datastore.GetGenerator("db-password", f.stack)
		require.NoError(t, err)
		require.NotNil(t, stored, "the generator-only command must still have written the generator")
	})
}

// Editing a generator's spec on a stack that already exists is likewise
// generator-only work.
func TestApplyForma_GeneratorOnlyUpdate_ReachesATerminalState(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		f, def := newGeneratorOnlyFixture(t)
		defer def()

		f.apply(t, f.forma(generatorOfLength(t, f.stack, 24)))
		f.apply(t, f.forma(generatorOfLength(t, f.stack, 32)))

		requireLatestCommandSucceeded(t, f.m, 2)

		stored, err := f.m.Datastore.GetGenerator("db-password", f.stack)
		require.NoError(t, err)
		pw, ok := stored.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, 32, pw.Length, "the generator-only command must still have applied the edit")
	})
}

// Removing a generator nobody references is an ordinary edit, and the
// resulting command's only work is the delete.
func TestApplyForma_GeneratorOnlyDelete_ReachesATerminalState(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		f, def := newGeneratorOnlyFixture(t)
		defer def()

		f.apply(t, f.forma(generatorOfLength(t, f.stack, 24)))
		f.apply(t, f.forma())

		requireLatestCommandSucceeded(t, f.m, 2)

		stored, err := f.m.Datastore.GetGenerator("db-password", f.stack)
		require.NoError(t, err)
		assert.Nil(t, stored, "the generator-only command must still have removed the generator")
	})
}

// A command carrying generator work AND resource work keeps going through the
// changeset executor, which owns its terminal state. Finalizing generator work
// early must not pre-empt that.
func TestApplyForma_GeneratorPlusResourceWork_StillRunsTheChangeset(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		f, def := newGeneratorOnlyFixture(t)
		defer def()

		f.apply(t, f.forma())

		f.anchorValue = "anchor-v2"
		f.apply(t, f.forma(generatorOfLength(t, f.stack, 24)))

		requireLatestCommandSucceeded(t, f.m, 2)

		cmds, err := f.m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		mixed := cmds[0]
		for _, c := range cmds {
			if c.StartTs.After(mixed.StartTs) {
				mixed = c
			}
		}
		anchorUpdate := findResourceUpdate(mixed.ResourceUpdates, "anchor")
		require.NotNil(t, anchorUpdate, "the mixed command must carry the anchor's update")
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, anchorUpdate.State,
			"the resource half of a mixed command must still be executed")

		stored, err := f.m.Datastore.GetGenerator("db-password", f.stack)
		require.NoError(t, err)
		require.NotNil(t, stored, "the generator half of a mixed command must still be written")
	})
}
