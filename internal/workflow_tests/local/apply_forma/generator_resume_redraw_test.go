// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// recordedCreates keeps EVERY value a destination was dispatched with, in
// order, rather than only the last, so a test can tell one draw from another
// across a restart.
type recordedCreates struct {
	mu      sync.Mutex
	byLabel map[string][]string
}

func newRecordedCreates() *recordedCreates {
	return &recordedCreates{byLabel: map[string][]string{}}
}

func (r *recordedCreates) record(properties json.RawMessage) {
	props := gjson.ParseBytes(properties)
	r.mu.Lock()
	defer r.mu.Unlock()
	label := props.Get("Name").String()
	r.byLabel[label] = append(r.byLabel[label], props.Get("SecretString").String())
}

func (r *recordedCreates) valuesFor(label string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.byLabel[label]...)
}

// gatedGenBoundSecret is a generator-bound secret that also carries a plain
// $res reference to another resource, which puts it downstream of that
// resource in the changeset. Holding the upstream open is how this test parks
// a destination before it is ever dispatched.
func gatedGenBoundSecret(stack, label, generatorLabel, gateLabel string) pkgmodel.Resource {
	res := genBoundSecret(stack, label, generatorLabel, "value")
	res.Properties = json.RawMessage(`{
		"Name": "` + label + `",
		"Description": {
			"$res":      true,
			"$label":    "` + gateLabel + `",
			"$type":     "FakeAWS::SecretsManager::Secret",
			"$stack":    "` + stack + `",
			"$property": "Name"
		},
		"SecretString": {
			"$gen":        true,
			"$label":      "` + generatorLabel + `",
			"$stack":      "` + stack + `",
			"$output":     "value",
			"$visibility": "Opaque"
		}
	}`)
	return res
}

// A command that is interrupted while it still owes destinations a value
// redraws its generator on resume. The value the interrupted run drew was
// never persisted and cannot be replayed, so the only correct resume is a
// fresh draw for exactly what the command still owes: the destinations that
// never dispatched are written with a value that is NOT the one the
// interrupted run produced, they all receive the same one, a destination that
// already finished is left alone, and neither generation survives in
// plaintext.
func TestApplyForma_GeneratorBoundSecret_ResumedCommandRedrawsForWhatItStillOwes(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		recorded := newRecordedCreates()
		const gateNativeID = "native-gate"
		gateProperties := json.RawMessage(`{"Name":"gate","SecretString":"gate-literal"}`)

		// Before the interruption "settled" completes outright while "gate"
		// reports in progress and is polled forever, which parks the two
		// destinations downstream of it before they are ever dispatched. Each
		// resource answers its own create rather than falling through to
		// FakeAWS, because the stalling Status below cannot tell one resource
		// from another.
		stalling := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				recorded.record(req.Properties)
				if gjson.ParseBytes(req.Properties).Get("Name").String() == "settled" {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        "native-settled",
					}}, nil
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
				}}, nil
			},
			Status: func(_ *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
				}}, nil
			},
		}
		// After the restart the gate's in-flight create reports done — a
		// create already dispatched is resumed by polling, never re-issued —
		// and the destinations behind it are dispatched for the first time.
		completing := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				recorded.record(req.Properties)
				return nil, nil
			},
			Status: func(_ *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           gateNativeID,
					ResourceProperties: gateProperties,
				}}, nil
			},
			// The gate was never created through FakeAWS, so FakeAWS cannot
			// read it back. The only read this node makes is the one that
			// resolves the gate's Name for the destinations behind it.
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   string(gateProperties),
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
		// "no-reset" keeps the file alive across the node restart.
		cfg.Agent.Datastore.Sqlite = pkgmodel.SqliteConfig{FilePath: t.TempDir() + "/no-reset-generator-resume.db"}

		db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		require.NoError(t, err)

		m, stop, err := test_helpers.NewTestMetastructureWithEverything(t, stalling, db, cfg)
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		// The gate borrows the bound secret's type, schema and target but
		// declares a literal: it is the thing the owed destinations wait on,
		// not a destination itself.
		gate := genBoundSecret(stack, "gate", "db-password", "value")
		gate.Properties = gateProperties
		forma := &pkgmodel.Forma{
			Stacks:     []pkgmodel.Stack{{Label: stack}},
			Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
			Resources: []pkgmodel.Resource{
				gate,
				genBoundSecret(stack, "settled", "db-password", "value"),
				gatedGenBoundSecret(stack, "owed-one", "db-password", "gate"),
				gatedGenBoundSecret(stack, "owed-two", "db-password", "gate"),
			},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)

		// The interruption point has to be pinned down, not merely likely:
		// "settled" recorded terminal, and the two destinations behind the
		// gate not yet started. A resume that owed nothing, or that owed
		// everything, would prove nothing about a redraw.
		require.Eventually(t, func() bool {
			cmds, loadErr := db.LoadFormaCommands()
			if loadErr != nil || len(cmds) != 1 {
				return false
			}
			settled := findResourceUpdate(cmds[0].ResourceUpdates, "settled")
			owedOne := findResourceUpdate(cmds[0].ResourceUpdates, "owed-one")
			owedTwo := findResourceUpdate(cmds[0].ResourceUpdates, "owed-two")
			return settled != nil && settled.State == resource_update.ResourceUpdateStateSuccess &&
				owedOne != nil && owedOne.State == resource_update.ResourceUpdateStateNotStarted &&
				owedTwo != nil && owedTwo.State == resource_update.ResourceUpdateStateNotStarted
		}, 15*time.Second, 50*time.Millisecond,
			"the command should be parked with one destination written and two still owed")

		settledValues := recorded.valuesFor("settled")
		require.Len(t, settledValues, 1)
		firstDraw := settledValues[0]
		require.Len(t, firstDraw, 24, "the interrupted run must have drawn a value")
		require.Empty(t, recorded.valuesFor("owed-one"), "the owed destinations must not have been dispatched yet")
		require.Empty(t, recorded.valuesFor("owed-two"), "the owed destinations must not have been dispatched yet")

		firstGeneration, err := db.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, firstGeneration.GenerationID, "the interrupted run's draw must have been recorded")

		// Crash the node with the command still in flight.
		stop()

		db, err = dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		require.NoError(t, err)
		incomplete, err := db.LoadIncompleteFormaCommands()
		require.NoError(t, err)
		require.Len(t, incomplete, 1, "the command must still be incomplete after the crash")

		m, def, err := test_helpers.NewTestMetastructureWithEverything(t, completing, db, cfg)
		defer def()
		require.NoError(t, err)

		waitForApplyComplete(t, m)

		cmds, err := db.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 1)
		cmd := cmds[0]
		require.Equal(t, forma_command.CommandStateSuccess, cmd.State,
			"the resumed command must be able to write the destinations it still owed")

		owedOne := recorded.valuesFor("owed-one")
		owedTwo := recorded.valuesFor("owed-two")
		require.Len(t, owedOne, 1, "the resumed command must dispatch the destination it still owed")
		require.Len(t, owedTwo, 1, "the resumed command must dispatch the destination it still owed")

		secondDraw := owedOne[0]
		assert.Len(t, secondDraw, 24, "the resumed command must draw a value at the generator's declared length")
		assert.NotContains(t, secondDraw, "$gen",
			"the provider must receive the drawn value, never the envelope naming it")
		assert.NotEqual(t, firstDraw, secondDraw,
			"the resumed command must redraw: the interrupted run's value was never persisted and cannot be replayed")
		assert.Equal(t, secondDraw, owedTwo[0],
			"every destination the command still owed must receive the same post-restart value")

		assert.Len(t, recorded.valuesFor("settled"), 1,
			"a destination that already reached a terminal state must not be written again")

		secondGeneration, err := db.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.NotEqual(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
			"the redraw must record its own generation")

		// Neither generation may survive anywhere durable.
		assertNoPlaintextInResourceUpdates(t, m, cmd.ID, firstDraw)
		assertNoPlaintextInResourceUpdates(t, m, cmd.ID, secondDraw)
	})
}
