// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	StatePersisting           = gen.Atom("persisting")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
)

// TargetUpdater is a simple FSM actor that persists a single target update
// via the ResourcePersister and reports the result back to its requester.
type TargetUpdater struct {
	statemachine.StateMachine[TargetUpdaterData]
}

// StartTargetUpdate is sent to the TargetUpdater to begin persisting a target update.
type StartTargetUpdate struct {
	TargetUpdate TargetUpdate
	CommandID    string
}

// TargetUpdateFinished is sent back to the requester when the target update completes.
type TargetUpdateFinished struct {
	NodeURI pkgmodel.FormaeURI
	State   TargetUpdateState
}

// Shutdown is sent to terminate the TargetUpdater process.
type Shutdown struct{}

// TargetUpdaterData holds the FSM's internal state.
type TargetUpdaterData struct {
	targetUpdate TargetUpdate
	commandID    string
	requestedBy  gen.PID
}

func NewTargetUpdater() gen.ProcessBehavior {
	return &TargetUpdater{}
}

func (t *TargetUpdater) Init(args ...any) (statemachine.StateMachineSpec[TargetUpdaterData], error) {
	data := TargetUpdaterData{
		requestedBy: args[0].(gen.PID),
	}

	t.Log().Debug("TargetUpdater %s initialized", t.Name())

	return statemachine.NewStateMachineSpec(StatePersisting,
		statemachine.WithData(data),
		statemachine.WithStateEnterCallback(onTargetUpdaterStateChange),
		statemachine.WithStateMessageHandler(StatePersisting, startPersist),
		statemachine.WithStateMessageHandler(StatePersisting, shutdownTargetUpdater),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdownTargetUpdater),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdownTargetUpdater),
	), nil
}

// resourcePersisterProcess returns the address of the global resource persister.
func resourcePersisterProcess(proc gen.Process) gen.ProcessID {
	return gen.ProcessID{
		Name: actornames.ResourcePersister,
		Node: proc.Node().Name(),
	}
}

func startPersist(from gen.PID, state gen.Atom, data TargetUpdaterData, message StartTargetUpdate, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	data.targetUpdate = message.TargetUpdate
	data.commandID = message.CommandID

	_, err := proc.Call(
		resourcePersisterProcess(proc),
		PersistTargetUpdates{
			TargetUpdates: []TargetUpdate{message.TargetUpdate},
			CommandID:     message.CommandID,
		},
	)
	if err != nil {
		proc.Log().Error("TargetUpdater: failed to persist target update", "error", err, "target", message.TargetUpdate.Target.Label)
		return StateFinishedWithError, data, nil, nil
	}

	return StateFinishedSuccessfully, data, nil, nil
}

func onTargetUpdaterStateChange(oldState gen.Atom, newState gen.Atom, data TargetUpdaterData, proc gen.Process) (gen.Atom, TargetUpdaterData, error) {
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError {
		var finalState TargetUpdateState
		if newState == StateFinishedSuccessfully {
			finalState = TargetUpdateStateSuccess
		} else {
			finalState = TargetUpdateStateFailed
		}

		proc.Log().Debug("TargetUpdater: sending TargetUpdateFinished to requester", "state", newState, "target", data.targetUpdate.Target.Label)
		err := proc.Send(
			data.requestedBy,
			TargetUpdateFinished{
				NodeURI: data.targetUpdate.NodeURI(),
				State:   finalState,
			},
		)
		if err != nil {
			proc.Log().Error("TargetUpdater: failed to send TargetUpdateFinished to requester", "error", err)
		}

		// Send ourselves a shutdown message to terminate the process.
		err = proc.Send(proc.PID(), Shutdown{})
		if err != nil {
			proc.Log().Error("TargetUpdater: failed to send shutdown message: %v", err)
		}
	}
	return newState, data, nil
}

func shutdownTargetUpdater(from gen.PID, state gen.Atom, data TargetUpdaterData, shutdown Shutdown, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}
