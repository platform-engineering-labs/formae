// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"testing"
	"time"

	"ergo.services/actor/statemachine"
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
// the discover() handler directly.
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

// A Discover arriving while a discovery cycle is running (e.g. force_discover
// or a target persisted mid-cycle) must not break the periodic chain. The
// busy guard drops the message without producing actions, and a subsequent
// cycle completion still returns a GenericTimeout to schedule the next tick.
//
// The named-timer design makes the wedge that 0.86.0 hit (a cycle swallowing
// the only pending periodic tick) structurally impossible: every transition
// to Idle returns a fresh GenericTimeout, and the framework auto-cancels any
// prior tick with the same name. There is no flag whose stale value could
// suppress the next schedule.
func TestDiscoverDuringRunningCycleDoesNotKillPeriodicChain(t *testing.T) {
	proc := &stubProcess{}
	data := newDiscoverData(proc)

	// A Discover lands during a running cycle. Busy guard drops it without
	// producing actions.
	state, _, actions, err := discover(gen.PID{}, StateDiscovering, data, Discover{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateDiscovering, state, "Discover during a running cycle must be dropped, not start a second cycle")
	require.Empty(t, actions, "a dropped message must not produce actions")

	// A subsequent cycle completes — here exercised via the no-targets early
	// exit, which is the simplest path through discover() that returns to
	// Idle with a rescheduleAction. The full completion paths in
	// processListing/syncCompleted use the same helper.
	data.ds = &stubDiscoveryDatastore{targets: nil}
	nextState, _, actions, err := discover(gen.PID{}, StateIdle, data, Discover{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateIdle, nextState)

	require.Len(t, actions, 1, "cycle completion must schedule the next periodic tick")
	timeout, ok := actions[0].(statemachine.GenericTimeout)
	require.True(t, ok, "scheduled action must be a GenericTimeout (named timer so a fresh schedule cancels any prior one)")
	assert.Equal(t, discoveryTickName, timeout.Name)
	assert.Equal(t, data.discoveryCfg.Interval, timeout.Duration)
	assert.Equal(t, Discover{}, timeout.Message)
}

// When discovery is disabled, no periodic chain should exist; cycle
// completion must not schedule a tick.
func TestDiscoveryDisabledDoesNotScheduleTick(t *testing.T) {
	proc := &stubProcess{}
	data := newDiscoverData(proc)
	data.discoveryCfg = &pkgmodel.DiscoveryConfig{Enabled: false, Interval: 20 * time.Second}
	data.ds = &stubDiscoveryDatastore{targets: nil}

	_, _, actions, err := discover(gen.PID{}, StateIdle, data, Discover{}, proc)
	require.NoError(t, err)
	assert.Empty(t, actions, "disabled discovery must not schedule any timer")
}
