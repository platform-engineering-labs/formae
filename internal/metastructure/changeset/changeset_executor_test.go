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

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
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

// changesetWithInProgressResource builds a single-node changeset whose resource
// update is already InProgress. Such a node is not "ready" (only NotStarted
// updates are), so resume() skips it without calling the RateLimiter, letting a
// Start drive the executor to StateProcessing in a unit test. A subsequent
// non-force Cancel then finds one in-progress resource and moves to
// StateCanceling.
func changesetWithInProgressResource(commandID string) Changeset {
	res := pkgmodel.Resource{Ksuid: util.NewID()}
	opURI := createOperationURI(res.URI(), resource_update.OperationCreate)
	return Changeset{
		CommandID: commandID,
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				opURI: {
					URI: opURI,
					Update: &resource_update.ResourceUpdate{
						DesiredState: res,
						Operation:    resource_update.OperationCreate,
						State:        resource_update.ResourceUpdateStateInProgress,
						StackLabel:   "default",
						Source:       resource_update.FormaCommandSourceSynchronize,
					},
				},
			},
		},
		trackedUpdates: make(map[string]bool),
	}
}

// A Resume timer scheduled while the changeset was executing can fire after a
// concurrent Cancel has moved the executor into StateCanceling. The executor
// must ignore that late Resume rather than terminate.
func TestChangesetExecutor_ResumeWhileCancelingIsIgnored(t *testing.T) {
	executor, sender, err := newChangesetExecutorForTest(t)
	require.NoError(t, err)

	executor.SendMessage(sender, Start{Changeset: changesetWithInProgressResource("cmd-resume-cancel")})
	executor.Call(sender, Cancel{CommandID: "cmd-resume-cancel", Force: false})

	executor.SendMessage(sender, Resume{})

	executor.ShouldNotTerminate().Assert()
}

// A command can be canceled in the window after its executor is spawned but
// before it processes Start (spawn and Start are separate messages). The
// executor must cancel cleanly rather than terminate on an unhandled Cancel.
func TestChangesetExecutor_CancelBeforeStartCancelsCleanly(t *testing.T) {
	executor, sender, err := newChangesetExecutorForTest(t)
	require.NoError(t, err)

	result := executor.Call(sender, Cancel{CommandID: "cmd-cancel-before-start", Force: false})

	executor.ShouldNotTerminate().Assert()
	resp, ok := result.Response.(CancelResponse)
	require.True(t, ok, "expected a CancelResponse, got %T", result.Response)
	assert.Empty(t, resp.ErrorMessage)

	executor.ShouldCall().
		Request(forma_persister.MarkCommandResourcesAsCanceled{CommandID: "cmd-cancel-before-start"}).
		Once().
		Assert()
}

// After a cancel-before-start drives the executor to Canceled, the still
// in-flight Start eventually arrives. The executor must ignore it rather than
// terminate on an unhandled Start.
func TestChangesetExecutor_StartAfterCancelIsIgnored(t *testing.T) {
	executor, sender, err := newChangesetExecutorForTest(t)
	require.NoError(t, err)

	executor.Call(sender, Cancel{CommandID: "cmd-start-after-cancel", Force: false})

	executor.SendMessage(sender, Start{Changeset: changesetWithInProgressResource("cmd-start-after-cancel")})

	executor.ShouldNotTerminate().Assert()
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

// The synchronizer exclusion exists to stop a sync cycle reading and
// persisting a resource in the window between this changeset writing it at the
// provider and persisting it itself. That race does not care who asked for the
// write, so the gate is "does this changeset write" rather than "is this a
// user's changeset".
//
// A rotation is the case that made the distinction matter. Gated on user
// updates alone, a rotation registers nothing, the sync records the value the
// rotation just wrote as out-of-band drift, and because a rotation is itself
// the reconcile that would clear the drift window every later rotation is
// refused. The credential then stops turning over permanently, with no
// user-visible signal beyond a repeating log line.

func TestChangesetWritesResources_GeneratorRotationWrites(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationUpdate)

	cs := Changeset{
		CommandID: "test",
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				opURI: {
					URI: opURI,
					Update: &resource_update.ResourceUpdate{
						Source: resource_update.FormaCommandSourceGeneratorRotation,
					},
				},
			},
		},
	}
	assert.True(t, changesetWritesResources(cs),
		"a rotation writes resources and must exclude them from sync")
}

func TestChangesetWritesResources_AutoReconcileWrites(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationUpdate)

	cs := Changeset{
		CommandID: "test",
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				opURI: {
					URI: opURI,
					Update: &resource_update.ResourceUpdate{
						Source: resource_update.FormaCommandSourcePolicyAutoReconcile,
					},
				},
			},
		},
	}
	assert.True(t, changesetWritesResources(cs),
		"an auto-reconcile writes resources and must exclude them from sync")
}

func TestChangesetWritesResources_UserWrites(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationUpdate)

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
	assert.True(t, changesetWritesResources(cs))
}

// Sync and discovery read rather than write. Excluding a resource from sync on
// behalf of a sync changeset would be circular, and would stop sync doing the
// only thing it is for.
func TestChangesetWritesResources_SyncDoesNotWrite(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationUpdate)

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
	assert.False(t, changesetWritesResources(cs))
}

func TestChangesetWritesResources_DiscoveryDoesNotWrite(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")
	opURI := createOperationURI(uri, resource_update.OperationUpdate)

	cs := Changeset{
		CommandID: "test",
		DAG: &ExecutionDAG{
			Nodes: map[pkgmodel.FormaeURI]*DAGNode{
				opURI: {
					URI: opURI,
					Update: &resource_update.ResourceUpdate{
						Source: resource_update.FormaCommandSourceDiscovery,
					},
				},
			},
		},
	}
	assert.False(t, changesetWritesResources(cs))
}

func TestChangesetWritesResources_EmptyChangeset(t *testing.T) {
	cs := Changeset{
		CommandID: "test",
		DAG:       &ExecutionDAG{Nodes: map[pkgmodel.FormaeURI]*DAGNode{}},
	}
	assert.False(t, changesetWritesResources(cs))
}

// A target update is not a resource write, so it registers nothing.
func TestChangesetWritesResources_TargetUpdatesIgnored(t *testing.T) {
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
	assert.False(t, changesetWritesResources(cs))
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

	cs, err := buildChangesetForTest(
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

// TestResolveNodeFinished_ConversionFailure_FailsDependentsClosed verifies that when a
// resolved target config cannot be converted to plugin format (here it still carries a
// $hashed value that must never be sent to a provider), the executor fails closed: the
// unconverted document is NOT propagated to dependent resource ops, and those ops are
// failed (removed from the DAG) so nothing malformed reaches a plugin.
func TestResolveNodeFinished_ConversionFailure_FailsDependentsClosed(t *testing.T) {
	const targetLabel = "consumer"
	ksuid := util.NewID()

	unresolved := json.RawMessage(`{"apiKey":{"$ref":"formae://secret#/token","$visibility":"Opaque"}}`)
	// A resolved config carrying a $hashed value: ConvertToPluginFormat rejects it,
	// because a stored hash can never be recovered to the live secret for a plugin.
	hashedResolved := json.RawMessage(`{"apiKey":{"$value":"deadbeef","$hashed":true}}`)

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

	cs, err := buildChangesetForTest(
		[]resource_update.ResourceUpdate{resourceOp},
		[]target_update.TargetUpdate{resolveOp},
		"cmd-resolve-fail-closed",
		pkgmodel.CommandApply,
		nil,
	)
	require.NoError(t, err)

	resolveNodeURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	resolveNode := cs.DAG.Nodes[resolveNodeURI]
	require.NotNil(t, resolveNode, "resolve node must exist in DAG")
	resolveNode.Update.MarkInProgress()

	opURI := createOperationURI(pkgmodel.NewFormaeURI(ksuid, ""), resource_update.OperationCreate)
	cs.trackedUpdates[string(opURI)] = true

	// Capture the resource node before the handler runs so we can inspect its config after.
	resourceNode := cs.DAG.Nodes[opURI]
	require.NotNil(t, resourceNode)
	ru := resourceNode.Update.(*resource_update.ResourceUpdate)

	actor, err := unit.Spawn(t, NewChangesetExecutor, unit.WithArgs(gen.PID{}), unit.WithLogLevel(gen.LogLevelError))
	require.NoError(t, err)
	proc := actor.Process()

	data := ChangesetData{changeset: cs, requestedBy: gen.PID{}}

	finishedMsg := target_update.TargetUpdateFinished{
		NodeURI:        resolveNodeURI,
		State:          target_update.TargetUpdateStateSuccess,
		ResolvedConfig: hashedResolved,
	}

	_, updatedData, _, _ := targetUpdateFinished(gen.PID{}, StateProcessing, data, finishedMsg, proc)

	// The unconverted (hashed) config must NOT have been propagated onto the dependent op.
	assert.NotContains(t, string(ru.ResourceTarget.Config), "$hashed",
		"the unconverted hashed config must never be propagated to a dependent resource op")
	assert.JSONEq(t, string(unresolved), string(ru.ResourceTarget.Config),
		"the dependent op must retain its original config, not receive the unconverted document")

	// The dependent resource op must be failed (removed from the DAG) so it is never
	// dispatched to a plugin with malformed config.
	assert.Nil(t, updatedData.changeset.DAG.Nodes[opURI],
		"the dependent resource op must be failed and removed from the DAG (fail closed)")
	assert.True(t, ru.IsFailed(),
		"the dependent resource op must be marked failed when the target config conversion fails")
}
