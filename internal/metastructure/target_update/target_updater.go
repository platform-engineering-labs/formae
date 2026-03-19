// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	StateResolving            = gen.Atom("resolving")
	StatePersisting           = gen.Atom("persisting")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
)

// TargetUpdater is a simple FSM actor that resolves config resolvables,
// persists a single target update via the ResourcePersister, and reports
// the result back to its requester.
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

// ResolveCacheMissingInAction is a timeout message for the resolve loop.
type ResolveCacheMissingInAction struct{}

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

	return statemachine.NewStateMachineSpec(StateResolving,
		statemachine.WithData(data),
		statemachine.WithStateEnterCallback(onTargetUpdaterStateChange),
		// Resolving state handlers
		statemachine.WithStateMessageHandler(StateResolving, handleStartTargetUpdate),
		statemachine.WithStateMessageHandler(StateResolving, targetValueResolved),
		statemachine.WithStateMessageHandler(StateResolving, targetFailedToResolve),
		statemachine.WithStateMessageHandler(StateResolving, targetResolveCacheTimeout),
		statemachine.WithStateMessageHandler(StateResolving, shutdownTargetUpdater),
		// Persisting state handlers
		statemachine.WithStateMessageHandler(StatePersisting, shutdownTargetUpdater),
		// Terminal state handlers
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

// handleStartTargetUpdate receives the initial message and enters the resolve loop or goes straight to persist.
func handleStartTargetUpdate(from gen.PID, state gen.Atom, data TargetUpdaterData, message StartTargetUpdate, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	data.targetUpdate = message.TargetUpdate
	data.commandID = message.CommandID

	if len(data.targetUpdate.RemainingResolvables) > 0 {
		return resolveTargetConfig(state, data, proc)
	}
	return persistTarget(data, proc)
}

// resolveTargetConfig pops the next resolvable and sends a ResolveValue request.
func resolveTargetConfig(state gen.Atom, data TargetUpdaterData, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	if len(data.targetUpdate.RemainingResolvables) == 0 {
		return persistTarget(data, proc)
	}

	first := data.targetUpdate.RemainingResolvables[0]
	data.targetUpdate.RemainingResolvables = data.targetUpdate.RemainingResolvables[1:]

	err := proc.Send(
		gen.ProcessID{
			Node: proc.Node().Name(),
			Name: actornames.ResolveCache(data.commandID),
		},
		messages.ResolveValue{
			ResourceURI: first,
		},
	)
	if err != nil {
		proc.Log().Error("TargetUpdater: failed to send ResolveValue", "error", err, "uri", first)
		return StateFinishedWithError, data, nil, nil
	}

	timeout := statemachine.StateTimeout{
		Duration: 30 * time.Second,
		Message:  ResolveCacheMissingInAction{},
	}

	return StateResolving, data, []statemachine.Action{timeout}, nil
}

// targetValueResolved handles a successfully resolved value.
func targetValueResolved(from gen.PID, state gen.Atom, data TargetUpdaterData, message messages.ValueResolved, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	err := data.targetUpdate.ResolveValue(message.ResourceURI, message.Value)
	if err != nil {
		proc.Log().Error("TargetUpdater: failed to resolve value", "error", err, "uri", message.ResourceURI)
		return StateFinishedWithError, data, nil, nil
	}

	return resolveTargetConfig(state, data, proc)
}

// targetFailedToResolve handles a resolution failure.
func targetFailedToResolve(from gen.PID, state gen.Atom, data TargetUpdaterData, message messages.FailedToResolveValue, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	proc.Log().Error("TargetUpdater: failed to resolve target config property", "uri", message.ResourceURI)
	return StateFinishedWithError, data, nil, nil
}

// targetResolveCacheTimeout handles the timeout when the resolve cache doesn't respond.
func targetResolveCacheTimeout(from gen.PID, state gen.Atom, data TargetUpdaterData, message ResolveCacheMissingInAction, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	proc.Log().Error("TargetUpdater: resolve cache timeout", "target", data.targetUpdate.Target.Label)
	return StateFinishedWithError, data, nil, nil
}

// persistTarget sends the target update to the ResourcePersister for storage.
func persistTarget(data TargetUpdaterData, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	_, err := proc.Call(
		resourcePersisterProcess(proc),
		PersistTargetUpdates{
			TargetUpdates: []TargetUpdate{data.targetUpdate},
			CommandID:     data.commandID,
		},
	)
	if err != nil {
		proc.Log().Error("TargetUpdater: failed to persist target update", "error", err, "target", data.targetUpdate.Target.Label)
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
