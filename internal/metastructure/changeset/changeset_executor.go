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
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	StateNotStarted           = gen.Atom("not_started")
	StateProcessing           = gen.Atom("processing")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
	StateCanceling            = gen.Atom("canceling")
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
	ChangeSetStateCanceled             ChangesetState = "Canceled"
)

type ChangesetData struct {
	changeset       Changeset
	requestedBy     gen.PID
	discoveryPaused bool // Tracks if this changeset paused Discovery
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
		statemachine.WithStateMessageHandler(StateCanceling, resourceUpdateFinished),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdown),
		statemachine.WithStateMessageHandler(StateCanceled, resourceUpdateFinished), // Ignore late messages from ResourceUpdaters
		statemachine.WithStateMessageHandler(StateCanceled, shutdown),
	), nil
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data ChangesetData, proc gen.Process) (gen.Atom, ChangesetData, error) {
	// Only shutdown when we reach a final state (not Canceling)
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError || newState == StateCanceled {
		// Resume Discovery if we paused it
		if data.discoveryPaused {
			discoveryPID := gen.ProcessID{Name: actornames.Discovery, Node: proc.Node().Name()}
			err := proc.Send(discoveryPID, messages.ResumeDiscovery{})
			if err != nil {
				proc.Log().Error("Failed to resume Discovery", "error", err, "commandID", data.changeset.CommandID)
			} else {
				proc.Log().Debug("Resumed Discovery after user changeset completed", "commandID", data.changeset.CommandID)
			}
		}

		// Shutdown resolve cache
		err := proc.Send(
			gen.ProcessID{Node: proc.Node().Name(), Name: actornames.ResolveCache(data.changeset.CommandID)},
			Shutdown{},
		)
		if err != nil {
			proc.Log().Error("Failed to shutdown resolve cache", "commandID", data.changeset.CommandID, "error", err)
		}

		// Send ourselves a shutdown message to terminate the process
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
			} else if newState == StateCanceled {
				changesetState = ChangeSetStateCanceled
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

	// Pause Discovery for user operations to prevent race conditions where Discovery
	// might list resources that are being created/modified by this changeset
	if changesetHasUserUpdates(data.changeset) {
		discoveryPID := gen.ProcessID{Name: actornames.Discovery, Node: proc.Node().Name()}
		_, err := proc.Call(discoveryPID, messages.PauseDiscovery{})
		if err != nil {
			proc.Log().Error("Failed to pause Discovery", "error", err, "commandID", data.changeset.CommandID)
			// Don't fail the operation - this is not critical enough to fail the user's operation
		} else {
			data.discoveryPaused = true
			proc.Log().Debug("Paused Discovery for user changeset", "commandID", data.changeset.CommandID)
		}
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
	// If we're in canceled state, ignore late resource update messages
	if state == StateCanceled {
		proc.Log().Debug("Ignoring ResourceUpdateFinished in canceled state", "uri", message.Uri)
		return state, data, nil, nil
	}

	// If we're in canceling state, we're waiting for in-progress resources to finish
	// Update the resource state and check if all in-progress resources are done
	if state == StateCanceling {
		// Find and update the finished resource
		for _, group := range data.changeset.Pipeline.ResourceUpdateGroups {
			for _, update := range group.Updates {
				if update.URI() == message.Uri && update.State == resource_update.ResourceUpdateStateInProgress {
					update.State = message.State
					proc.Log().Debug("In-progress resource finished during cancellation",
						"uri", message.Uri,
						"finalState", message.State)
					break
				}
			}
		}

		// Check if any resources are still in progress
		inProgressCount := 0
		for _, group := range data.changeset.Pipeline.ResourceUpdateGroups {
			for _, update := range group.Updates {
				if update.State == resource_update.ResourceUpdateStateInProgress {
					inProgressCount++
				}
			}
		}

		// If no more in-progress resources, transition to Canceled
		if inProgressCount == 0 {
			proc.Log().Debug("All in-progress resources completed, transitioning to Canceled",
				"commandID", data.changeset.CommandID)
			return StateCanceled, data, nil, nil
		}

		// Still waiting for more resources to finish
		proc.Log().Debug("Still waiting for in-progress resources during cancellation",
			"commandID", data.changeset.CommandID,
			"remainingCount", inProgressCount)
		return state, data, nil, nil
	}

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

	// Unregister non-sync resources from the Synchronizer
	// Now that the operation is complete, the resource can be included in sync operations again
	if finishedUpdate.Source != resource_update.FormaCommandSourceSynchronize {
		synchronizerPID := gen.ProcessID{Name: actornames.Synchronizer, Node: proc.Node().Name()}
		err := proc.Send(synchronizerPID, messages.UnregisterInProgressResource{
			ResourceURI: string(finishedUpdate.URI()),
		})
		if err != nil {
			proc.Log().Error("Failed to unregister in-progress resource from synchronizer", "error", err, "resourceURI", finishedUpdate.URI())
			// Don't return error - this is not critical enough to fail the operation
		}
	}

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
	// The command state and sensitive data hashing are handled automatically by FormaCommandPersister
	// when individual resources complete via markResourceUpdateAsComplete or bulk updates
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

		// Register non-sync resources as in-progress with the Synchronizer
		// to prevent race conditions where sync might include resources being updated by user operations
		if update.Source != resource_update.FormaCommandSourceSynchronize {
			synchronizerPID := gen.ProcessID{Name: actornames.Synchronizer, Node: proc.Node().Name()}
			err := proc.Send(synchronizerPID, messages.RegisterInProgressResource{
				ResourceURI: string(update.URI()),
			})
			if err != nil {
				proc.Log().Error("Failed to register in-progress resource with synchronizer", "error", err, "resourceURI", update.URI())
				// Don't return error - this is not critical enough to fail the operation
			}
		}

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
	proc.Log().Debug("ChangesetExecutor received cancel request", "commandID", message.CommandID)

	// Collect resources by state
	var urisToCancel []pkgmodel.FormaeURI
	var inProgressCount int

	for _, group := range data.changeset.Pipeline.ResourceUpdateGroups {
		for _, update := range group.Updates {
			// Only cancel resources that haven't started yet (NotStarted state)
			// Do NOT cancel InProgress resources to avoid orphaned cloud resources
			if update.State == resource_update.ResourceUpdateStateNotStarted {
				urisToCancel = append(urisToCancel, update.URI())
			} else if update.State == resource_update.ResourceUpdateStateInProgress {
				// Count in-progress resources - we need to wait for these to complete
				inProgressCount++
			}
		}
	}

	// Mark NotStarted resources as canceled
	if len(urisToCancel) > 0 {
		_, err := proc.Call(
			gen.ProcessID{Node: proc.Node().Name(), Name: gen.Atom("FormaCommandPersister")},
			forma_persister.MarkResourcesAsCanceled{
				CommandID:    data.changeset.CommandID,
				ResourceUris: urisToCancel,
			},
		)
		if err != nil {
			proc.Log().Error("Failed to mark resources as canceled", "commandID", data.changeset.CommandID, "error", err)
		}

		proc.Log().Debug("Marked NotStarted resources as canceled",
			"commandID", message.CommandID,
			"canceledCount", len(urisToCancel))
	}

	// Determine next state
	var nextState gen.Atom
	if inProgressCount > 0 {
		// We have in-progress resources - transition to Canceling state and wait for them to finish
		proc.Log().Debug("Command is canceling, waiting for in-progress resources to complete",
			"commandID", message.CommandID,
			"inProgressCount", inProgressCount)
		nextState = StateCanceling
	} else {
		// No in-progress resources - transition directly to Canceled
		proc.Log().Debug("Command canceled immediately (no in-progress resources)",
			"commandID", message.CommandID)
		nextState = StateCanceled
	}

	return nextState, data, nil, nil
}

func shutdown(state gen.Atom, data ChangesetData, shutdown Shutdown, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

// changesetHasUserUpdates checks if the changeset contains any updates from user operations.
// Returns true if at least one update has Source == FormaCommandSourceUser.
func changesetHasUserUpdates(changeset Changeset) bool {
	for _, group := range changeset.Pipeline.ResourceUpdateGroups {
		for _, update := range group.Updates {
			if update.Source == resource_update.FormaCommandSourceUser {
				return true
			}
		}
	}
	return false
}
