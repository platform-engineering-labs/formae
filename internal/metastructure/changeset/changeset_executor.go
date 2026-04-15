// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"fmt"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
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
// organizes the updates into a datastructure called the DAG, which contains groups of updates that can be executed
// in parallel. For each update, the executor will spawn a ResourceUpdater actor to handle the actual update and a
// ResolveCache to cache resolvables during the lifetime of the changeset execution. The ResourceUpdater reports the
// results back to the ChangesetExecutor, which updates the DAG and determines the next steps.
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
	Changeset        Changeset
	NotifyOnComplete bool // If true, send ChangesetCompleted to the sender when done
}

type Resume struct{}

type Cancel struct {
	CommandID string
}

// CancelResponse contains per-resource-update states at cancel time.
type CancelResponse struct {
	ResourceStates map[string]string // URI → state ("Canceled", "InProgress", "Success", "Failed")
}

type ChangesetState string

const (
	ChangeSetStateFinishedSuccessfully ChangesetState = "FinishedSuccessfully"
	ChangeSetStateFinishedWithErrors   ChangesetState = "FinishedWithErrors"
	ChangeSetStateCanceled             ChangesetState = "Canceled"
)

type ChangesetData struct {
	changeset                Changeset
	requestedBy              gen.PID
	notifyOnComplete         bool     // If true, send ChangesetCompleted to requestedBy when done
	discoveryPaused          bool     // Tracks if this changeset paused Discovery
	stacksWithDeletes        []string // Stacks that had delete operations, captured at start
	syncExcludedResourceURIs []string // Resource URIs registered with Synchronizer, captured at start
}

type RegisterEvents struct{}

type ChangesetCompleted struct {
	CommandID string
	State     ChangesetState
}

func (s *ChangesetExecutor) Init(args ...any) (statemachine.StateMachineSpec[ChangesetData], error) {
	data := ChangesetData{}

	return statemachine.NewStateMachineSpec(StateNotStarted,
		statemachine.WithData(data),
		statemachine.WithStateEnterCallback(onStateChange),
		statemachine.WithStateMessageHandler(StateNotStarted, start),
		statemachine.WithStateMessageHandler(StateProcessing, resourceUpdateFinished),
		statemachine.WithStateMessageHandler(StateProcessing, targetUpdateFinished),
		statemachine.WithStateMessageHandler(StateProcessing, resume),
		statemachine.WithStateCallHandler(StateProcessing, cancel),
		statemachine.WithStateMessageHandler(StateCanceling, resourceUpdateFinished),
		statemachine.WithStateMessageHandler(StateCanceling, targetUpdateFinished),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdown),
		statemachine.WithStateMessageHandler(StateCanceled, resourceUpdateFinished), // Ignore late messages from ResourceUpdaters
		statemachine.WithStateMessageHandler(StateCanceled, targetUpdateFinished),   // Ignore late messages from TargetUpdaters
		statemachine.WithStateMessageHandler(StateCanceled, shutdown),
	), nil
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data ChangesetData, proc gen.Process) (gen.Atom, ChangesetData, error) {
	// Cleanup empty stacks after successful completion
	if newState == StateFinishedSuccessfully {
		// Use the stacks captured at start, since the DAG is empty by now
		proc.Log().Debug("Using pre-captured stacks for cleanup stacks=%v commandID=%s", data.stacksWithDeletes, data.changeset.CommandID)
		if len(data.stacksWithDeletes) > 0 {
			resourcePersisterPID := gen.ProcessID{Name: actornames.ResourcePersister, Node: proc.Node().Name()}
			// Use Send instead of Call - we don't need the response (CleanupEmptyStacks returns nil),
			// and Call can timeout in state enter callbacks due to Ergo framework limitations.
			err := proc.Send(resourcePersisterPID, messages.CleanupEmptyStacks{
				StackLabels: data.stacksWithDeletes,
				CommandID:   data.changeset.CommandID,
			})
			if err != nil {
				proc.Log().Error("Failed to send CleanupEmptyStacks message commandID=%s: %v", data.changeset.CommandID, err)
			}
		}
	}

	// Only shutdown when we reach a final state (not Canceling)
	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError || newState == StateCanceled {
		// Resume Discovery if we paused it
		if data.discoveryPaused {
			discoveryPID := gen.ProcessID{Name: actornames.Discovery, Node: proc.Node().Name()}
			err := proc.Send(discoveryPID, messages.ResumeDiscovery{})
			if err != nil {
				proc.Log().Error("Failed to resume Discovery commandID=%s: %v", data.changeset.CommandID, err)
			} else {
				proc.Log().Debug("Resumed Discovery after user changeset completed commandID=%s", data.changeset.CommandID)
			}
		}

		// Unregister all resources from the Synchronizer so they can be
		// included in future sync cycles. This is the counterpart to
		// registerAllResourcesWithSynchronizer called in start().
		if len(data.syncExcludedResourceURIs) > 0 {
			unregisterAllResourcesFromSynchronizer(data.syncExcludedResourceURIs, proc)
		}

		// Shutdown resolve cache
		err := proc.Send(
			gen.ProcessID{Node: proc.Node().Name(), Name: actornames.ResolveCache(data.changeset.CommandID)},
			Shutdown{},
		)
		if err != nil {
			proc.Log().Error("Failed to shutdown resolve cache commandID=%s: %v", data.changeset.CommandID, err)
		}

		// Send ourselves a shutdown message to terminate the process
		proc.Log().Debug("ChangesetExecutor: sending shutdown message to self state=%s", newState)
		err = proc.Send(proc.PID(), Shutdown{})
		if err != nil {
			proc.Log().Error("ChangesetExecutor: failed to send terminate message: %v", err)
		}

		// Only send completion notification if the requester asked for it
		if data.notifyOnComplete {
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
				proc.Log().Debug("Failed to send ChangesetCompleted event to requester: %v", err)
			}
		}
	}
	return newState, data, nil
}

func start(from gen.PID, state gen.Atom, data ChangesetData, message Start, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	data.requestedBy = from
	data.changeset = message.Changeset
	data.notifyOnComplete = message.NotifyOnComplete

	// Capture stacks with delete operations NOW, before the DAG is modified during execution
	data.stacksWithDeletes = collectStacksWithDeletes(data.changeset.DAG)

	// Ensure the resolve cache is started
	_, err := proc.Call(
		gen.ProcessID{Node: proc.Node().Name(), Name: actornames.ChangesetSupervisor},
		EnsureResolveCache{
			CommandID: data.changeset.CommandID,
		})
	if err != nil {
		proc.Log().Error("Failed to ensure resource updater: %v", err)
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
			proc.Log().Error("Failed to pause Discovery commandID=%s: %v", data.changeset.CommandID, err)
			// Don't fail the operation - this is not critical enough to fail the user's operation
		} else {
			data.discoveryPaused = true
			proc.Log().Debug("Paused Discovery for user changeset commandID=%s", data.changeset.CommandID)
		}
	}

	// Register ALL non-sync resources with the Synchronizer upfront to prevent
	// a race where a sync cycle could read (and persist) OOB changes for a
	// resource before the ResourceUpdater's own sync-Read runs. Without this,
	// the sync-Read would see no diff (sync already equalised DB and cloud)
	// and the update would proceed when it should have been rejected.
	if changesetHasUserUpdates(data.changeset) {
		data.syncExcludedResourceURIs = registerAllResourcesWithSynchronizer(data.changeset.DAG, proc)
	}

	return resume(from, state, data, Resume{}, proc)
}

// registerAllResourcesWithSynchronizer sends RegisterInProgressResource for
// every non-sync resource in the DAG. This must happen before any ResourceUpdater
// starts so that a concurrent sync cycle cannot slip in and read a resource that
// is about to be updated. Returns the list of registered URIs so they can be
// unregistered when the changeset reaches a terminal state.
func registerAllResourcesWithSynchronizer(dag *ExecutionDAG, proc gen.Process) []string {
	synchronizerPID := gen.ProcessID{Name: actornames.Synchronizer, Node: proc.Node().Name()}
	var registeredURIs []string
	for _, node := range dag.Nodes {
		ru, ok := node.Update.(*resource_update.ResourceUpdate)
		if !ok {
			continue
		}
		if ru.Source == resource_update.FormaCommandSourceSynchronize {
			continue
		}
		uri := string(ru.URI())
		err := proc.Send(synchronizerPID, messages.RegisterInProgressResource{
			ResourceURI: uri,
		})
		if err != nil {
			proc.Log().Error("Failed to register in-progress resource with synchronizer resourceURI=%v: %v",
				ru.URI(), err)
		}
		registeredURIs = append(registeredURIs, uri)
	}
	return registeredURIs
}

// unregisterAllResourcesFromSynchronizer sends UnregisterInProgressResource for
// every resource URI that was registered at changeset start. Uses the saved list
// rather than the DAG because nodes are removed from the DAG as they complete.
func unregisterAllResourcesFromSynchronizer(resourceURIs []string, proc gen.Process) {
	synchronizerPID := gen.ProcessID{Name: actornames.Synchronizer, Node: proc.Node().Name()}
	for _, uri := range resourceURIs {
		err := proc.Send(synchronizerPID, messages.UnregisterInProgressResource{
			ResourceURI: uri,
		})
		if err != nil {
			proc.Log().Error("Failed to unregister in-progress resource from synchronizer resourceURI=%s: %v",
				uri, err)
		}
	}
}

func resume(from gen.PID, state gen.Atom, data ChangesetData, message Resume, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	finished := true
	availableUpdates := data.changeset.AvailableExecutableUpdates()

	for namespace, updates := range availableUpdates {
		tokens, err := proc.Call(actornames.RateLimiter, RequestTokens{Namespace: namespace, N: updates})
		if err != nil {
			proc.Log().Error("Failed to fetch tokens for namespace=%s: %v", namespace, err)
			return StateFinishedWithError, data, nil, nil
		}
		n := tokens.(TokensGranted).N
		if n < updates {
			finished = false
		}
		updates := data.changeset.GetExecutableUpdates(namespace, n)
		err = startUpdates(updates, data.changeset.CommandID, proc)
		if err != nil {
			proc.Log().Error("Failed to start executable updates for changeset commandID=%s: %v", data.changeset.CommandID, err)
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

// updateFinishedEvent normalizes the incoming completion message from either
// a ResourceUpdater or TargetUpdater into a common shape for handleUpdateFinished.
type updateFinishedEvent struct {
	nodeURI   pkgmodel.FormaeURI // URI to look up in the DAG
	isSuccess bool
}

func resourceUpdateFinished(from gen.PID, state gen.Atom, data ChangesetData, message resource_update.ResourceUpdateFinished, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	// Build the DAG key — resources use operation-qualified URIs
	ru := findRunningUpdate[*resource_update.ResourceUpdate](data, message.Uri)

	var dagKey pkgmodel.FormaeURI
	if ru != nil {
		dagKey = createOperationURI(ru.URI(), ru.Operation)
	}

	return handleUpdateFinished(from, state, data, updateFinishedEvent{
		nodeURI:   dagKey,
		isSuccess: message.State == resource_update.ResourceUpdateStateSuccess,
	}, proc)
}

func targetUpdateFinished(from gen.PID, state gen.Atom, data ChangesetData, message target_update.TargetUpdateFinished, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	// Notify the FormaCommandPersister about the target update completion.
	// This is done here (not in the TargetUpdater) to avoid an import cycle
	// between target_update and messages packages.
	node, exists := data.changeset.DAG.Nodes[message.NodeURI]
	if exists {
		if tu, ok := node.Update.(*target_update.TargetUpdate); ok {
			_, err := proc.Call(
				gen.ProcessID{Name: gen.Atom("FormaCommandPersister"), Node: proc.Node().Name()},
				messages.MarkTargetUpdateAsComplete{
					CommandID:       data.changeset.CommandID,
					TargetLabel:     tu.Target.Label,
					TargetOperation: string(tu.Operation),
					FinalState:      message.State,
					ModifiedTs:      util.TimeNow(),
				},
			)
			if err != nil {
				proc.Log().Error("Failed to update target state in persister target=%s: %v", tu.Target.Label, err)
			}
		}
	}

	// After a target resolves successfully, propagate the resolved config to
	// all downstream resource updates that reference this target. Without this,
	// resource updaters would use the stale snapshot from generation time which
	// still contains unresolved $ref objects.
	if message.State == target_update.TargetUpdateStateSuccess && message.ResolvedConfig != nil {
		if tu, ok := node.Update.(*target_update.TargetUpdate); ok {
			// Convert the resolved config to plugin format: strip $ref/$value
			// metadata so plugins receive plain values.
			pluginConfig, err := resolver.ConvertToPluginFormat(message.ResolvedConfig)
			if err != nil {
				proc.Log().Error("Failed to convert target config to plugin format target=%s: %v", tu.Target.Label, err)
				pluginConfig = message.ResolvedConfig
			}
			for _, n := range data.changeset.DAG.Nodes {
				if ru, ok := n.Update.(*resource_update.ResourceUpdate); ok {
					if ru.DesiredState.Target == tu.Target.Label {
						ru.ResourceTarget.Config = pluginConfig
					}
				}
			}
		}
	}

	return handleUpdateFinished(from, state, data, updateFinishedEvent{
		nodeURI:   message.NodeURI,
		isSuccess: message.State == target_update.TargetUpdateStateSuccess,
	}, proc)
}

// findRunningUpdate searches the DAG for a running update matching messageURI and returns
// it cast to T, or nil if not found.
func findRunningUpdate[T Update](data ChangesetData, messageURI pkgmodel.FormaeURI) T {
	var zero T
	for _, node := range data.changeset.DAG.Nodes {
		if node.Update.NodeURI() == messageURI && node.Update.IsRunning() {
			if typed, ok := node.Update.(T); ok {
				return typed
			}
		}
	}
	return zero
}

func handleUpdateFinished(from gen.PID, state gen.Atom, data ChangesetData, event updateFinishedEvent, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	// If we're in canceled state, ignore late messages
	if state == StateCanceled {
		proc.Log().Debug("Ignoring update finished in canceled state nodeURI=%v", event.nodeURI)
		return state, data, nil, nil
	}

	// If we're in canceling state, update the node and check if all in-progress are done
	if state == StateCanceling {
		for _, node := range data.changeset.DAG.Nodes {
			if node.Update.NodeURI() == event.nodeURI && node.Update.IsRunning() {
				if event.isSuccess {
					// Mark via the Update interface — no type-specific knowledge needed
					// (success isn't on the interface, but we can leave it running;
					// the canceling path only needs to drain, not report)
				} else {
					node.Update.MarkFailed()
				}
				proc.Log().Debug("In-progress update finished during cancellation nodeURI=%v success=%v",
					event.nodeURI, event.isSuccess)
				break
			}
		}

		inProgressCount := 0
		for _, node := range data.changeset.DAG.Nodes {
			if node.IsRunning() {
				inProgressCount++
			}
		}

		if inProgressCount == 0 {
			proc.Log().Debug("All in-progress updates completed, transitioning to Canceled commandID=%s",
				data.changeset.CommandID)
			return StateCanceled, data, nil, nil
		}

		proc.Log().Debug("Still waiting for in-progress updates during cancellation commandID=%s remainingCount=%d",
			data.changeset.CommandID, inProgressCount)
		return state, data, nil, nil
	}

	// Find the finished node in the DAG
	node, exists := data.changeset.DAG.Nodes[event.nodeURI]
	if !exists || !node.Update.IsRunning() {
		proc.Log().Debug("Update finished but not found in active DAG (likely already completed) nodeURI=%v", event.nodeURI)
		return state, data, nil, nil
	}

	// Update the state on the underlying Update
	if event.isSuccess {
		// Success is tracked by the specific Update type; we set it via type switch
		// because the Update interface only has MarkFailed/MarkInProgress.
		switch u := node.Update.(type) {
		case *resource_update.ResourceUpdate:
			u.State = resource_update.ResourceUpdateStateSuccess
		case *target_update.TargetUpdate:
			u.State = target_update.TargetUpdateStateSuccess
		}
	} else {
		node.Update.MarkFailed()
	}

	// Update DAG and get any cascading failures
	cascadingFailures, err := data.changeset.UpdateDAG(event.nodeURI, node.Update)
	if err != nil {
		proc.Log().Error("Failed to update DAG nodeURI=%v: %v", event.nodeURI, err)
		return StateFinishedWithError, data, nil, nil
	}

	// Persist cascading failures via FormaCommandPersister.
	if len(cascadingFailures) > 0 {
		var failedResources []forma_persister.ResourceUpdateRef
		var failedTargets []forma_persister.TargetUpdateRef
		for _, failedUpdate := range cascadingFailures {
			// Skip the original failure — its updater actor already persisted it.
			if failedUpdate.NodeURI() == event.nodeURI {
				continue
			}
			switch u := failedUpdate.(type) {
			case *resource_update.ResourceUpdate:
				proc.Log().Debug("Resource marked as failed due to cascade uri=%v operation=%s originalFailure=%v",
					u.URI(), u.Operation, event.nodeURI)
				failedResources = append(failedResources, forma_persister.ResourceUpdateRef{
					URI:       u.URI(),
					Operation: u.Operation,
				})
			case *target_update.TargetUpdate:
				proc.Log().Debug("Target marked as failed due to cascade label=%s operation=%s originalFailure=%v",
					u.Target.Label, u.Operation, event.nodeURI)
				failedTargets = append(failedTargets, forma_persister.TargetUpdateRef{
					Label:     u.Target.Label,
					Operation: u.Operation,
				})
			}
		}
		if len(failedResources) > 0 {
			proc.Log().Warning("Cascading failures detected: %d for command %s", len(failedResources), data.changeset.CommandID)
		}

		persisterPID := gen.ProcessID{Node: proc.Node().Name(), Name: gen.Atom("FormaCommandPersister")}
		now := util.TimeNow()

		if len(failedResources) > 0 {
			_, err = proc.Call(persisterPID, forma_persister.MarkResourcesAsFailed{
				CommandID:          data.changeset.CommandID,
				Resources:          failedResources,
				ResourceModifiedTs: now,
			})
			if err != nil {
				proc.Log().Error("Failed to mark resources as failed in persister commandID=%s: %v", data.changeset.CommandID, err)
			}
		}

		if len(failedTargets) > 0 {
			_, err = proc.Call(persisterPID, forma_persister.MarkTargetsAsFailed{
				CommandID:        data.changeset.CommandID,
				Targets:          failedTargets,
				TargetModifiedTs: now,
			})
			if err != nil {
				proc.Log().Error("Failed to mark targets as failed in persister commandID=%s: %v", data.changeset.CommandID, err)
			}
		}
	}

	if data.changeset.IsComplete() {
		proc.Log().Debug("Changeset execution finished for command commandID=%s", data.changeset.CommandID)
		return StateFinishedSuccessfully, data, nil, nil
	}

	return resume(from, state, data, Resume{}, proc)
}

func startUpdates(updates []Update, commandID string, proc gen.Process) error {
	for _, update := range updates {
		switch u := update.(type) {
		case *resource_update.ResourceUpdate:
			if err := startResourceUpdate(u, commandID, proc); err != nil {
				return err
			}
		case *target_update.TargetUpdate:
			if err := startTargetUpdate(u, commandID, proc); err != nil {
				return err
			}
		default:
			proc.Log().Error("Unknown update type in startUpdates uri=%v", update.NodeURI())
		}
	}

	return nil
}

func startResourceUpdate(ru *resource_update.ResourceUpdate, commandID string, proc gen.Process) error {
	proc.Log().Debug("Starting resource updater uri=%v operation=%s", ru.URI(), ru.Operation)

	// Sync exclusion is handled in bulk by registerAllResourcesWithSynchronizer
	// at changeset start and unregisterAllResourcesFromSynchronizer at changeset
	// end. This guarantees atomic protection for the entire stack — all resources
	// in the changeset are excluded from sync for the full duration, with a clean
	// unregister when the changeset reaches a terminal state.

	_, err := proc.Call(gen.ProcessID{Name: actornames.ResourceUpdaterSupervisor, Node: proc.Node().Name()},
		resource_update.EnsureResourceUpdater{
			ResourceURI: ru.URI(),
			Operation:   string(ru.Operation),
			CommandID:   commandID,
		})
	if err != nil {
		proc.Log().Error("Failed to ensure resource updater: %v", err)
		return err
	}

	err = proc.Send(gen.ProcessID{Name: actornames.ResourceUpdater(ru.URI(), string(ru.Operation), commandID), Node: proc.Node().Name()},
		resource_update.StartResourceUpdate{
			ResourceUpdate: *ru,
			CommandID:      commandID,
		})
	if err != nil {
		proc.Log().Error("Failed to send start message to resource updater: %v", err)
		return err
	}

	return nil
}

func startTargetUpdate(tu *target_update.TargetUpdate, commandID string, proc gen.Process) error {
	label := tu.Target.Label
	operation := string(tu.Operation)

	proc.Log().Debug("Starting target updater label=%s operation=%s", label, operation)

	_, err := proc.Call(
		gen.ProcessID{Name: actornames.TargetUpdaterSupervisor, Node: proc.Node().Name()},
		target_update.EnsureTargetUpdater{
			Label:     label,
			Operation: operation,
			CommandID: commandID,
		},
	)
	if err != nil {
		proc.Log().Error("Failed to ensure target updater: %v", err)
		return err
	}

	err = proc.Send(
		gen.ProcessID{Name: actornames.TargetUpdater(label, operation, commandID), Node: proc.Node().Name()},
		target_update.StartTargetUpdate{
			TargetUpdate: *tu,
			CommandID:    commandID,
		},
	)
	if err != nil {
		proc.Log().Error("Failed to send start message to target updater: %v", err)
		return err
	}

	return nil
}

func cancel(from gen.PID, state gen.Atom, data ChangesetData, message Cancel, proc gen.Process) (gen.Atom, ChangesetData, CancelResponse, []statemachine.Action, error) {
	proc.Log().Debug("ChangesetExecutor received cancel request commandID=%s", message.CommandID)

	// Collect resources by state
	var resourcesToCancel []forma_persister.ResourceUpdateRef
	var inProgressCount int
	resourceStates := make(map[string]string)

	for _, node := range data.changeset.DAG.Nodes {
		ru, ok := node.Update.(*resource_update.ResourceUpdate)
		if !ok {
			continue
		}
		uri := string(ru.URI())
		switch ru.State {
		case resource_update.ResourceUpdateStateNotStarted:
			resourceStates[uri] = "Canceled"
			resourcesToCancel = append(resourcesToCancel, forma_persister.ResourceUpdateRef{
				URI:       ru.URI(),
				Operation: ru.Operation,
			})
		case resource_update.ResourceUpdateStateInProgress:
			resourceStates[uri] = "InProgress"
			inProgressCount++
		case resource_update.ResourceUpdateStateSuccess:
			resourceStates[uri] = "Success"
		case resource_update.ResourceUpdateStateFailed:
			resourceStates[uri] = "Failed"
		case resource_update.ResourceUpdateStateCanceled:
			resourceStates[uri] = "Canceled"
		default:
			resourceStates[uri] = string(ru.State)
		}
	}

	// Mark NotStarted resources as canceled
	if len(resourcesToCancel) > 0 {
		_, err := proc.Call(
			gen.ProcessID{Node: proc.Node().Name(), Name: gen.Atom("FormaCommandPersister")},
			forma_persister.MarkResourcesAsCanceled{
				CommandID: data.changeset.CommandID,
				Resources: resourcesToCancel,
			},
		)
		if err != nil {
			proc.Log().Error("Failed to mark resources as canceled commandID=%s: %v", data.changeset.CommandID, err)
		}

		proc.Log().Debug("Marked NotStarted resources as canceled commandID=%s canceledCount=%d",
			message.CommandID, len(resourcesToCancel))
	}

	cancelResp := CancelResponse{ResourceStates: resourceStates}

	// Determine next state
	var nextState gen.Atom
	if inProgressCount > 0 {
		proc.Log().Debug("Command is canceling, waiting for in-progress resources to complete commandID=%s inProgressCount=%d",
			message.CommandID, inProgressCount)
		nextState = StateCanceling
	} else {
		proc.Log().Debug("Command canceled immediately (no in-progress resources) commandID=%s",
			message.CommandID)
		nextState = StateCanceled
	}

	return nextState, data, cancelResp, nil, nil
}

func shutdown(from gen.PID, state gen.Atom, data ChangesetData, shutdown Shutdown, proc gen.Process) (gen.Atom, ChangesetData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

// changesetHasUserUpdates checks if the changeset contains any updates from user operations.
// Returns true if at least one update has Source == FormaCommandSourceUser.
func changesetHasUserUpdates(changeset Changeset) bool {
	for _, node := range changeset.DAG.Nodes {
		if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
			if ru.Source == resource_update.FormaCommandSourceUser {
				return true
			}
		}
	}
	return false
}

// collectStacksWithDeletes returns a list of unique stack labels that had delete operations
// in the DAG, excluding the unmanaged stack.
func collectStacksWithDeletes(dag *ExecutionDAG) []string {
	stackSet := make(map[string]struct{})
	for _, node := range dag.Nodes {
		if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
			if ru.Operation == resource_update.OperationDelete || ru.Operation == resource_update.OperationReplace {
				stackLabel := ru.StackLabel
				if stackLabel != "" && stackLabel != constants.UnmanagedStack {
					stackSet[stackLabel] = struct{}{}
				}
			}
		}
	}

	stacks := make([]string, 0, len(stackSet))
	for stack := range stackSet {
		stacks = append(stacks, stack)
	}
	return stacks
}
