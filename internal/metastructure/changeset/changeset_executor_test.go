// © 2025 Platform Engineering Labs Inc.
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

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestChangesetExecutor_EmptyChangesetFinishesImmediately(t *testing.T) {
	emptyChangeset := Changeset{
		CommandID:      "test-command-empty",
		DAG:            &ExecutionDAG{},
		trackedUpdates: make(map[string]bool),
	}

	executor, sender, err := newChangesetExecutorForTest(t)
	assert.NoError(t, err, "Failed to spawn changeset executor")
	executor.SendMessage(sender, Start{Changeset: emptyChangeset})

	executor.ShouldNotSend().
		Message(resource_update.ResourceUpdate{}).
		Once().
		Assert()
}

func TestChangesetHasUserUpdates_WithUserSource(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationCreate)

	cs := Changeset{
		CommandID: "test",
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				opURI: {
					URI: opURI,
					Update: &resource_update.ResourceUpdate{
						Source: resource_update.FormaCommandSourceUser,
					},
				},
			},
		},
	}
	assert.True(t, changesetHasUserUpdates(cs))
}

func TestChangesetHasUserUpdates_WithSyncSource(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationCreate)

	cs := Changeset{
		CommandID: "test",
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				opURI: {
					URI: opURI,
					Update: &resource_update.ResourceUpdate{
						Source: resource_update.FormaCommandSourceSynchronize,
					},
				},
			},
		},
	}
	assert.False(t, changesetHasUserUpdates(cs))
}

func TestChangesetHasUserUpdates_EmptyChangeset(t *testing.T) {
	cs := Changeset{
		CommandID: "test",
		DAG:       &ExecutionDAG{Nodes: map[pkgmodel.FormaeURI]*DAGNode{}},
	}
	assert.False(t, changesetHasUserUpdates(cs))
}

func TestChangesetHasUserUpdates_TargetUpdatesIgnored(t *testing.T) {
	cs := Changeset{
		CommandID: "test",
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				"target://t": {
					URI: "target://t",
					Update: &target_update.TargetUpdate{
						Target:    pkgmodel.Target{Label: "t", Namespace: "AWS"},
						Operation: target_update.TargetOperationCreate,
						State:     target_update.TargetUpdateStateNotStarted,
					},
				},
			},
		},
	}
	assert.False(t, changesetHasUserUpdates(cs))
}

func TestCollectStacksWithDeletes_DeleteOps(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationDelete)

	dag := &ExecutionDAG{
		Nodes: map[pkgmodel.FormaeURI]*DAGNode{
			opURI: {
				URI: opURI,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationDelete,
					StackLabel: "production",
				},
			},
		},
	}

	stacks := collectStacksWithDeletes(dag)
	require.Len(t, stacks, 1)
	assert.Equal(t, "production", stacks[0])
}

func TestCollectStacksWithDeletes_ReplaceOps(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationReplace)

	dag := &ExecutionDAG{
		Nodes: map[pkgmodel.FormaeURI]*DAGNode{
			opURI: {
				URI: opURI,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationReplace,
					StackLabel: "staging",
				},
			},
		},
	}

	stacks := collectStacksWithDeletes(dag)
	require.Len(t, stacks, 1)
	assert.Equal(t, "staging", stacks[0])
}

func TestCollectStacksWithDeletes_ExcludesUnmanagedAndEmpty(t *testing.T) {
	uri1 := pkgmodel.NewFormaeURI(util.NewID(), "")
	uri2 := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI1 := createOperationURI(uri1, resource_update.OperationDelete)
	opURI2 := createOperationURI(uri2, resource_update.OperationDelete)

	dag := &ExecutionDAG{
		Nodes: map[pkgmodel.FormaeURI]*DAGNode{
			opURI1: {
				URI: opURI1,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationDelete,
					StackLabel: "$unmanaged",
				},
			},
			opURI2: {
				URI: opURI2,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationDelete,
					StackLabel: "",
				},
			},
		},
	}

	stacks := collectStacksWithDeletes(dag)
	assert.Empty(t, stacks)
}

func TestCollectStacksWithDeletes_CreateOpsIgnored(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationCreate)

	dag := &ExecutionDAG{
		Nodes: map[pkgmodel.FormaeURI]*DAGNode{
			opURI: {
				URI: opURI,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationCreate,
					StackLabel: "production",
				},
			},
		},
	}

	stacks := collectStacksWithDeletes(dag)
	assert.Empty(t, stacks)
}

func TestCollectStacksWithDeletes_DeduplicatesStacks(t *testing.T) {
	uri1 := pkgmodel.NewFormaeURI(util.NewID(), "")
	uri2 := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI1 := createOperationURI(uri1, resource_update.OperationDelete)
	opURI2 := createOperationURI(uri2, resource_update.OperationDelete)

	dag := &ExecutionDAG{
		Nodes: map[pkgmodel.FormaeURI]*DAGNode{
			opURI1: {
				URI: opURI1,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationDelete,
					StackLabel: "production",
				},
			},
			opURI2: {
				URI: opURI2,
				Update: &resource_update.ResourceUpdate{
					Operation:  resource_update.OperationDelete,
					StackLabel: "production",
				},
			},
		},
	}

	stacks := collectStacksWithDeletes(dag)
	require.Len(t, stacks, 1)
	assert.Equal(t, "production", stacks[0])
}

func TestCollectStacksWithDeletes_TargetUpdatesIgnored(t *testing.T) {
	dag := &ExecutionDAG{
		Nodes: map[pkgmodel.FormaeURI]*DAGNode{
			"target://t": {
				URI: "target://t",
				Update: &target_update.TargetUpdate{
					Target:    pkgmodel.Target{Label: "t", Namespace: "AWS"},
					Operation: target_update.TargetOperationDelete,
					State:     target_update.TargetUpdateStateNotStarted,
				},
			},
		},
	}

	stacks := collectStacksWithDeletes(dag)
	assert.Empty(t, stacks)
}

// newChangesetExecutorForTest spawns a ChangesetExecutor for testing purposes
func newChangesetExecutorForTest(t *testing.T) (*unit.TestActor, gen.PID, error) {
	sender := gen.PID{Node: "test", ID: 100}

	executor, err := unit.Spawn(t, NewChangesetExecutor, unit.WithArgs(sender), unit.WithLogLevel(gen.LogLevelDebug))
	if err != nil {
		return nil, gen.PID{}, err
	}

	return executor, sender, nil
}

// TestResolveNodeFinished_PropagatesConfigAndSkipsPersist verifies that when
// the executor receives TargetUpdateFinished for a synthetic Resolve op:
//   - the resolved config is propagated onto every dependent resource op, and
//   - MarkTargetUpdateAsComplete is NOT called (the Resolve op is synthetic and
//     has no row in the command's TargetUpdates, so calling it would be incorrect).
func TestResolveNodeFinished_PropagatesConfigAndSkipsPersist(t *testing.T) {
	const targetLabel = "consumer"
	ksuid := util.NewID()

	unresolved := json.RawMessage(`{"apiKey":{"$ref":"formae://secret#/token","$visibility":"Opaque"}}`)
	resolved := json.RawMessage(`{"apiKey":"resolved-credential"}`)

	// Build a changeset: one Resolve op on 'consumer', one resource Create that
	// depends on it (resource op references consumer's target label).
	resourceOp := resource_update.ResourceUpdate{
		DesiredState:   pkgmodel.Resource{Label: "bucket", Type: "FakeAWS::S3::Bucket", Stack: "s", Ksuid: ksuid, Target: targetLabel},
		Operation:      resource_update.OperationCreate,
		State:          resource_update.ResourceUpdateStateNotStarted,
		StackLabel:     "s",
		ResourceTarget: pkgmodel.Target{Label: targetLabel, Namespace: "test", Config: unresolved},
	}

	resolveOp := target_update.NewResolveTargetUpdate(
		pkgmodel.Target{Label: targetLabel, Namespace: "test", Config: unresolved},
		[]pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(util.NewID(), "")},
	)

	cs, err := NewChangeset(
		[]resource_update.ResourceUpdate{resourceOp},
		[]target_update.TargetUpdate{resolveOp},
		"cmd-resolve-propagation",
		pkgmodel.CommandApply,
		nil, // datastore not needed: resolve op already provided explicitly
	)
	require.NoError(t, err)

	// Mark the resolve node as running so the executor treats the completion
	// message as valid (only running nodes are matched by the handler).
	resolveNodeURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	resolveNode := cs.DAG.Nodes[resolveNodeURI]
	require.NotNil(t, resolveNode, "resolve node must exist in DAG")
	resolveNode.Update.MarkInProgress()

	// Pre-track the resource op so AvailableExecutableUpdates skips it during
	// the resume() call that follows handleUpdateFinished. Without this, resume
	// would call the RateLimiter actor — which does not exist in a unit test.
	opURI := createOperationURI(pkgmodel.NewFormaeURI(ksuid, ""), resource_update.OperationCreate)
	cs.trackedUpdates[string(opURI)] = true

	// Obtain a TestProcess so we can inspect what proc.Call emits.
	actor, err := unit.Spawn(t, NewChangesetExecutor, unit.WithArgs(gen.PID{}), unit.WithLogLevel(gen.LogLevelError))
	require.NoError(t, err)
	proc := actor.Process()

	data := ChangesetData{
		changeset:   cs,
		requestedBy: gen.PID{},
	}

	finishedMsg := target_update.TargetUpdateFinished{
		NodeURI:        resolveNodeURI,
		State:          target_update.TargetUpdateStateSuccess,
		ResolvedConfig: resolved,
	}

	// Call the handler directly. Before the fix, it calls MarkTargetUpdateAsComplete
	// unconditionally, emitting a CallEvent. After the fix, no such CallEvent exists.
	_, updatedData, _, _ := targetUpdateFinished(gen.PID{}, StateProcessing, data, finishedMsg, proc)

	// ── Assert propagation ───────────────────────────────────────────────────
	// Every resource op on the consumer target must have received the resolved
	// config after the Resolve node finishes, so the op dispatches the plaintext
	// credential to the plugin instead of the raw $ref object.
	resourceNode := updatedData.changeset.DAG.Nodes[opURI]
	require.NotNil(t, resourceNode, "resource node must exist in DAG after handler ran")
	ru := resourceNode.Update.(*resource_update.ResourceUpdate)
	assert.JSONEq(t, string(resolved), string(ru.ResourceTarget.Config),
		"resource op must carry the resolved config after the Resolve node finishes")

	// ── Assert no persist call ───────────────────────────────────────────────
	// A Resolve op is synthetic: it has no row in command.TargetUpdates and
	// must not trigger MarkTargetUpdateAsComplete.
	for _, event := range actor.Events() {
		callEvent, ok := event.(unit.CallEvent)
		if !ok {
			continue
		}
		_, isMarkComplete := callEvent.Request.(messages.MarkTargetUpdateAsComplete)
		assert.Falsef(t, isMarkComplete,
			"MarkTargetUpdateAsComplete must not be called for a synthetic Resolve op; got %+v", callEvent.Request)
	}
}
