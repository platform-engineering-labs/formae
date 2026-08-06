// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"encoding/json"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const (
	StateNotStarted           = gen.Atom("not_started")
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
	NodeURI        pkgmodel.FormaeURI
	State          TargetUpdateState
	ResolvedConfig json.RawMessage // The target config after resolution (with $value filled in)
}

// Shutdown is sent to terminate the TargetUpdater process.
type Shutdown struct{}

// ResolveTimedOut is a timeout message for the resolve loop.
type ResolveTimedOut struct{}

// TargetUpdaterData holds the FSM's internal state.
type TargetUpdaterData struct {
	targetUpdate TargetUpdate
	commandID    string
	requestedBy  gen.PID
	// datastore re-reads the live persisted target so a synthetic Resolve op can
	// close the TOCTOU window between changeset build and execute. It is read from
	// the node environment (the same handle every other actor shares) rather than
	// threaded through the supervisor, so it broadens no plumbing. May be nil in
	// unit harnesses that never wire the environment; re-validation then no-ops.
	datastore targetLoader
}

func NewTargetUpdater() gen.ProcessBehavior {
	return &TargetUpdater{}
}

func (t *TargetUpdater) Init(args ...any) (statemachine.StateMachineSpec[TargetUpdaterData], error) {
	data := TargetUpdaterData{
		requestedBy: args[0].(gen.PID),
	}

	// Read the shared datastore handle from the node environment so a synthetic
	// Resolve op can re-validate the target's revision at execute time. It is
	// asserted against the local targetLoader interface (not the concrete
	// datastore.Datastore) to avoid an import cycle — target_update is imported by
	// the datastore package. Absence is tolerated (some test harnesses run the FSM
	// without an environment): re-validation then no-ops and the op resolves
	// against its snapshot.
	if env, ok := t.Env("Datastore"); ok {
		if ds, ok := env.(targetLoader); ok {
			data.datastore = ds
		}
	}

	t.Log().Debug("TargetUpdater %s initialized", t.Name())

	return statemachine.NewStateMachineSpec(StateNotStarted,
		statemachine.WithData(data),
		statemachine.WithStateEnterCallback(onTargetUpdaterStateChange),
		// Not started — waiting for StartTargetUpdate message
		statemachine.WithStateMessageHandler(StateNotStarted, handleStartTargetUpdate),
		statemachine.WithStateMessageHandler(StateNotStarted, shutdownTargetUpdater),
		// Resolving state handlers
		statemachine.WithStateMessageHandler(StateResolving, targetValueResolved),
		statemachine.WithStateMessageHandler(StateResolving, targetFailedToResolve),
		statemachine.WithStateMessageHandler(StateResolving, targetResolveCacheTimeout),
		statemachine.WithStateMessageHandler(StateResolving, shutdownTargetUpdater),
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
// A Resolve op is always routed through resolveTargetConfig regardless of resolvables count, because Resolve
// ops must never call persistTarget — the resolve loop's drain path handles the zero-resolvables case directly.
func handleStartTargetUpdate(from gen.PID, state gen.Atom, data TargetUpdaterData, message StartTargetUpdate, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	data.targetUpdate = message.TargetUpdate
	data.commandID = message.CommandID

	// A synthetic Resolve op carries the target's config and revision as they were
	// at changeset build time. Before resolving, re-read the live persisted target:
	// if a concurrent command bumped its revision, resolve against the current
	// config instead of the stale snapshot; if the target was deleted, fail rather
	// than resolve a phantom credential.
	revised, err := revalidateResolveTarget(data.targetUpdate, data.datastore)
	if err != nil {
		proc.Log().Error("TargetUpdater: failed to re-validate resolve target target=%s: %v",
			data.targetUpdate.Target.Label, err)
		return StateFinishedWithError, data, nil, nil
	}
	data.targetUpdate = revised

	if len(data.targetUpdate.RemainingResolvables) > 0 || data.targetUpdate.Operation == TargetOperationResolve {
		return resolveTargetConfig(state, data, proc)
	}
	return persistTarget(data, proc)
}

// resolveTargetConfig pops the next resolvable and sends a ResolveValue request.
// resolvingTimeout sizes the ResolveCache timeout to outlive the cache's
// worst-case retry wall time: MaxRetries+1 reads (the initial read plus
// MaxRetries retries, each up to the plugin call timeout) plus the exponential
// backoff budget. It derives the backoff envelope from the same RetryConfig the
// ResolveCache reads, so a tuned policy cannot cause the two to drift and trip
// this timeout mid-retry.
func resolvingTimeout(proc gen.Process) time.Duration {
	// perAttempt mirrors resource_update.PluginOperationCallTimeout (60s). It is
	// duplicated as a local const because target_update must not import
	// resource_update (which imports target_update).
	const perAttempt = 60 * time.Second
	const margin = 30 * time.Second

	env, _ := proc.Env("RetryConfig")
	cfg, ok := env.(pkgmodel.RetryConfig)
	if !ok {
		// No environment (unit harness): a fixed, generous default.
		return perAttempt + margin
	}
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	return time.Duration(cfg.MaxRetries+1)*perAttempt + strategy.MaxTotalDelay() + margin
}

func resolveTargetConfig(state gen.Atom, data TargetUpdaterData, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	if len(data.targetUpdate.RemainingResolvables) == 0 {
		// A Resolve op resolves config in-memory only: the target row is never
		// written. Transition directly to success so the finished signal carries
		// the resolved config without touching the datastore.
		if data.targetUpdate.Operation == TargetOperationResolve {
			return StateFinishedSuccessfully, data, nil, nil
		}
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
		proc.Log().Error("TargetUpdater: failed to send ResolveValue uri=%v: %v", first, err)
		return StateFinishedWithError, data, nil, nil
	}

	timeout := statemachine.StateTimeout{
		Duration: resolvingTimeout(proc),
		Message:  ResolveTimedOut{},
	}

	return StateResolving, data, []statemachine.Action{timeout}, nil
}

// targetValueResolved handles a successfully resolved value.
func targetValueResolved(from gen.PID, state gen.Atom, data TargetUpdaterData, message messages.ValueResolved, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	err := data.targetUpdate.ResolveValue(message.ResourceURI, message.Value)
	if err != nil {
		proc.Log().Error("TargetUpdater: failed to resolve value uri=%v: %v", message.ResourceURI, err)
		return StateFinishedWithError, data, nil, nil
	}

	return resolveTargetConfig(state, data, proc)
}

// targetFailedToResolve handles a resolution failure.
func targetFailedToResolve(from gen.PID, state gen.Atom, data TargetUpdaterData, message messages.FailedToResolveValue, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	proc.Log().Error("TargetUpdater: failed to resolve target config property uri=%v", message.ResourceURI)
	return StateFinishedWithError, data, nil, nil
}

// targetResolveCacheTimeout handles the timeout when the resolve cache doesn't respond.
func targetResolveCacheTimeout(from gen.PID, state gen.Atom, data TargetUpdaterData, message ResolveTimedOut, proc gen.Process) (gen.Atom, TargetUpdaterData, []statemachine.Action, error) {
	proc.Log().Error("TargetUpdater: resolve cache timeout target=%s", data.targetUpdate.Target.Label)
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
		proc.Log().Error("TargetUpdater: failed to persist target update target=%s: %v", data.targetUpdate.Target.Label, err)
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

		proc.Log().Debug("TargetUpdater: sending TargetUpdateFinished to requester state=%s target=%s", newState, data.targetUpdate.Target.Label)
		finished := TargetUpdateFinished{
			NodeURI: data.targetUpdate.NodeURI(),
			State:   finalState,
		}
		if finalState == TargetUpdateStateSuccess {
			finished.ResolvedConfig = data.targetUpdate.Target.Config
		}
		err := proc.Send(data.requestedBy, finished)
		if err != nil {
			proc.Log().Error("TargetUpdater: failed to send TargetUpdateFinished to requester: %v", err)
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
