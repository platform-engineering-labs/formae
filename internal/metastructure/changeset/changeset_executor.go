// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"fmt"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	StateNotStarted           = gen.Atom("not_started")
	StateProcessing           = gen.Atom("processing")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
	StateCanceled             = gen.Atom("canceled")
)

// ChangesetExecutor is a state machine that processes a set of resource updates delivered by a forma. The changeset
// organizes the updates into a datastructure called the Pipeline, which contains groups of updates that can be executed
// in parallel. For each update, the executor will spawn a ResourceUpdater actor to handle the actual update and a
// ResolveCache to cache resolvables during the lifetime of the changeset execution. The ResourceUpdater reports the
// results back to the ChangesetExecutor, which updates the Pipeline and determines the next steps.
// Note: multiple changeset executors can be running in parallel, as long as they don't touch the same resources.
//
// The state machine transitions are as follows:
//
//                     +-----------------------+
//                     |      NotStarted       |
//                     +-----------------------+
//                                 |
//                                 |
//                                 |
//                                 v
//                     +-----------------------+
//                     |     Initializing      |
//                     +-----------------------+
//                       |                   |
//                       |                   |
//                       |                   |
//                       v                   v
//    +----------------------+            +----------------------+
//    | FinishedSuccessfully |            |  FinishedWithErrors  |
//    +----------------------+            +----------------------+

type ChangesetExecutor struct {
	statemachine.StateMachine[ChangesetData]
}

func NewChangesetExecutor() gen.ProcessBehavior {
	return &ChangesetExecutor{}
}

type Start struct {
	Changeset Changeset
}

type Resume struct{}

type Cancel struct {
	CommandID string
}

type ChangesetState string

const (
	ChangeSetStateFinishedSuccessfully ChangesetState = "FinishedSuccessfully"
	ChangeSetStateFinishedWithErrors   ChangesetState = "FinishedWithErrors"
)

type ChangesetData struct {
	changeset   Changeset
	requestedBy gen.PID
}

type RegisterEvents struct{}

type ChangesetCompleted struct {
	CommandID string
	State     ChangesetState
}

func (s *ChangesetExecutor) Init(args ...any) (statemachine.StateMachineSpec[ChangesetData], error) {
	if len(args) != 1 {
		return statemachine.StateMachineSpec[ChangesetData]{}, fmt.Errorf("expected 1 argument, got %d", len(args))
	}
	data := ChangesetData{
		requestedBy: args[0].(gen.PID),
	}

	return statemachine.NewStateMachineSpec(StateNotStarted,
		statemachine.WithData(data),
		statemachine.WithStateEnterCallback(onStateChange),
		statemachine.WithStateMessageHandler(StateNotStarted, start),
		statemachine.WithStateMessageHandler(StateProcessing, resourceUpdateFinished),
		statemachine.WithStateMessageHandler(StateProcessing, resume),
		statemachine.WithStateMessageHandler(StateProcessing, cancel),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdown),
		statemachine.WithStateMessageHandler(StateCanceled, shutdown),
	), nil
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data ChangesetData, proc gen.Process) (gen.Atom, ChangesetData, error) {
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError || newState == StateCanceled {
		_, err := proc.Call(
			gen.ProcessID{Node: proc.Node().Name(), Name: gen.Atom("FormaCommandPersister")},
			forma_persister.MarkFormaCommandAsComplete{
				CommandID: data.changeset.CommandID,
			})
		if err != nil {
			proc.Log().Error("Failed to hash all forma resources", "commandID", data.changeset.CommandID, "error", err)
		}

		err = proc.Send(
			gen.ProcessID{Node: proc.Node().Name(), Name: actornames.ResolveCache(data.changeset.CommandID)},
			Shutdown{},
		)
		if err != nil {
			proc.Log().Error("Failed to shutdown resolve cache", "commandID", data.changeset.CommandID, "error", err)
		}
		// Send ourselves a shutdown message to terminate the process.
		proc.Log().Debug("ChangesetExecutor: sending shutdown message to self", "state", newState)
		err = proc.Send(proc.PID(), Shutdown{})
		if err != nil {
			proc.Log().Error("ChangesetExecutor: failed to send terminate message: %v", err)
		}

		// Changesets can be requested by other actors or by the metastructure. The metastructure is not an actor (yet) and
		// uses the node to send messages. We can not send messages to the node. Once we migrated the metastructure onto an
		// actor we can remove this check.
		if data.requestedBy != proc.Node().PID() {
			// Send a message to the requester that the changeset has completed
			changesetState := ChangeSetStateFinishedSuccessfully
			if newState == StateFinishedWithError {
				changesetState = ChangeSetStateFinishedWithErrors
			}
			completed := ChangesetCompleted{
				CommandID: data.changeset.CommandID,
				State:     changesetState,
			}
			err = proc.Send(data.requestedBy, completed)
			if err != nil {
				proc.Log().Debug("Failed to send ChangesetCompleted event to requester", "error", err)
			}
		}
	}
	return newState, data, nil
}

func start(state gen.Atom, data ChangesetData, message Start, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	data.changeset = message.Changeset

	// Ensure the resolve cache is started
	_, err := proc.Call(
		gen.ProcessID{Node: proc.Node().Name(), Name: actornames.ChangesetSupervisor},
		EnsureResolveCache{
			CommandID: data.changeset.CommandID,
		})
	if err != nil {
		proc.Log().Error("Failed to ensure resource updater", "error", err)
		return StateFinishedWithError, data, nil, nil
	}

	// We should never receive a changeset with no updates. If we do, we error.
	if data.changeset.IsComplete() {
		proc.Log().Error("No resource updates were found for commandID %s", data.changeset.CommandID)
		return StateFinishedWithError, data, nil, nil
	}

	return resume(state, data, Resume{}, proc)
}

func resume(state gen.Atom, data ChangesetData, message Resume, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	finished := true
	availableUpdates := data.changeset.AvailableExecutableUpdates()

	for namespace, updates := range availableUpdates {
		tokens, err := proc.Call(actornames.RateLimiter, RequestTokens{Namespace: namespace, N: updates})
		if err != nil {
			proc.Log().Error("Failed to fetch tokens for namespace", "namespace", namespace, "error", err)
			return StateFinishedWithError, data, nil, nil
		}
		n := tokens.(TokensGranted).N
		if n < updates {
			finished = false
		}
		updates := data.changeset.GetExecutableUpdates(namespace, n)
		err = startResourceUpdates(updates, data.changeset.CommandID, proc)
		if err != nil {
			proc.Log().Error("Failed to start executable resource updates for changeset", "commandID", data.changeset.CommandID, "error", err)
			return StateFinishedWithError, data, nil, nil
		}
	}

	var actions []statemachine.Action
	if !finished {
		actions = append(actions,
			statemachine.GenericTimeout{
				Name:     gen.Atom(fmt.Sprintf("changeset-executor-rate-limit-%s", data.changeset.CommandID)),
				Duration: 1 * time.Second,
				Message:  Resume{},
			})
	}

	return StateProcessing, data, actions, nil
}

func resourceUpdateFinished(state gen.Atom, data ChangesetData, message resource_update.ResourceUpdateFinished, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	// Find the resource update that finished
	var finishedUpdate *resource_update.ResourceUpdate

	// Look through all groups to find the update with matching URI and state
	for _, group := range data.changeset.Pipeline.ResourceUpdateGroups {
		for _, update := range group.Updates {
			if update.URI() == message.Uri && update.State == resource_update.ResourceUpdateStateInProgress {
				finishedUpdate = update
				break
			}
		}
		if finishedUpdate != nil {
			break
		}
	}

	if finishedUpdate == nil {
		// Warn only if the URI is still tracked anywhere else it's likely been popped
		stillTracked := false
		for _, group := range data.changeset.Pipeline.ResourceUpdateGroups {
			for _, u := range group.Updates {
				if u.URI() == message.Uri {
					stillTracked = true
					break
				}
			}
			if stillTracked {
				break
			}
		}
		if stillTracked {
			proc.Log().Warning("Could not find finished resource update", "uri", message.Uri)
		} else {
			proc.Log().Debug("Finished resource update not found in active set (likely already popped)", "uri", message.Uri)
		}

		return state, data, nil, nil
	}

	// Update the state
	finishedUpdate.State = message.State

	// Update pipeline and get next executable updates and any failed updates
	cascadingFailures, err := data.changeset.UpdatePipeline(finishedUpdate)
	if err != nil {
		proc.Log().Error("Failed to update pipeline", "error", err)
		return StateFinishedWithError, data, nil, nil
	}

	// If we have cascading failures, we need to inform the forma command persister to mark those resource updates
	// as failed. For non-cascading failures the resource updater would already have taken care of this but as
	// we never spawn a resource updater for these dependent updates, we need to handle it here.
	if len(cascadingFailures) > 0 {
		proc.Log().Warning("Cascading failures detected", "failedCount", len(cascadingFailures), "commandID", data.changeset.CommandID)

		// Extract URIs of failed resources for bulk update
		var failedURIs []pkgmodel.FormaeURI
		for _, failedUpdate := range cascadingFailures {
			proc.Log().Debug("Resource marked as failed due to cascade",
				"uri", failedUpdate.URI(),
				"operation", failedUpdate.Operation,
				"originalFailure", message.Uri)
			failedURIs = append(failedURIs, failedUpdate.URI())
		}

		// Send bulk update to forma command persister
		_, err = proc.Call(
			gen.ProcessID{Node: proc.Node().Name(), Name: gen.Atom("FormaCommandPersister")},
			forma_persister.MarkResourcesAsFailed{
				CommandID:          data.changeset.CommandID,
				ResourceUris:       failedURIs,
				ResourceModifiedTs: util.TimeNow(),
			})
		if err != nil {
			proc.Log().Error("Failed to mark resources as failed in persister", "error", err, "commandID", data.changeset.CommandID)
		}
	}

	// Check if the changeset execution is complete
	if data.changeset.IsComplete() {
		proc.Log().Debug("Changeset execution finished for command", "commandID", data.changeset.CommandID)
		return StateFinishedSuccessfully, data, nil, nil
	}

	// Try to start the next batch of resource updates that can be executed in parallel
	return resume(state, data, Resume{}, proc)
}

func startResourceUpdates(updates []*resource_update.ResourceUpdate, commandID string, proc gen.Process) error {
	for _, update := range updates {
		proc.Log().Debug("Starting resource updater", "uri", update.URI(), "operation", update.Operation)

		_, err := proc.Call(gen.ProcessID{Name: actornames.ResourceUpdaterSupervisor, Node: proc.Node().Name()},
			resource_update.EnsureResourceUpdater{
				ResourceURI: update.URI(),
				Operation:   string(update.Operation),
				CommandID:   commandID,
			})
		if err != nil {
			proc.Log().Error("Failed to ensure resource updater", "error", err)
			return err
		}

		err = proc.Send(gen.ProcessID{Name: actornames.ResourceUpdater(update.URI(), string(update.Operation), commandID), Node: proc.Node().Name()},
			resource_update.StartResourceUpdate{
				ResourceUpdate: *update,
				CommandID:      commandID,
			})
		if err != nil {
			proc.Log().Error("Failed to send start message to resource updater", "error", err)
			return err
		}
	}

	return nil
}

func cancel(state gen.Atom, data ChangesetData, message Cancel, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	proc.Log().Info("ChangesetExecutor received cancel request", "commandID", message.CommandID)

	// Transition immediately to canceled state
	// The onStateChange callback will handle cleanup
	return StateCanceled, data, nil, nil
}

func shutdown(state gen.Atom, data ChangesetData, shutdown Shutdown, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}
