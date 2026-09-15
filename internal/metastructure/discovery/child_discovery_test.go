// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	childParentType = "FakeAzure::Resources::ResourceGroup"
	childChildType  = "FakeAzure::ContainerService::ManagedCluster"
	childNamespace  = "FakeAzure"
	childTarget     = "sub-1"
)

// stubChildDatastore returns a fixed parent set from QueryResources, or an
// error when queryErr is set.
type stubChildDatastore struct {
	datastore.Datastore
	parents  []*pkgmodel.Resource
	queryErr error
}

func (s *stubChildDatastore) QueryResources(_ *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	return s.parents, nil
}

// newChildDiscoveryData wires a parent type that has one child type, so
// discoverChildren has somewhere to queue work.
func newChildDiscoveryData(ds datastore.Datastore) DiscoveryData {
	child := &hierarchyNode{resourceType: childChildType, discoverable: true}
	parent := &hierarchyNode{
		resourceType: childParentType,
		discoverable: true,
		children: map[*hierarchyNode][]plugin.ListParameter{
			child: {{ParentProperty: "name", ListProperty: "resourceGroupName"}},
		},
	}
	return DiscoveryData{
		ds:           ds,
		discoveryCfg: &pkgmodel.DiscoveryConfig{Enabled: true, Interval: 20 * time.Second},
		serverCfg:    &pkgmodel.ServerConfig{},
		targets: map[string]pkgmodel.Target{
			childTarget: {Label: childTarget, Namespace: childNamespace},
		},
		resourceHierarchy: map[string]*hierarchyNode{
			childParentType: parent,
			childChildType:  child,
		},
		resourceDescriptors:           map[string]plugin.ResourceDescriptor{},
		queuedListOperations:          map[string][]ListOperation{},
		outstandingListOperations:     map[string]ListOperation{},
		outstandingSyncCommands:       map[string]ListOperation{},
		recentlyDiscoveredResourceIDs: map[string]struct{}{},
		summary:                       map[string]int{},
		typesWithChildrenQueued:       map[string]struct{}{},
		nativeIDsByCommand:            map[string][]string{},
	}
}

func childParent(name string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Ksuid:      "ksuid-" + name,
		NativeID:   name,
		Type:       childParentType,
		Target:     childTarget,
		Properties: json.RawMessage(`{"name":"` + name + `"}`),
	}
}

// A sync that finishes with errors still persisted some parents. Their children
// must still be discovered: one sibling failing is not a reason to skip an
// entire batch, and nothing else will come back for them, because
// discoverChildrenOnce already ran against a datastore that predates the sync.
func TestSyncCompleted_PartialFailureStillDiscoversChildren(t *testing.T) {
	const commandID = "cmd-1"

	// "rg-a" persisted, "rg-missing" did not: the datastore only knows the first.
	ds := &stubChildDatastore{parents: []*pkgmodel.Resource{childParent("rg-a")}}
	data := newChildDiscoveryData(ds)
	op := ListOperation{ResourceType: childParentType, TargetLabel: childTarget}
	data.outstandingSyncCommands[commandID] = op
	data.nativeIDsByCommand[commandID] = []string{"rg-a", "rg-missing"}

	proc := &stubProcess{}
	_, data, _, err := syncCompleted(gen.PID{}, StateDiscovering, data, changeset.ChangesetCompleted{
		CommandID: commandID,
		State:     changeset.ChangeSetStateFinishedWithErrors,
	}, proc)
	require.NoError(t, err)

	queued := data.queuedListOperations[childNamespace]
	require.Len(t, queued, 1, "the parent that did persist must have its children queued")
	assert.Equal(t, childChildType, queued[0].ResourceType)
	assert.Contains(t, queued[0].ListParams, "rg-a")
	assert.NotContains(t, queued[0].ListParams, "rg-missing",
		"a parent that never persisted is filtered out by the datastore read, not by association")
}

// failingSendProcess makes the ResumeScanning dispatch inside discoverChildren
// fail, which is how queueing breaks under back-pressure in production.
type failingSendProcess struct{ *stubProcess }

func (p *failingSendProcess) Send(_ any, _ any) error {
	return errors.New("mailbox full")
}

// The completion marker must reflect work actually done. Queueing that fails
// part way must leave the type unmarked, so the guard in discoverChildrenOnce
// lets a later attempt in the same cycle retry it instead of refusing forever.
func TestDiscoverChildrenOnce_DoesNotMarkDoneWhenQueueingFails(t *testing.T) {
	ds := &stubChildDatastore{parents: []*pkgmodel.Resource{childParent("rg-a")}}
	data := newChildDiscoveryData(ds)
	op := ListOperation{ResourceType: childParentType, TargetLabel: childTarget}

	err := discoverChildrenOnce(op, data, &failingSendProcess{&stubProcess{}})
	require.Error(t, err)

	key := childParentType + "#" + childTarget
	_, marked := data.typesWithChildrenQueued[key]
	assert.False(t, marked, "queueing that failed must not mark the type complete")

	// The retry succeeds once the mailbox recovers, which is only reachable
	// because the marker was left unset.
	require.NoError(t, discoverChildrenOnce(op, data, &stubProcess{}))
	assert.Len(t, data.queuedListOperations[childNamespace], 1, "retry must not duplicate an already queued child operation")
	_, marked = data.typesWithChildrenQueued[key]
	assert.True(t, marked)
}

// A parent load that fails must also leave the type unmarked.
func TestDiscoverChildrenOnce_DoesNotMarkDoneWhenParentLoadFails(t *testing.T) {
	ds := &stubChildDatastore{queryErr: errors.New("datastore unavailable")}
	data := newChildDiscoveryData(ds)
	op := ListOperation{ResourceType: childParentType, TargetLabel: childTarget}

	require.Error(t, discoverChildrenOnce(op, data, &stubProcess{}))

	key := childParentType + "#" + childTarget
	_, marked := data.typesWithChildrenQueued[key]
	assert.False(t, marked, "a failed parent load must not mark the type complete")
}

// The happy path still marks the type, so a parent list arriving in several
// pages does not re-queue the same children repeatedly.
func TestDiscoverChildrenOnce_MarksDoneAfterQueueing(t *testing.T) {
	ds := &stubChildDatastore{parents: []*pkgmodel.Resource{childParent("rg-a")}}
	data := newChildDiscoveryData(ds)
	op := ListOperation{ResourceType: childParentType, TargetLabel: childTarget}

	require.NoError(t, discoverChildrenOnce(op, data, &stubProcess{}))

	key := childParentType + "#" + childTarget
	_, marked := data.typesWithChildrenQueued[key]
	assert.True(t, marked)
	assert.Len(t, data.queuedListOperations[childNamespace], 1)

	// A second call is a no-op rather than a duplicate queue entry.
	require.NoError(t, discoverChildrenOnce(op, data, &stubProcess{}))
	assert.Len(t, data.queuedListOperations[childNamespace], 1)
}

// With no other messages in flight, retaining a child operation after its
// scanner wakeup fails strands the cycle in Discovering. Both completion paths
// must instead finish and arm the next periodic pass.
func TestChildDiscovery_WakeupFailureCompletesCycle(t *testing.T) {
	for _, completion := range []string{"listing", "sync-success", "sync-partial-failure"} {
		t.Run(completion, func(t *testing.T) {
			ds := &stubChildDatastore{parents: []*pkgmodel.Resource{childParent("rg-a"), childParent("rg-b")}}
			data := newChildDiscoveryData(ds)
			op := ListOperation{ResourceType: childParentType, TargetLabel: childTarget}
			proc := &failingSendProcess{&stubProcess{}}
			var state gen.Atom
			var actions []statemachine.Action
			var err error
			if completion == "listing" {
				data.outstandingListOperations[listOperationMapKey(childParentType, childTarget, "")] = op
				state, data, actions, err = processListing(gen.PID{}, StateDiscovering, data, plugin.Listing{
					ResourceType: childParentType, TargetLabel: childTarget,
				}, proc)
			} else {
				data.outstandingSyncCommands["cmd-1"] = op
				data.nativeIDsByCommand["cmd-1"] = []string{"rg-a", "rg-b"}
				syncState := changeset.ChangeSetStateFinishedSuccessfully
				if completion == "sync-partial-failure" {
					syncState = changeset.ChangeSetStateFinishedWithErrors
				}
				state, data, actions, err = syncCompleted(gen.PID{}, StateDiscovering, data, changeset.ChangesetCompleted{
					CommandID: "cmd-1", State: syncState,
				}, proc)
			}
			require.NoError(t, err)
			assert.False(t, data.HasOutstandingWork(), "a failed wakeup must not leave unreachable queued work")
			assert.Equal(t, StateIdle, state, "without another callback, Discovering would never finish")
			require.Len(t, actions, 1, "the next discovery cycle must be scheduled")
			tick, ok := actions[0].(statemachine.GenericTimeout)
			require.True(t, ok)
			assert.Equal(t, Discover{}, tick.Message)
			assert.Equal(t, 20*time.Second, tick.Duration)

			// On a later successful attempt, both saved parents are still
			// eligible and each contributes exactly one child operation.
			require.NoError(t, discoverChildrenOnce(op, data, &stubProcess{}))
			queued := data.queuedListOperations[childNamespace]
			require.Len(t, queued, 2)
			assert.Equal(t, "ksuid-rg-a", queued[0].ParentKSUID)
			assert.Equal(t, "ksuid-rg-b", queued[1].ParentKSUID)
		})
	}
}

func TestDiscoverChildren_WakeupFailurePreservesExistingQueue(t *testing.T) {
	data := newChildDiscoveryData(&stubChildDatastore{})
	existing := ListOperation{ResourceType: childChildType, TargetLabel: childTarget, ParentKSUID: "existing"}
	data.queuedListOperations[childNamespace] = []ListOperation{existing}

	err := discoverChildren([]*pkgmodel.Resource{childParent("rg-a"), childParent("rg-b")},
		ListOperation{ResourceType: childParentType, TargetLabel: childTarget}, data, &failingSendProcess{&stubProcess{}})
	require.Error(t, err)
	assert.Equal(t, []ListOperation{existing}, data.queuedListOperations[childNamespace],
		"a failed batch must neither append partial work nor discard earlier queued work")
}

func TestDiscoverChildren_UsesPendingScannerWakeup(t *testing.T) {
	data := newChildDiscoveryData(&stubChildDatastore{})
	data.hasPendingResumeScan = true

	// Immediate sends fail, but the existing delayed wakeup will drain this
	// batch: no additional send is needed to accept all of its children.
	err := discoverChildren([]*pkgmodel.Resource{childParent("rg-a"), childParent("rg-b")},
		ListOperation{ResourceType: childParentType, TargetLabel: childTarget}, data, &failingSendProcess{&stubProcess{}})
	require.NoError(t, err)
	assert.Len(t, data.queuedListOperations[childNamespace], 2)
}
