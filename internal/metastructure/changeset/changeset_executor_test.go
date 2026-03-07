// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"testing"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
