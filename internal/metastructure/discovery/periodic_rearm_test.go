// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// stubDiscoveryDatastore is a datastore.Datastore test double that only serves
// the discoverable-targets lookup discover() performs at the start of a cycle.
type stubDiscoveryDatastore struct {
	datastore.Datastore

	targets []*pkgmodel.Target
}

func (s *stubDiscoveryDatastore) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	return s.targets, nil
}

// newDiscoverData builds a DiscoveryData wired to one discoverable target whose
// plugin reports a single discoverable resource type, ready to be driven through
// the discover() and onStateChange() handlers directly.
func newDiscoverData(proc *stubProcess) DiscoveryData {
	const namespace = "FakeAWS"

	proc.pluginInfo = &messages.PluginInfoResponse{
		Found:     true,
		Namespace: namespace,
		SupportedResources: []plugin.ResourceDescriptor{
			{Type: "FakeAWS::EC2::InternetGateway", Discoverable: true},
		},
	}

	return DiscoveryData{
		ds: &stubDiscoveryDatastore{targets: []*pkgmodel.Target{
			{Label: "us-east-1", Namespace: namespace, Discoverable: true},
		}},
		discoveryCfg:                  &pkgmodel.DiscoveryConfig{Enabled: true, Interval: 20 * time.Second},
		serverCfg:                     &pkgmodel.ServerConfig{},
		targets:                       map[string]pkgmodel.Target{},
		resourceHierarchy:             map[string]*hierarchyNode{},
		resourceDescriptors:           map[string]plugin.ResourceDescriptor{},
		queuedListOperations:          map[string][]ListOperation{},
		outstandingListOperations:     map[string]ListOperation{},
		outstandingSyncCommands:       map[string]ListOperation{},
		recentlyDiscoveredResourceIDs: map[string]struct{}{},
		summary:                       map[string]int{},
		pluginInfoCache:               map[string]*messages.PluginInfoResponse{},
		typesWithChildrenQueued:       map[string]struct{}{},
		nativeIDsByCommand:            map[string][]string{},
	}
}

// Regression test for the discovery wedge: a one-shot Discover{Once:true}
// (fired by target persistence during an apply) enters Discovering and is held
// there while the apply runs. The pending periodic tick fires mid-cycle, hits
// the already-running guard, and is dropped. When the one-shot cycle finally
// completes, a replacement periodic tick MUST be scheduled — otherwise the
// periodic chain is dead until agent restart and out-of-band resources are
// never discovered.
func TestOneShotCycleThatSwallowsPeriodicTickReschedulesOnCompletion(t *testing.T) {
	proc := &stubProcess{}
	data := newDiscoverData(proc)

	// A one-shot discovery starts while Idle (target persisted during apply).
	state, data, _, err := discover(gen.PID{}, StateIdle, data, Discover{Once: true}, proc)
	require.NoError(t, err)
	require.Equal(t, StateDiscovering, state, "one-shot must start a cycle")
	require.Equal(t, 0, proc.scheduledDiscoverCount(), "a one-shot run must not arm the periodic ticker by itself")

	// The pending periodic tick fires mid-cycle and is dropped by the
	// already-running guard. This consumes the only periodic timer in flight.
	state, data, _, err = discover(gen.PID{}, state, data, Discover{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateDiscovering, state, "tick during a running cycle must be dropped, not start a second cycle")

	// The parked one-shot cycle completes (apply finished, queue drained).
	_, _, err = onStateChange(StateDiscovering, StateIdle, data, proc)
	require.NoError(t, err)

	assert.Equal(t, 1, proc.scheduledDiscoverCount(),
		"the dropped periodic tick must be replaced when the cycle completes; otherwise periodic discovery is dead until restart")
}

// A fast one-shot that completes while the periodic timer is still pending must
// NOT schedule an additional tick — otherwise every force_discover or
// target-persist trigger would spawn an extra periodic chain and multiply the
// scan frequency.
func TestOneShotCompletionWithPendingTickDoesNotDoubleSchedule(t *testing.T) {
	proc := &stubProcess{}
	data := newDiscoverData(proc)

	// A periodic cycle runs and completes: it schedules the next tick (baton out).
	state, data, _, err := discover(gen.PID{}, StateIdle, data, Discover{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateDiscovering, state)
	_, data, err = onStateChange(StateDiscovering, StateIdle, data, proc)
	require.NoError(t, err)
	require.Equal(t, 1, proc.scheduledDiscoverCount(), "a periodic cycle must schedule its successor on completion")

	// A one-shot starts and completes while that tick is still pending.
	state, data, _, err = discover(gen.PID{}, StateIdle, data, Discover{Once: true}, proc)
	require.NoError(t, err)
	require.Equal(t, StateDiscovering, state)
	_, _, err = onStateChange(StateDiscovering, StateIdle, data, proc)
	require.NoError(t, err)

	assert.Equal(t, 1, proc.scheduledDiscoverCount(),
		"a one-shot completing while a periodic tick is pending must not schedule a second timer")
}

// The plain periodic loop: each cycle schedules exactly one successor on
// completion.
func TestPeriodicCycleSchedulesExactlyOneSuccessor(t *testing.T) {
	proc := &stubProcess{}
	data := newDiscoverData(proc)

	state, data, _, err := discover(gen.PID{}, StateIdle, data, Discover{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateDiscovering, state)

	_, _, err = onStateChange(StateDiscovering, StateIdle, data, proc)
	require.NoError(t, err)

	assert.Equal(t, 1, proc.scheduledDiscoverCount())
}
