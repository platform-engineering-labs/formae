// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"testing"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubSyncLog is a gen.Log that swallows all log output.
type stubSyncLog struct {
	gen.Log
}

func (stubSyncLog) Trace(string, ...any)   {}
func (stubSyncLog) Debug(string, ...any)   {}
func (stubSyncLog) Info(string, ...any)    {}
func (stubSyncLog) Warning(string, ...any) {}
func (stubSyncLog) Error(string, ...any)   {}
func (stubSyncLog) Panic(string, ...any)   {}

// stubSyncProcess is a hand-rolled gen.Process double for synchronizer tests.
// The synchronize() busy-guard path and changesetCompleted() don't reach into
// the datastore or any collaborators, so an inert process is enough to drive
// them directly through their handlers.
type stubSyncProcess struct {
	gen.Process
}

func (p *stubSyncProcess) Log() gen.Log { return stubSyncLog{} }
func (p *stubSyncProcess) PID() gen.PID { return gen.PID{Node: "test-node", ID: 1} }

// A second Synchronize arriving while a periodic synchronization cycle is
// running (e.g. a ForceSync triggered by the CLI) must not kill the periodic
// chain. The intervening message is dropped by the busy guard, and when the
// running cycle completes, the next periodic tick is still scheduled.
// Otherwise synchronization would be dead until agent restart and
// out-of-band changes would never be detected.
func TestForceSyncDuringPeriodicCycleDoesNotKillPeriodicChain(t *testing.T) {
	proc := &stubSyncProcess{}
	data := SynchronizerData{
		cfg:       pkgmodel.SynchronizationConfig{Enabled: true, Interval: 5 * time.Minute},
		commandID: "cycle-1",
	}

	// While a periodic cycle is running, a second Synchronize arrives.
	state, data, actions, err := synchronize(gen.PID{}, StateSynchronizing, data, Synchronize{}, proc)
	require.NoError(t, err)
	require.Equal(t, StateSynchronizing, state, "intervening Synchronize must be dropped by the busy guard, not start a second cycle")
	require.Empty(t, actions, "a dropped message must not produce actions")

	// The running cycle completes.
	nextState, _, actions, err := changesetCompleted(gen.PID{}, StateSynchronizing, data, changeset.ChangesetCompleted{CommandID: data.commandID}, proc)
	require.NoError(t, err)
	require.Equal(t, StateIdle, nextState)

	require.Len(t, actions, 1, "cycle completion must schedule the next periodic tick")
	timeout, ok := actions[0].(statemachine.GenericTimeout)
	require.True(t, ok, "scheduled action must be a GenericTimeout (named timer so a fresh schedule cancels any prior one)")
	assert.Equal(t, syncTickName, timeout.Name)
	assert.Equal(t, data.cfg.Interval, timeout.Duration)
	assert.Equal(t, Synchronize{}, timeout.Message)
}

// When synchronization is disabled, no periodic chain should exist; cycle
// completion must not schedule a tick.
func TestSynchronizationDisabledDoesNotScheduleTick(t *testing.T) {
	proc := &stubSyncProcess{}
	data := SynchronizerData{
		cfg:       pkgmodel.SynchronizationConfig{Enabled: false, Interval: 5 * time.Minute},
		commandID: "cycle-1",
	}

	_, _, actions, err := changesetCompleted(gen.PID{}, StateSynchronizing, data, changeset.ChangesetCompleted{CommandID: data.commandID}, proc)
	require.NoError(t, err)
	assert.Empty(t, actions, "disabled synchronization must not schedule any timer")
}
