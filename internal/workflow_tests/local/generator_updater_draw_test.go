// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// persistedGenerator creates a stack and a password generator on it, and
// returns the generator carrying the KSUID its row holds — the shape the
// executor hands a GeneratorUpdater, where a $gen envelope has already been
// translated to that same KSUID.
func persistedGenerator(t *testing.T, ds datastore.Datastore, stackLabel, label string, length int) *pkgmodel.PasswordGenerator {
	t.Helper()

	stack := &pkgmodel.Stack{Label: stackLabel}
	_, err := ds.CreateStack(stack, "cmd-generator-stack")
	require.NoError(t, err)
	require.NotEmpty(t, stack.ID)

	generator := &pkgmodel.PasswordGenerator{
		Label:                   label,
		Stack:                   stackLabel,
		StackID:                 stack.ID,
		Length:                  length,
		Uppercase:               true,
		Lowercase:               true,
		Digits:                  true,
		RequireEachIncludedType: true,
	}
	_, err = ds.CreateGenerator(generator, "cmd-generator-create")
	require.NoError(t, err)

	identity, err := ds.GetGeneratorIdentity(label, stackLabel)
	require.NoError(t, err)
	require.NotEmpty(t, identity.ID)
	generator.SetID(identity.ID)

	return generator
}

// spawnGeneratorUpdater spawns a GeneratorUpdater on the node under the
// canonical name for a draw, exactly as the ChangesetExecutor does. from is
// the PID that will receive GeneratorUpdateFinished.
func spawnGeneratorUpdater(t *testing.T, node gen.Node, from gen.PID, nodeURI pkgmodel.FormaeURI, commandID string) (gen.PID, error) {
	t.Helper()
	name := actornames.GeneratorUpdater(nodeURI, commandID)
	return node.SpawnRegister(name, generator_update.NewGeneratorUpdater, gen.ProcessOptions{}, from)
}

// A GeneratorUpdater spawned the way the executor spawns it draws a value end
// to end: the spawn itself proves Init reads the node's Datastore env and
// that the argument the executor passes is the shape Init expects, and the
// finished signal proves the handler registration table routes both the start
// message and the draw the actor sends itself.
//
// It also asserts the generation is recorded against the real row, and that
// the actor's process is gone afterwards.
func TestGeneratorUpdater_DrawsAValueAndRecordsItsGeneration(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		const (
			commandID   = "cmd-draw-on-node"
			declaredLen = 24
		)
		stackLabel := "draw-stack-" + util.NewID()
		generator := persistedGenerator(t, m.Datastore, stackLabel, "db-password", declaredLen)

		draw := generator_update.NewDrawGeneratorUpdate(generator, stackLabel)
		updaterPID, err := spawnGeneratorUpdater(t, m.Node, helperPID, draw.NodeURI(), commandID)
		require.NoError(t, err)

		name := actornames.GeneratorUpdater(draw.NodeURI(), commandID)
		require.NoError(t, testutil.Send(m.Node, name,
			generator_update.StartGeneratorUpdate{GeneratorUpdate: draw}))

		var drawnValue string
		testutil.ExpectMessageWithPredicate(t, received, 10*time.Second,
			func(msg generator_update.GeneratorUpdateFinished) bool {
				assert.Equal(t, generator_update.GeneratorUpdateStateSuccess, msg.State,
					"a well-formed generator must draw: %s", msg.ErrorMessage)
				assert.Len(t, msg.DrawnValues["value"], declaredLen,
					"the drawn value must have the declared length")
				drawnValue = msg.DrawnValues["value"]
				return true
			},
		)
		require.NotEmpty(t, drawnValue)

		// The generation is recorded against the generator's real row, under
		// the spec the value was drawn from.
		identity, err := m.Datastore.GetGeneratorIdentityByID(generator.GetID())
		require.NoError(t, err)
		assert.NotEmpty(t, identity.GenerationID,
			"a drawn value must leave a generation recorded against the generator")
		require.NotEmpty(t, identity.GenerationSpec,
			"the generation must record the spec it was drawn under")
		recorded, err := pkgmodel.ParseGenerator(identity.GenerationSpec)
		require.NoError(t, err, "the recorded spec must parse back as a generator")
		recordedPassword, ok := recorded.(*pkgmodel.PasswordGenerator)
		require.True(t, ok)
		assert.Equal(t, declaredLen, recordedPassword.Length)
		assert.Equal(t, "db-password", recordedPassword.Label)

		// The actor shuts itself down once it has reported, so its process
		// must be gone rather than left holding the registered name.
		assert.Eventually(t, func() bool {
			state, stateErr := m.Node.ProcessState(updaterPID)
			return stateErr != nil || state == gen.ProcessStateTerminated || state == gen.ProcessStateZombee
		}, 5*time.Second, 50*time.Millisecond,
			"the GeneratorUpdater must terminate itself after reporting")
	})
}

// The same generator label in two different stacks is two different
// generators. Both must draw, under their own canonical names and against
// their own rows: a name keyed on the label alone would put them on one
// actor and lose a draw.
func TestGeneratorUpdater_SameLabelInTwoStacksBothDraw(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 2)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		const commandID = "cmd-two-stacks"
		firstStack := "first-stack-" + util.NewID()
		secondStack := "second-stack-" + util.NewID()

		first := persistedGenerator(t, m.Datastore, firstStack, "db-password", 20)
		second := persistedGenerator(t, m.Datastore, secondStack, "db-password", 32)
		require.NotEqual(t, first.GetID(), second.GetID(),
			"precondition: the same label in two stacks is two generators")

		for _, draw := range []generator_update.GeneratorUpdate{
			generator_update.NewDrawGeneratorUpdate(first, firstStack),
			generator_update.NewDrawGeneratorUpdate(second, secondStack),
		} {
			_, spawnErr := spawnGeneratorUpdater(t, m.Node, helperPID, draw.NodeURI(), commandID)
			require.NoError(t, spawnErr)
			require.NoError(t, testutil.Send(m.Node, actornames.GeneratorUpdater(draw.NodeURI(), commandID),
				generator_update.StartGeneratorUpdate{GeneratorUpdate: draw}))
		}

		lengths := map[int]bool{}
		for range 2 {
			testutil.ExpectMessageWithPredicate(t, received, 10*time.Second,
				func(msg generator_update.GeneratorUpdateFinished) bool {
					assert.Equal(t, generator_update.GeneratorUpdateStateSuccess, msg.State, msg.ErrorMessage)
					lengths[len(msg.DrawnValues["value"])] = true
					return true
				},
			)
		}
		assert.Equal(t, map[int]bool{20: true, 32: true}, lengths,
			"both generators must draw, each under its own spec")

		for _, generator := range []*pkgmodel.PasswordGenerator{first, second} {
			identity, err := m.Datastore.GetGeneratorIdentityByID(generator.GetID())
			require.NoError(t, err)
			assert.NotEmpty(t, identity.GenerationID,
				"each generator must carry its own recorded generation")
		}
	})
}

// A draw against a generator formae holds no identity for is refused before
// anything is drawn, and reports an operator-facing reason rather than a raw
// error.
func TestGeneratorUpdater_WithoutAnIdentityRefusesToDraw(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		const commandID = "cmd-no-identity"
		anonymous := generator_update.NewDrawGeneratorUpdate(
			&pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: "nowhere", Length: 24,
				Uppercase: true, Lowercase: true, Digits: true,
			},
			"nowhere",
		)

		_, err = spawnGeneratorUpdater(t, m.Node, helperPID, anonymous.NodeURI(), commandID)
		require.NoError(t, err)
		require.NoError(t, testutil.Send(m.Node, actornames.GeneratorUpdater(anonymous.NodeURI(), commandID),
			generator_update.StartGeneratorUpdate{GeneratorUpdate: anonymous}))

		testutil.ExpectMessageWithPredicate(t, received, 10*time.Second,
			func(msg generator_update.GeneratorUpdateFinished) bool {
				assert.Equal(t, generator_update.GeneratorUpdateStateFailed, msg.State)
				assert.Empty(t, msg.DrawnValues, "a refused draw carries no value")
				assert.NotEmpty(t, msg.ErrorMessage)
				return true
			},
		)
	})
}

// The generation spec recorded for a draw is the generator's specification
// and never the value it produced: the column it lands in is not redacted by
// anything downstream.
func TestGeneratorUpdater_RecordedGenerationSpecHoldsNoDrawnValue(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		received := make(chan any, 1)
		helperPID, err := testutil.StartTestHelperActor(m.Node, received)
		require.NoError(t, err)

		const commandID = "cmd-spec-holds-no-value"
		stackLabel := "spec-stack-" + util.NewID()
		generator := persistedGenerator(t, m.Datastore, stackLabel, "db-password", 24)

		draw := generator_update.NewDrawGeneratorUpdate(generator, stackLabel)
		_, err = spawnGeneratorUpdater(t, m.Node, helperPID, draw.NodeURI(), commandID)
		require.NoError(t, err)
		require.NoError(t, testutil.Send(m.Node, actornames.GeneratorUpdater(draw.NodeURI(), commandID),
			generator_update.StartGeneratorUpdate{GeneratorUpdate: draw}))

		var drawnValue string
		testutil.ExpectMessageWithPredicate(t, received, 10*time.Second,
			func(msg generator_update.GeneratorUpdateFinished) bool {
				require.Equal(t, generator_update.GeneratorUpdateStateSuccess, msg.State, msg.ErrorMessage)
				drawnValue = msg.DrawnValues["value"]
				return true
			},
		)
		require.NotEmpty(t, drawnValue)

		identity, err := m.Datastore.GetGeneratorIdentityByID(generator.GetID())
		require.NoError(t, err)
		assert.NotContains(t, string(identity.GenerationSpec), drawnValue,
			"the recorded generation spec must never contain the value drawn under it")

		stored, err := m.Datastore.GetGenerator("db-password", stackLabel)
		require.NoError(t, err)
		require.NotNil(t, stored)
		encoded, err := json.Marshal(stored)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), drawnValue,
			"the generator row must never hold the value drawn from it")
	})
}
