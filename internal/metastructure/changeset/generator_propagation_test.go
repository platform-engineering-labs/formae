// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"encoding/json"
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// changesetWithOneDraw builds a changeset holding a single draw and a single
// create bound to it, with the draw already dispatched (in progress) so the
// executor's finished handler treats the completion as valid.
func changesetWithOneDraw(t *testing.T, commandID string, mode pkgmodel.FormaApplyMode) (Changeset, generator_update.GeneratorUpdate, resource_update.ResourceUpdate) {
	t.Helper()

	generatorKsuid := util.NewID()
	secret := genBoundSecret("app-secret", generatorKsuid)
	draw := drawOp("db-password", generatorKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{secret}, nil,
		[]generator_update.GeneratorUpdate{draw},
		commandID, pkgmodel.CommandApply, mode,
	)
	require.NoError(t, err)

	drawNode := cs.DAG.Nodes[draw.NodeURI()]
	require.NotNil(t, drawNode)
	drawNode.Update.MarkInProgress()

	return cs, draw, secret
}

// testExecutorProcess returns a ChangesetExecutor TestActor and its process,
// for calling the executor's handlers as plain functions.
func testExecutorProcess(t *testing.T) (*unit.TestActor, gen.Process) {
	t.Helper()
	actor, err := unit.Spawn(t, NewChangesetExecutor, unit.WithArgs(gen.PID{}), unit.WithLogLevel(gen.LogLevelError))
	require.NoError(t, err)
	return actor, actor.Process()
}

// The drawn value reaches its destination inside the $gen envelope. The
// envelope is what carries $visibility:"Opaque", and that marker is the only
// reason the persist path hashes the value at rest, so a bare scalar in the
// envelope's place would put a live credential in cleartext into the stored
// properties.
func TestGeneratorDrawFinished_DeliversTheValueInsideTheEnvelope(t *testing.T) {
	cs, draw, secret := changesetWithOneDraw(t, "cmd-draw-delivers", pkgmodel.FormaApplyModeReconcile)
	actor, proc := testExecutorProcess(t)
	_ = actor

	// Pre-track the destination so the resume() that follows does not reach
	// for the RateLimiter actor, which does not exist in a unit test.
	opURI := createOperationURI(secret.URI(), secret.Operation)
	cs.trackedUpdates[string(opURI)] = true

	data := ChangesetData{changeset: cs}
	_, updated, _, _ := generatorUpdateFinished(gen.PID{}, StateProcessing, data,
		generator_update.GeneratorUpdateFinished{
			NodeURI:      draw.NodeURI(),
			State:        generator_update.GeneratorUpdateStateSuccess,
			DrawnValues:  map[string]string{"value": "drawn-credential"},
			GenerationID: "generation-1",
		}, proc)

	node := updated.changeset.DAG.Nodes[opURI]
	require.NotNil(t, node, "the destination must still be schedulable after a successful draw")
	ru := node.Update.(*resource_update.ResourceUpdate)

	envelope := gjson.GetBytes(ru.DesiredState.Properties, "password")
	require.True(t, envelope.IsObject(), "the value must land inside the envelope, not replace it")
	assert.Equal(t, "drawn-credential", envelope.Get("$value").String())
	assert.Equal(t, "Opaque", envelope.Get("$visibility").String())
}

// A successful draw must be recorded as a success on the update itself, or
// UpdateDAG reads it as an unexpected state, treats it as a failure, and
// cascades every destination waiting on it.
func TestGeneratorDrawFinished_MarksTheDrawSucceededAndReleasesItsDestination(t *testing.T) {
	cs, draw, secret := changesetWithOneDraw(t, "cmd-draw-success", pkgmodel.FormaApplyModeReconcile)
	actor, proc := testExecutorProcess(t)
	_ = actor

	opURI := createOperationURI(secret.URI(), secret.Operation)
	cs.trackedUpdates[string(opURI)] = true

	drawUpdate := cs.DAG.Nodes[draw.NodeURI()].Update

	data := ChangesetData{changeset: cs}
	_, updated, _, _ := generatorUpdateFinished(gen.PID{}, StateProcessing, data,
		generator_update.GeneratorUpdateFinished{
			NodeURI:      draw.NodeURI(),
			State:        generator_update.GeneratorUpdateStateSuccess,
			DrawnValues:  map[string]string{"value": "drawn-credential"},
			GenerationID: "generation-1",
		}, proc)

	assert.True(t, drawUpdate.IsSuccess(), "a completed draw must be recorded as a success")
	assert.Nil(t, updated.changeset.DAG.Nodes[draw.NodeURI()], "a succeeded draw leaves the DAG")

	node := updated.changeset.DAG.Nodes[opURI]
	require.NotNil(t, node, "the destination must survive a successful draw")
	assert.False(t, node.Update.IsFailed(), "a successful draw must not fail its destination")
	assert.Empty(t, node.Dependencies, "the destination is released once the draw completes")
}

// A draw that fails must not leave its destinations hanging: they cascade to
// failed and that cascade is persisted, so the command reports what happened
// rather than sitting in progress forever.
func TestGeneratorDrawFailed_CascadesToItsDestinationAndPersistsTheFailure(t *testing.T) {
	cs, draw, secret := changesetWithOneDraw(t, "cmd-draw-failure", pkgmodel.FormaApplyModeReconcile)
	actor, proc := testExecutorProcess(t)

	opURI := createOperationURI(secret.URI(), secret.Operation)
	node := cs.DAG.Nodes[opURI]
	require.NotNil(t, node)
	ru := node.Update.(*resource_update.ResourceUpdate)

	data := ChangesetData{changeset: cs}
	_, updated, _, _ := generatorUpdateFinished(gen.PID{}, StateProcessing, data,
		generator_update.GeneratorUpdateFinished{
			NodeURI:      draw.NodeURI(),
			State:        generator_update.GeneratorUpdateStateFailed,
			ErrorMessage: "cannot draw a value for this generator",
		}, proc)

	assert.True(t, ru.IsFailed(), "a destination whose draw failed must fail rather than dispatch undrawn")
	assert.Nil(t, updated.changeset.DAG.Nodes[opURI], "the failed destination leaves the DAG")
	assert.False(t, gjson.GetBytes(ru.DesiredState.Properties, "password.$value").Exists(),
		"a failed draw writes no value anywhere")

	var marked []forma_persister.ResourceUpdateRef
	for _, event := range actor.Events() {
		callEvent, ok := event.(unit.CallEvent)
		if !ok {
			continue
		}
		if failed, ok := callEvent.Request.(forma_persister.MarkResourcesAsFailed); ok {
			marked = append(marked, failed.Resources...)
		}
	}
	require.Len(t, marked, 1, "the cascaded destination must be recorded as failed")
	assert.Equal(t, secret.URI(), marked[0].URI)
}

// If the drawn value cannot be delivered, the draw is treated as a failure so
// the cascade runs: a destination that never received its value must not go
// on to dispatch its undrawn envelope.
//
// Delivery is driven to refuse through a door that production can open: a
// destination whose $gen sits under a map key containing a dot. The walk that
// selects destinations addresses them by a dot-joined path, so such a
// destination is not addressable and SetGenValues refuses rather than writing
// the credential wherever that path happens to land.
func TestGeneratorDrawFinished_UndeliverableValueFailsClosed(t *testing.T) {
	generatorKsuid := util.NewID()

	unaddressable := genBoundSecret("dotted-key-secret", generatorKsuid)
	unaddressable.DesiredState.Properties = json.RawMessage(`{"labels":{"app.kubernetes.io/secret":{"$gen":true,"$generator":"` +
		generatorKsuid + `","$output":"value","$visibility":"Opaque"}}}`)
	draw := drawOp("db-password", generatorKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{unaddressable}, nil,
		[]generator_update.GeneratorUpdate{draw},
		"cmd-draw-undeliverable", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	opURI := createOperationURI(unaddressable.URI(), unaddressable.Operation)
	node := cs.DAG.Nodes[opURI]
	require.NotNil(t, node)
	require.True(t, dependsOn(node, draw.NodeURI()),
		"precondition: the destination is wired to the draw, so a refusal is what stops it")
	ru := node.Update.(*resource_update.ResourceUpdate)

	drawNode := cs.DAG.Nodes[draw.NodeURI()]
	require.NotNil(t, drawNode)
	drawNode.Update.MarkInProgress()

	actor, proc := testExecutorProcess(t)

	data := ChangesetData{changeset: cs}
	_, updated, _, _ := generatorUpdateFinished(gen.PID{}, StateProcessing, data,
		generator_update.GeneratorUpdateFinished{
			NodeURI:      draw.NodeURI(),
			State:        generator_update.GeneratorUpdateStateSuccess,
			DrawnValues:  map[string]string{"value": "drawn-credential"},
			GenerationID: "generation-1",
		}, proc)

	assert.True(t, ru.IsFailed(), "an undeliverable draw must fail its destinations closed")
	assert.Nil(t, updated.changeset.DAG.Nodes[opURI])
	assert.NotContains(t, string(ru.DesiredState.Properties), "drawn-credential",
		"nothing is written when delivery is refused")

	// The refusal must reach the operator, not only the log: the cascaded
	// destinations are persisted with the structural delivery error as their
	// failure reason, naming the destination — and never the value.
	var reasons []string
	for _, event := range actor.Events() {
		callEvent, ok := event.(unit.CallEvent)
		if !ok {
			continue
		}
		if failed, ok := callEvent.Request.(forma_persister.MarkResourcesAsFailed); ok {
			reasons = append(reasons, failed.FailureReason)
		}
	}
	require.NotEmpty(t, reasons, "the cascaded failure must be persisted")
	assert.Contains(t, reasons[0], "failed to deliver",
		"the persisted reason must carry the delivery refusal, not be empty")
	assert.NotContains(t, reasons[0], "drawn-credential",
		"the persisted reason must never carry the value")
}

// A draw naming no generator delivers nothing. An authored, not yet
// translated envelope carries no $generator either, so an empty ksuid would
// otherwise match every one of them and deliver the credential into all of
// them. The changeset builder already refuses to build such a draw; this is
// the same refusal restated where the write happens.
func TestPropagateDrawnGeneratorValue_WithoutAGeneratorIdentityDeliversNothing(t *testing.T) {
	generatorKsuid := util.NewID()

	authored := genBoundSecret("authored-secret", generatorKsuid)
	authored.DesiredState.Properties = json.RawMessage(
		`{"password":{"$gen":true,"$label":"db-password","$stack":"default","$output":"value","$visibility":"Opaque"}}`)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{authored}, nil,
		[]generator_update.GeneratorUpdate{drawOp("db-password", generatorKsuid)},
		"cmd-no-identity", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	err = cs.DAG.propagateDrawnGeneratorValue("", map[string]string{"value": "drawn-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err, "a draw naming no generator must refuse delivery outright")
	assert.NotContains(t, err.Error(), "drawn-credential")

	ru := cs.DAG.Nodes[createOperationURI(authored.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)
	assert.NotContains(t, string(ru.DesiredState.Properties), "drawn-credential",
		"an untranslated envelope must never receive a credential")
}

// A draw naming no generation delivers nothing. The destination would be
// written with a credential and no provenance, so every later apply would
// read its movement as unknown, plan it, and rotate the credential again.
func TestPropagateDrawnGeneratorValue_WithoutAGenerationDeliversNothing(t *testing.T) {
	generatorKsuid := util.NewID()
	destination := genBoundSecret("db-secret", generatorKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{destination}, nil,
		[]generator_update.GeneratorUpdate{drawOp("db-password", generatorKsuid)},
		"cmd-no-generation", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	err = cs.DAG.propagateDrawnGeneratorValue(generatorKsuid, map[string]string{"value": "drawn-credential"}, "", pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err, "a draw naming no generation must refuse delivery outright")
	assert.NotContains(t, err.Error(), "drawn-credential")

	ru := cs.DAG.Nodes[createOperationURI(destination.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)
	assert.NotContains(t, string(ru.DesiredState.Properties), "drawn-credential",
		"nothing is written when the generation cannot be attested")
}

// A destination being torn down never receives a drawn value: a delete
// carries the stored envelope and writes nothing, so delivering there would
// put a live credential into a row about to be removed.
func TestPropagateDrawnGeneratorValue_SkipsATeardownDestination(t *testing.T) {
	generatorKsuid := util.NewID()

	teardown := genBoundSecret("dying-secret", generatorKsuid)
	teardown.Operation = resource_update.OperationDelete
	teardown.PriorState = teardown.DesiredState

	survivor := genBoundSecret("live-secret", generatorKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{teardown, survivor}, nil,
		[]generator_update.GeneratorUpdate{drawOp("db-password", generatorKsuid)},
		"cmd-teardown", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	require.NoError(t, cs.DAG.propagateDrawnGeneratorValue(generatorKsuid, map[string]string{"value": "drawn-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile))

	dying := cs.DAG.Nodes[createOperationURI(teardown.URI(), resource_update.OperationDelete)].Update.(*resource_update.ResourceUpdate)
	assert.False(t, gjson.GetBytes(dying.DesiredState.Properties, "password.$value").Exists(),
		"a destination being torn down must never receive a drawn value")

	live := cs.DAG.Nodes[createOperationURI(survivor.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)
	assert.Equal(t, "drawn-credential", gjson.GetBytes(live.DesiredState.Properties, "password.$value").String())
}

// The value is delivered only to destinations naming the generator that drew.
func TestPropagateDrawnGeneratorValue_LeavesAnotherGeneratorsDestinationAlone(t *testing.T) {
	firstKsuid := util.NewID()
	secondKsuid := util.NewID()

	first := genBoundSecret("db-secret", firstKsuid)
	second := genBoundSecret("api-secret", secondKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{first, second}, nil,
		[]generator_update.GeneratorUpdate{drawOp("db-password", firstKsuid), drawOp("api-key", secondKsuid)},
		"cmd-two-draws", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	require.NoError(t, cs.DAG.propagateDrawnGeneratorValue(firstKsuid, map[string]string{"value": "first-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile))

	firstRU := cs.DAG.Nodes[createOperationURI(first.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)
	secondRU := cs.DAG.Nodes[createOperationURI(second.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)

	assert.Equal(t, "first-credential", gjson.GetBytes(firstRU.DesiredState.Properties, "password.$value").String())
	assert.False(t, gjson.GetBytes(secondRU.DesiredState.Properties, "password.$value").Exists(),
		"a destination bound to a different generator must not receive this draw")
}

// Delivery re-derives the destination's patch under the changeset's own apply
// mode. Under reconcile an EntitySet member dropped from the desired state
// keeps its remove op; regenerating under patch semantics would drop it.
func TestPropagateDrawnGeneratorValue_RegeneratesUnderTheChangesetsMode(t *testing.T) {
	generatorKsuid := util.NewID()

	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "password", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	newUpdate := func() resource_update.ResourceUpdate {
		ksuid := util.NewID()
		return resource_update.ResourceUpdate{
			Operation:  resource_update.OperationUpdate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StackLabel: "default",
			PriorState: pkgmodel.Resource{
				Label: "app-secret", Type: "AWS::SecretsManager::Secret", Stack: "default",
				Ksuid: ksuid, Target: "aws", Schema: schema,
				Properties: json.RawMessage(`{"Name":"app-secret","password":"old","Tags":[{"Key":"env","Value":"prod"},{"Key":"legacy","Value":"true"}]}`),
			},
			DesiredState: pkgmodel.Resource{
				Label: "app-secret", Type: "AWS::SecretsManager::Secret", Stack: "default",
				Ksuid: ksuid, Target: "aws", Schema: schema,
				Properties: json.RawMessage(`{"Name":"app-secret","password":{"$gen":true,"$generator":"` + generatorKsuid + `","$output":"value","$visibility":"Opaque"},"Tags":[{"Key":"env","Value":"prod"}]}`),
			},
		}
	}

	build := func(mode pkgmodel.FormaApplyMode) *resource_update.ResourceUpdate {
		ru := newUpdate()
		cs, err := NewChangeset(
			[]resource_update.ResourceUpdate{ru}, nil,
			[]generator_update.GeneratorUpdate{drawOp("db-password", generatorKsuid)},
			"cmd-mode-"+string(mode), pkgmodel.CommandApply, mode,
		)
		require.NoError(t, err)
		require.NoError(t, cs.DAG.propagateDrawnGeneratorValue(generatorKsuid, map[string]string{"value": "drawn"}, "generation-1", cs.Mode))
		return cs.DAG.Nodes[createOperationURI(ru.URI(), resource_update.OperationUpdate)].Update.(*resource_update.ResourceUpdate)
	}

	reconciled := build(pkgmodel.FormaApplyModeReconcile)
	assert.Contains(t, string(reconciled.DesiredState.PatchDocument), "remove",
		"reconcile delivery must keep the remove op planning derived")

	patched := build(pkgmodel.FormaApplyModePatch)
	assert.NotContains(t, string(patched.DesiredState.PatchDocument), "remove",
		"patch-mode delivery must leave the undeclared member unchanged")
}

// A draw that becomes executable is dispatched to a GeneratorUpdater spawned
// under the canonical name, carrying the draw itself.
func TestStartUpdates_DispatchesADrawToAGeneratorUpdater(t *testing.T) {
	const commandID = "cmd-dispatch"
	generatorKsuid := util.NewID()
	draw := drawOp("db-password", generatorKsuid)

	actor, proc := testExecutorProcess(t)

	require.NoError(t, startUpdates([]Update{&draw}, commandID, pkgmodel.FormaApplyModeReconcile, proc))

	expectedName := actornames.GeneratorUpdater(draw.NodeURI(), commandID)

	var dispatched *generator_update.StartGeneratorUpdate
	var dispatchedTo gen.ProcessID
	for _, event := range actor.Events() {
		sendEvent, ok := event.(unit.SendEvent)
		if !ok {
			continue
		}
		if start, ok := sendEvent.Message.(generator_update.StartGeneratorUpdate); ok {
			dispatched = &start
			dispatchedTo, _ = sendEvent.To.(gen.ProcessID)
		}
	}
	require.NotNil(t, dispatched, "a draw must be dispatched to a GeneratorUpdater")
	assert.Equal(t, expectedName, dispatchedTo.Name,
		"the draw must go to the actor registered under the canonical generator-updater name")
	assert.Equal(t, draw.NodeURI(), dispatched.GeneratorUpdate.NodeURI())
}

// Every draw the synthesis produces has at least one destination waiting on
// it, whatever shape that destination takes. Both sides read the same
// classification over the same updates, and the edge builder considers a
// superset of the ops the synthesis does — it does not skip a teardown.
//
// This is the invariant a failed draw's reporting rests on. A draw writes no
// row and has no entry in the command record, so a draw failure is observable
// only through the destinations it cascades to; a draw with no destination
// would fail invisibly and leave the command reporting success.
func TestEverySynthesizedDrawHasADestinationWaitingOnIt(t *testing.T) {
	generatorKsuid := util.NewID()
	lookup := stubGeneratorLookup(map[string]pkgmodel.Generator{
		generatorKsuid: &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 24},
	})

	stable := genBoundSecret("stable-secret", generatorKsuid)
	stable.Operation = resource_update.OperationUpdate
	stable.ProvenanceRecords = []resource_update.OccurrenceRecord{{
		DestinationPath: "password",
		DesiredIdentity: resource_update.OccurrenceIdentity{
			Kind: resource_update.OccurrenceKindGenerator, Ksuid: generatorKsuid, PropertyPath: "value",
		},
		Class: resource_update.OccurrenceStable,
	}}

	teardown := genBoundSecret("dying-secret", generatorKsuid)
	teardown.Operation = resource_update.OperationDelete
	teardown.PriorState = teardown.DesiredState

	fresh := genBoundSecret("fresh-secret", generatorKsuid)

	replaced := genBoundSecret("replaced-secret", generatorKsuid)
	replaced.Operation = resource_update.OperationReplace

	cases := []struct {
		name    string
		updates []resource_update.ResourceUpdate
	}{
		{"a create", []resource_update.ResourceUpdate{fresh}},
		{"a replace, split into a delete and a create", []resource_update.ResourceUpdate{replaced}},
		{"a stable destination beside a fresh one", []resource_update.ResourceUpdate{stable, fresh}},
		{"a teardown beside a fresh one", []resource_update.ResourceUpdate{teardown, fresh}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draws, err := generator_update.SynthesizeDrawGeneratorUpdates(tc.updates, nil, lookup)
			require.NoError(t, err)
			require.Len(t, draws, 1, "a destination that still needs a value produces a draw")

			cs, err := NewChangeset(tc.updates, nil, draws, "cmd-invariant",
				pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
			require.NoError(t, err)

			drawNode := cs.DAG.Nodes[draws[0].NodeURI()]
			require.NotNil(t, drawNode)
			assert.NotEmpty(t, drawNode.Dependents,
				"a draw with no destination would fail invisibly: nothing else records its state")
		})
	}

	// The other half of the rule: nothing to deliver to means no draw at all,
	// so there is no node whose failure could go unreported.
	draws, err := generator_update.SynthesizeDrawGeneratorUpdates(
		[]resource_update.ResourceUpdate{stable, teardown}, nil, lookup)
	require.NoError(t, err)
	assert.Empty(t, draws, "a generator no destination needs a value from draws nothing")
}

// A multi-output draw delivers each destination the output its envelope
// names. One string fanned to both halves of a key pair would apply cleanly
// with the wrong material in one of them, which is the failure output-aware
// delivery exists to prevent.
func TestPropagateDrawnGeneratorValue_SelectsOutputsPerDestination(t *testing.T) {
	generatorKsuid := util.NewID()

	private := genBoundSecret("private-half", generatorKsuid)
	private.DesiredState.Properties = json.RawMessage(`{"password":{"$gen":true,"$generator":"` +
		generatorKsuid + `","$output":"privateKey","$visibility":"Opaque"}}`)
	public := genBoundSecret("public-half", generatorKsuid)
	public.DesiredState.Properties = json.RawMessage(`{"password":{"$gen":true,"$generator":"` +
		generatorKsuid + `","$output":"publicKey","$visibility":"Opaque"}}`)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{private, public}, nil,
		[]generator_update.GeneratorUpdate{drawOp("id-key", generatorKsuid)},
		"cmd-two-outputs", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	require.NoError(t, cs.DAG.propagateDrawnGeneratorValue(generatorKsuid,
		map[string]string{"privateKey": "PRIVATE-PEM", "publicKey": "PUBLIC-PEM"},
		"generation-1", pkgmodel.FormaApplyModeReconcile))

	privateRU := cs.DAG.Nodes[createOperationURI(private.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)
	publicRU := cs.DAG.Nodes[createOperationURI(public.URI(), resource_update.OperationCreate)].Update.(*resource_update.ResourceUpdate)
	assert.Equal(t, "PRIVATE-PEM", gjson.GetBytes(privateRU.DesiredState.Properties, "password.$value").String())
	assert.Equal(t, "PUBLIC-PEM", gjson.GetBytes(publicRU.DesiredState.Properties, "password.$value").String())
}

// A refusal at any destination leaves every destination untouched, whatever
// order the walk visited them in. DAG ordering happens to keep half-delivered
// nodes undispatchable, but credential delivery must not lean on that:
// delivery is prepared for every destination before any is mutated.
func TestPropagateDrawnGeneratorValue_RefusalMutatesNoDestination(t *testing.T) {
	generatorKsuid := util.NewID()

	deliverable := genBoundSecret("deliverable", generatorKsuid)
	refusing := genBoundSecret("refusing", generatorKsuid)
	refusing.DesiredState.Properties = json.RawMessage(`{"password":{"$gen":true,"$generator":"` +
		generatorKsuid + `","$output":"privateKey","$visibility":"Opaque"}}`)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{deliverable, refusing}, nil,
		[]generator_update.GeneratorUpdate{drawOp("db-password", generatorKsuid)},
		"cmd-refusal-atomic", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	before := string(cs.DAG.Nodes[createOperationURI(deliverable.URI(), resource_update.OperationCreate)].
		Update.(*resource_update.ResourceUpdate).DesiredState.Properties)

	err = cs.DAG.propagateDrawnGeneratorValue(generatorKsuid,
		map[string]string{"value": "drawn-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err, "a destination naming an output the draw lacks must refuse the delivery")
	assert.Contains(t, err.Error(), "privateKey", "the refusal must name the output")
	assert.NotContains(t, err.Error(), "drawn-credential")

	after := string(cs.DAG.Nodes[createOperationURI(deliverable.URI(), resource_update.OperationCreate)].
		Update.(*resource_update.ResourceUpdate).DesiredState.Properties)
	assert.Equal(t, before, after,
		"the deliverable destination must be untouched when a sibling refuses")
	assert.NotContains(t, after, "drawn-credential")
}
