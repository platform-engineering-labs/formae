// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func genBoundProperties(generatorKsuid string) json.RawMessage {
	return json.RawMessage(`{"password":{"$gen":true,"$generator":"` + generatorKsuid +
		`","$output":"value","$visibility":"Opaque"}}`)
}

// genBoundSecret builds a create op for a resource whose "password" property
// is bound to generatorKsuid.
func genBoundSecret(label, generatorKsuid string) resource_update.ResourceUpdate {
	return resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label:      label,
			Type:       "AWS::SecretsManager::Secret",
			Stack:      "default",
			Ksuid:      util.NewID(),
			Target:     "aws",
			Properties: genBoundProperties(generatorKsuid),
		},
		Operation:  resource_update.OperationCreate,
		State:      resource_update.ResourceUpdateStateNotStarted,
		StackLabel: "default",
	}
}

func drawOp(label, generatorKsuid string) generator_update.GeneratorUpdate {
	return generator_update.NewDrawGeneratorUpdate(
		&pkgmodel.PasswordGenerator{Label: label, Stack: "default", Length: 24, ID: generatorKsuid},
		"default",
	)
}

func resourceNode(t *testing.T, cs Changeset, ru resource_update.ResourceUpdate) *DAGNode {
	t.Helper()
	node := cs.DAG.Nodes[createOperationURI(ru.URI(), ru.Operation)]
	require.NotNil(t, node, "no DAG node for resource %s", ru.DesiredState.Label)
	return node
}

func dependsOn(node *DAGNode, uri pkgmodel.FormaeURI) bool {
	for _, dep := range node.Dependencies {
		if dep.URI == uri {
			return true
		}
	}
	return false
}

// A generator whose value a destination still needs gets its own node, and
// the resource op holding that destination waits for it.
func TestGeneratorDrawGetsANodeAndTheConsumerDependsOnIt(t *testing.T) {
	generatorKsuid := util.NewID()
	secret := genBoundSecret("app-secret", generatorKsuid)
	draw := drawOp("db-password", generatorKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{secret},
		nil,
		[]generator_update.GeneratorUpdate{draw},
		"cmd-draw", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	drawNode := cs.DAG.Nodes[draw.NodeURI()]
	require.NotNil(t, drawNode, "the generator draw must be a DAG node")

	consumer := resourceNode(t, cs, secret)
	assert.True(t, dependsOn(consumer, draw.NodeURI()),
		"the consumer must wait for the draw that produces its value")
	assert.Len(t, drawNode.Dependencies, 0, "a draw waits for nothing")
	require.Len(t, drawNode.Dependents, 1)
	assert.Equal(t, consumer.URI, drawNode.Dependents[0].URI)
}

// A destination whose occurrence classified stable already holds the value
// the generator's current generation produced. It must not be wired to the
// draw: an edge would deliver a freshly drawn value over a credential that
// nothing asked to rotate.
func TestStableDestinationIsNotWiredToTheDraw(t *testing.T) {
	generatorKsuid := util.NewID()

	stable := genBoundSecret("stable-secret", generatorKsuid)
	stable.Operation = resource_update.OperationUpdate
	stable.ProvenanceRecords = []resource_update.OccurrenceRecord{{
		DestinationPath: "password",
		DesiredIdentity: resource_update.OccurrenceIdentity{
			Kind: resource_update.OccurrenceKindGenerator, Ksuid: generatorKsuid, PropertyPath: "value",
		},
		Class: resource_update.OccurrenceStable,
	}}

	fresh := genBoundSecret("fresh-secret", generatorKsuid)
	draw := drawOp("db-password", generatorKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{stable, fresh},
		nil,
		[]generator_update.GeneratorUpdate{draw},
		"cmd-stable", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	assert.False(t, dependsOn(resourceNode(t, cs, stable), draw.NodeURI()),
		"a stable destination must not be wired to the draw")
	assert.True(t, dependsOn(resourceNode(t, cs, fresh), draw.NodeURI()),
		"a destination that still needs a value must be wired to the draw")

	drawNode := cs.DAG.Nodes[draw.NodeURI()]
	require.NotNil(t, drawNode)
	assert.Len(t, drawNode.Dependents, 1, "only the destination that needs a value depends on the draw")
}

// Two generators are two independent producers: each consumer waits only for
// the generator its own destination names.
func TestTwoGeneratorsProduceIndependentSubgraphs(t *testing.T) {
	firstKsuid := util.NewID()
	secondKsuid := util.NewID()

	firstConsumer := genBoundSecret("db-secret", firstKsuid)
	secondConsumer := genBoundSecret("api-secret", secondKsuid)
	firstDraw := drawOp("db-password", firstKsuid)
	secondDraw := drawOp("api-key", secondKsuid)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{firstConsumer, secondConsumer},
		nil,
		[]generator_update.GeneratorUpdate{firstDraw, secondDraw},
		"cmd-two", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.NoError(t, err)

	require.NotNil(t, cs.DAG.Nodes[firstDraw.NodeURI()])
	require.NotNil(t, cs.DAG.Nodes[secondDraw.NodeURI()])

	first := resourceNode(t, cs, firstConsumer)
	second := resourceNode(t, cs, secondConsumer)

	assert.True(t, dependsOn(first, firstDraw.NodeURI()))
	assert.False(t, dependsOn(first, secondDraw.NodeURI()))
	assert.True(t, dependsOn(second, secondDraw.NodeURI()))
	assert.False(t, dependsOn(second, firstDraw.NodeURI()))
}

// The full-graph cycle re-check runs after the generator edges are in place,
// so a cycle formed by target-resolvable edges is still rejected rather than
// hanging the executor.
func TestCycleIsStillDetectedWithGeneratorNodesPresent(t *testing.T) {
	const (
		targetA = "target-a"
		targetB = "target-b"
	)
	resOnA := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")
	resOnB := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	generatorKsuid := util.NewID()
	secretOnA := genBoundSecret("secret-on-a", generatorKsuid)
	secretOnA.DesiredState.Ksuid = resOnA.KSUID()
	secretOnA.DesiredState.Target = targetA

	secretOnB := genBoundSecret("secret-on-b", generatorKsuid)
	secretOnB.DesiredState.Ksuid = resOnB.KSUID()
	secretOnB.DesiredState.Target = targetB

	targetUpdates := []target_update.TargetUpdate{
		{
			Target:               pkgmodel.Target{Label: targetA, Namespace: "AWS", Config: opaqueRefConfig(string(resOnB))},
			Operation:            target_update.TargetOperationCreate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{resOnB},
		},
		{
			Target:               pkgmodel.Target{Label: targetB, Namespace: "AWS", Config: opaqueRefConfig(string(resOnA))},
			Operation:            target_update.TargetOperationCreate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{resOnA},
		},
	}

	_, err := NewChangeset(
		[]resource_update.ResourceUpdate{secretOnA, secretOnB},
		targetUpdates,
		[]generator_update.GeneratorUpdate{drawOp("db-password", generatorKsuid)},
		"cmd-cycle", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.Error(t, err, "mutually referencing in-command targets form a cycle and must be rejected, not hang")
}

// stubGeneratorSpecs answers GetGenerator from a (label, stack) keyed map.
// Its generators carry no ID, exactly as a datastore load does.
type stubGeneratorSpecs struct {
	byKey map[pkgmodel.GeneratorKey]pkgmodel.Generator
}

func (s *stubGeneratorSpecs) GetGenerator(label, stackLabel string) (pkgmodel.Generator, error) {
	return s.byKey[pkgmodel.GeneratorKey{Label: label, Stack: stackLabel}], nil
}

// A resource newly bound to a generator whose own spec is untouched must
// still reach a draw. The generator produces no row update, so nothing about
// the generator diff would put it in the graph; the destination is what puts
// it there, and without the edge the new consumer would dispatch its $gen
// envelope undrawn.
func TestSecondConsumerOnAnUnchangedGeneratorIsWiredToADraw(t *testing.T) {
	generatorKsuid := util.NewID()

	appliedConsumer := genBoundSecret("app-secret", generatorKsuid)
	appliedConsumer.Operation = resource_update.OperationUpdate
	appliedConsumer.ProvenanceRecords = []resource_update.OccurrenceRecord{{
		DestinationPath: "password",
		DesiredIdentity: resource_update.OccurrenceIdentity{
			Kind: resource_update.OccurrenceKindGenerator, Ksuid: generatorKsuid, PropertyPath: "value",
		},
		Class: resource_update.OccurrenceStable,
	}}

	newConsumer := genBoundSecret("worker-secret", generatorKsuid)
	resourceUpdates := []resource_update.ResourceUpdate{appliedConsumer, newConsumer}

	draws, err := generator_update.SynthesizeDrawGeneratorUpdates(
		resourceUpdates,
		nil, // the generator's spec is unchanged: no GeneratorUpdate exists
		map[pkgmodel.GeneratorKey]string{{Label: "db-password", Stack: "default"}: generatorKsuid},
		&stubGeneratorSpecs{byKey: map[pkgmodel.GeneratorKey]pkgmodel.Generator{
			{Label: "db-password", Stack: "default"}: &pkgmodel.PasswordGenerator{
				Label: "db-password", Stack: "default", Length: 24,
			},
		}},
	)
	require.NoError(t, err)
	require.Len(t, draws, 1)

	cs, err := NewChangeset(resourceUpdates, nil, draws, "cmd-second-consumer",
		pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)

	drawNode := cs.DAG.Nodes[draws[0].NodeURI()]
	require.NotNil(t, drawNode, "an unchanged generator with a new consumer must still get a node")
	assert.True(t, dependsOn(resourceNode(t, cs, newConsumer), draws[0].NodeURI()),
		"the newly bound consumer must wait for the draw")
	assert.False(t, dependsOn(resourceNode(t, cs, appliedConsumer), draws[0].NodeURI()),
		"the already-applied consumer must keep the value it holds")
}

// A draw carrying no generator identity cannot be matched to the
// destinations that need its value, which would leave them dispatching an
// undrawn envelope. It is rejected at build time instead.
func TestDrawWithoutGeneratorIdentityIsRejected(t *testing.T) {
	generatorKsuid := util.NewID()
	anonymous := generator_update.NewDrawGeneratorUpdate(
		&pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 24},
		"default",
	)

	_, err := NewChangeset(
		[]resource_update.ResourceUpdate{genBoundSecret("app-secret", generatorKsuid)},
		nil,
		[]generator_update.GeneratorUpdate{anonymous},
		"cmd-anonymous", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
	)
	require.Error(t, err)
}
