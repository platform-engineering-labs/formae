// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// mutableResolvableTargetUpdate builds a mutable target-config update (Update op)
// on a $ref-bearing target. The single external resolvable marks the target as
// re-resolving config without adding a resolvable edge to any resource in the
// changeset.
func mutableResolvableTargetUpdate(label string) target_update.TargetUpdate {
	return target_update.TargetUpdate{
		Target:               pkgmodel.Target{Label: label, Namespace: "AWS"},
		Operation:            target_update.TargetOperationUpdate,
		State:                target_update.TargetUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(util.NewID(), "")},
	}
}

// TestBuildTargetResourceEdges_MutableTargetUpdate_AllDependentOpsDependOnTargetNode
// asserts that when a $ref-bearing target receives a mutable config update, every
// dependent resource op — create, update, AND delete — gets an ordering edge onto
// the target-update node, so none can dispatch before the config is re-resolved.
func TestBuildTargetResourceEdges_MutableTargetUpdate_AllDependentOpsDependOnTargetNode(t *testing.T) {
	const targetLabel = "grafana"
	createKsuid := util.NewID()
	updateKsuid := util.NewID()
	deleteKsuid := util.NewID()

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "create-res", Type: "AWS::Test::Resource", Stack: "s", Ksuid: createKsuid, Target: targetLabel},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState: pkgmodel.Resource{Label: "update-res", Type: "AWS::Test::Resource", Stack: "s", Ksuid: updateKsuid, Target: targetLabel},
			Operation:    resource_update.OperationUpdate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			PriorState:   pkgmodel.Resource{Label: "delete-res", Type: "AWS::Test::Resource", Stack: "s", Ksuid: deleteKsuid, Target: targetLabel},
			DesiredState: pkgmodel.Resource{Label: "delete-res", Type: "AWS::Test::Resource", Stack: "s", Ksuid: deleteKsuid, Target: targetLabel},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	targetUpdates := []target_update.TargetUpdate{mutableResolvableTargetUpdate(targetLabel)}

	cs, err := NewChangeset(resourceUpdates, targetUpdates, "cmd-ordering", pkgmodel.CommandApply)
	require.NoError(t, err)

	targetNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://"+targetLabel+"/update")]
	require.NotNil(t, targetNode, "expected a target-update node")

	cases := []struct {
		name  string
		ksuid string
		op    resource_update.OperationType
	}{
		{"create", createKsuid, resource_update.OperationCreate},
		{"update", updateKsuid, resource_update.OperationUpdate},
		{"delete", deleteKsuid, resource_update.OperationDelete},
	}
	for _, tc := range cases {
		node := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(tc.ksuid, ""), tc.op)
		assert.Truef(t, hasDependency(node, targetNode),
			"%s op should depend on the target-update node so it dispatches after re-resolution", tc.name)
	}
}

// TestNewChangeset_ResourceReplaceOnResolvableTarget_RemainsAcyclic checks that a
// resource replace (delete + create) on a target that also takes a mutable $ref
// config update stays acyclic: the new delete→target-update ordering edge coexists
// with the replace's create→delete edge without forming a cycle.
func TestNewChangeset_ResourceReplaceOnResolvableTarget_RemainsAcyclic(t *testing.T) {
	const targetLabel = "grafana"
	k := util.NewID()

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "r", Type: "AWS::Test::Resource", Stack: "s", Ksuid: k, Target: targetLabel},
			Operation:    resource_update.OperationReplace,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}
	targetUpdates := []target_update.TargetUpdate{mutableResolvableTargetUpdate(targetLabel)}

	cs, err := NewChangeset(resourceUpdates, targetUpdates, "cmd-replace-acyclic", pkgmodel.CommandApply)
	require.NoError(t, err)

	assert.False(t, cs.DAG.HasCycles(),
		"a resource replace on a $ref target plus a mutable target update must stay acyclic")

	// The split delete should still carry the ordering edge onto the target update.
	deleteNode := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(k, ""), resource_update.OperationDelete)
	targetNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://"+targetLabel+"/update")]
	require.NotNil(t, targetNode)
	assert.True(t, hasDependency(deleteNode, targetNode),
		"the replace's delete should wait for the target update when no cycle would result")
}

// TestBuildTargetResourceEdges_CycleProneDeleteEdge_IsSkipped covers the adversarial
// shape the cycle-safety guard exists for: a target update whose $ref points at a
// resource being replaced on that same target. There, target-update→create (resolvable)
// and create→delete (replace) already exist, so adding delete→target-update would close
// a cycle. The guard must skip that edge.
func TestBuildTargetResourceEdges_CycleProneDeleteEdge_IsSkipped(t *testing.T) {
	const targetLabel = "grafana"
	k := util.NewID()

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "r", Type: "AWS::Test::Resource", Stack: "s", Ksuid: k, Target: targetLabel},
			Operation:    resource_update.OperationReplace,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}
	// The target config $ref resolves to the replaced resource (matching ksuid).
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:               pkgmodel.Target{Label: targetLabel, Namespace: "AWS"},
			Operation:            target_update.TargetOperationUpdate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{pkgmodel.NewFormaeURI(k, "")},
		},
	}

	cs, err := NewChangeset(resourceUpdates, targetUpdates, "cmd-cycle-guard", pkgmodel.CommandApply)
	require.NoError(t, err)

	deleteNode := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(k, ""), resource_update.OperationDelete)
	targetNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://"+targetLabel+"/update")]
	require.NotNil(t, targetNode)

	assert.False(t, hasDependency(deleteNode, targetNode),
		"the cycle-prone delete→target-update edge must be skipped by the cycle-safety guard")
}

// TestDependsOnTransitively_WalksDependencyChain locks the semantics of the
// cycle-safety helper the delete edge relies on.
func TestDependsOnTransitively_WalksDependencyChain(t *testing.T) {
	a := &DAGNode{URI: "a", Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}
	b := &DAGNode{URI: "b", Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}
	c := &DAGNode{URI: "c", Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}

	// a depends on b, b depends on c.
	a.LinkWith(b)
	b.LinkWith(c)

	assert.True(t, dependsOnTransitively(a, "b"), "a directly depends on b")
	assert.True(t, dependsOnTransitively(a, "c"), "a transitively depends on c")
	assert.False(t, dependsOnTransitively(c, "a"), "c does not depend on a")
	assert.False(t, dependsOnTransitively(a, "missing"), "a does not depend on an absent node")
}

// TestChangeset_MutableTargetUpdate_DeleteDispatchesResolvedConfigAfterTargetUpdate
// drives the changeset's scheduling API to prove the real race boundary: on a mutable
// $ref-target update, a dependent delete becomes executable only after the target
// update completes, and the dispatch snapshot it would carry holds the resolved plain
// plugin config — while a resource on a different target is left untouched.
func TestChangeset_MutableTargetUpdate_DeleteDispatchesResolvedConfigAfterTargetUpdate(t *testing.T) {
	const (
		targetLabel = "grafana"
		otherLabel  = "other-target"
	)
	deleteKsuid := util.NewID()
	createKsuid := util.NewID()
	otherKsuid := util.NewID()

	unresolved := json.RawMessage(`{"auth":{"$ref":"formae://secret#/token"}}`)
	otherConfig := json.RawMessage(`{"auth":"other-untouched"}`)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			PriorState:     pkgmodel.Resource{Label: "del", Type: "AWS::Test::Resource", Stack: "s", Ksuid: deleteKsuid, Target: targetLabel},
			DesiredState:   pkgmodel.Resource{Label: "del", Type: "AWS::Test::Resource", Stack: "s", Ksuid: deleteKsuid, Target: targetLabel},
			Operation:      resource_update.OperationDelete,
			State:          resource_update.ResourceUpdateStateNotStarted,
			StackLabel:     "s",
			ResourceTarget: pkgmodel.Target{Label: targetLabel, Namespace: "AWS", Config: unresolved},
		},
		{
			DesiredState:   pkgmodel.Resource{Label: "cre", Type: "AWS::Test::Resource", Stack: "s", Ksuid: createKsuid, Target: targetLabel},
			Operation:      resource_update.OperationCreate,
			State:          resource_update.ResourceUpdateStateNotStarted,
			StackLabel:     "s",
			ResourceTarget: pkgmodel.Target{Label: targetLabel, Namespace: "AWS", Config: unresolved},
		},
		{
			DesiredState:   pkgmodel.Resource{Label: "oth", Type: "AWS::Test::Resource", Stack: "s", Ksuid: otherKsuid, Target: otherLabel},
			Operation:      resource_update.OperationCreate,
			State:          resource_update.ResourceUpdateStateNotStarted,
			StackLabel:     "s",
			ResourceTarget: pkgmodel.Target{Label: otherLabel, Namespace: "AWS", Config: otherConfig},
		},
	}
	targetUpdates := []target_update.TargetUpdate{mutableResolvableTargetUpdate(targetLabel)}

	cs, err := NewChangeset(resourceUpdates, targetUpdates, "cmd-propagation", pkgmodel.CommandApply)
	require.NoError(t, err)

	// isGrafanaDelete identifies the grafana delete among scheduled updates. A
	// ResourceUpdate's NodeURI is not operation-qualified, so match on (ksuid, op).
	isGrafanaDelete := func(u Update) bool {
		ru, ok := u.(*resource_update.ResourceUpdate)
		return ok && ru.Operation == resource_update.OperationDelete && ru.DesiredState.Ksuid == deleteKsuid
	}

	// Before the target update completes, the grafana delete must not be executable.
	first := cs.GetExecutableUpdates("AWS", 10)
	for _, u := range first {
		assert.Falsef(t, isGrafanaDelete(u),
			"grafana delete must wait for the target update to re-resolve config")
	}

	// Simulate the target updater resolving $ref → $value and the executor propagating
	// the plain plugin value onto dependent ops, then completing the target update.
	resolved := json.RawMessage(`{"auth":{"token":"resolved-secret"}}`)
	cs.DAG.propagateResolvedTargetConfig(targetLabel, resolved)

	targetNodeURI := pkgmodel.FormaeURI("target://" + targetLabel + "/update")
	tuNode := cs.DAG.Nodes[targetNodeURI]
	require.NotNil(t, tuNode)
	tu := tuNode.Update.(*target_update.TargetUpdate)
	tu.State = target_update.TargetUpdateStateSuccess
	_, err = cs.UpdateDAG(targetNodeURI, tu)
	require.NoError(t, err)

	// Now the grafana delete is executable, and the snapshot it would dispatch carries
	// the resolved plain plugin config.
	second := cs.GetExecutableUpdates("AWS", 10)
	var deletedDispatched *resource_update.ResourceUpdate
	for _, u := range second {
		if isGrafanaDelete(u) {
			deletedDispatched = u.(*resource_update.ResourceUpdate)
		}
	}
	require.NotNil(t, deletedDispatched, "grafana delete should be executable after the target update completes")
	assert.JSONEq(t, string(resolved), string(deletedDispatched.ResourceTarget.Config),
		"the delete must dispatch with the resolved target config, not the stale $ref snapshot")

	// A resource on a different target must not receive the propagated config.
	otherNode := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(otherKsuid, ""), resource_update.OperationCreate)
	otherRU := otherNode.Update.(*resource_update.ResourceUpdate)
	assert.JSONEq(t, string(otherConfig), string(otherRU.ResourceTarget.Config),
		"a resource on a different target must not receive the propagated config")
}
