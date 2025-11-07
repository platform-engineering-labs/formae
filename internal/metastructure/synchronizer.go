// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Synchronizer is the actor responsible for keeping all resources in inventory in sync with the deployed
// infrastructure. It runs on a scheduled interval.
//
// The state transitions are as follows:
//
//				+-----------------------+
//			 -->|         Idle          |---
//			|   +-----------------------+   |
//			|                               |
//			|                               |
//			|                               |
//			|                               |
//			|                               |                 -
//			|                               |
//			|   +-----------------------+   |
//			 ---|     Synchronizing     |<---
//				+-----------------------+

func factory_Synchronizer() gen.ProcessBehavior {
	return &Synchronizer{}
}

type SynchronizeSingleResource struct {
	Uri pkgmodel.FormaeURI
}

type ResourceSynchronized struct {
	Uri pkgmodel.FormaeURI
}

const (
	StateIdle          = gen.Atom("idle")
	StateSynchronizing = gen.Atom("synchronizing")
)

type Synchronizer struct {
	statemachine.StateMachine[SynchronizerData]
}

type SynchronizerData struct {
	datastore datastore.Datastore
	cfg       pkgmodel.SynchronizationConfig

	isScheduledSync bool
	timeStarted     time.Time
	commandID       string

	// excludedResources tracks resources that are currently being updated by
	// non-sync operations (e.g., user apply/destroy commands). These resources
	// should be excluded from synchronization to prevent race conditions.
	excludedResources map[string]struct{}
}

// Messages processed by Synchronizer

type Synchronize struct {
	Once bool
}

func (s *Synchronizer) Init(args ...any) (statemachine.StateMachineSpec[SynchronizerData], error) {
	ds, ok := s.Env("Datastore")
	if !ok {
		s.Log().Error("Missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[SynchronizerData]{}, fmt.Errorf("synchronizer: missing 'Datastore' environment variable")
	}

	cfg, ok := s.Env("SynchronizationConfig")
	if !ok {
		s.Log().Error("Missing 'SynchronizationConfig' environment variable")
		return statemachine.StateMachineSpec[SynchronizerData]{}, fmt.Errorf("synchronizer: missing 'SynchronizationConfig' environment variable")
	}

	synchronizerCfg := cfg.(pkgmodel.SynchronizationConfig)
	if !synchronizerCfg.Enabled {
		s.Log().Warning("Synchronization is disabled")
	}

	data := SynchronizerData{
		datastore:         ds.(datastore.Datastore),
		cfg:               synchronizerCfg,
		excludedResources: make(map[string]struct{}),
	}

	spec := statemachine.NewStateMachineSpec(StateIdle,
		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateMessageHandler(StateIdle, synchronize),
		statemachine.WithStateMessageHandler(StateSynchronizing, synchronize),
		statemachine.WithStateMessageHandler(StateSynchronizing, changesetCompleted),

		// Handle resource exclusion in both states
		statemachine.WithStateMessageHandler(StateIdle, registerInProgressResource),
		statemachine.WithStateMessageHandler(StateSynchronizing, registerInProgressResource),
		statemachine.WithStateMessageHandler(StateIdle, unregisterInProgressResource),
		statemachine.WithStateMessageHandler(StateSynchronizing, unregisterInProgressResource),
	)

	if synchronizerCfg.Enabled {
		if err := s.Send(s.PID(), Synchronize{}); err != nil {
			return statemachine.StateMachineSpec[SynchronizerData]{}, fmt.Errorf("failed to send initial synchronize message: %s", err)
		}
		s.Log().Info("Resource synchronization ready")
	}

	return spec, nil
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data SynchronizerData, proc gen.Process) (gen.Atom, SynchronizerData, error) {
	if oldState == StateSynchronizing && newState == StateIdle {
		proc.Log().Debug("Synchronization finished", "duration", time.Since(data.timeStarted))
		if err := scheduleNextSync(data, proc); err != nil {
			return newState, data, gen.TerminateReasonPanic
		}
	}
	return newState, data, nil
}

func synchronize(state gen.Atom, data SynchronizerData, message Synchronize, proc gen.Process) (gen.Atom, SynchronizerData, []statemachine.Action, error) {
	data.isScheduledSync = !message.Once
	data.timeStarted = time.Now()

	if state == StateSynchronizing {
		proc.Log().Debug("Synchronizer already running, consider configuring a longer interval")
		return state, data, nil, nil
	}
	proc.Log().Debug("Starting resource synchronization", "timestamp", data.timeStarted)

	return synchronizeAllResources(state, data, proc)
}

func synchronizeAllResources(state gen.Atom, data SynchronizerData, proc gen.Process) (gen.Atom, SynchronizerData, []statemachine.Action, error) {
	stacks, err := data.datastore.LoadAllStacks()
	if err != nil {
		proc.Log().Error("failed to load stacks: %w", err)
		return state, data, nil, gen.TerminateReasonPanic
	}

	existingTargets, err := data.datastore.LoadAllTargets()
	if err != nil {
		proc.Log().Error("failed to load targets: %w", err)
		return state, data, nil, gen.TerminateReasonPanic
	}

	var allResourceUpdates []resource_update.ResourceUpdate
	for _, stack := range stacks {
		resourceUpdates, err := resource_update.GenerateResourceUpdates(
			stack,
			pkgmodel.CommandSync,
			pkgmodel.FormaApplyModePatch,
			resource_update.FormaCommandSourceSynchronize,
			existingTargets,
			data.datastore,
			nil, // No resource filters needed for synchronization
		)
		if err != nil {
			proc.Log().Error("failed to generate resource updates for stack %s: %w", stack.SingleStackLabel(), err)
			return state, data, nil, gen.TerminateReasonPanic
		}

		allResourceUpdates = append(allResourceUpdates, resourceUpdates...)
	}

	// Filter out resources that are currently being updated by non-sync operations
	filteredResourceUpdates := make([]resource_update.ResourceUpdate, 0, len(allResourceUpdates))
	for _, update := range allResourceUpdates {
		resourceURI := string(update.URI())
		if _, excluded := data.excludedResources[resourceURI]; !excluded {
			filteredResourceUpdates = append(filteredResourceUpdates, update)
		} else {
			proc.Log().Debug("Excluding resource from sync (in-progress operation)", "resourceURI", resourceURI)
		}
	}
	allResourceUpdates = filteredResourceUpdates

	if len(allResourceUpdates) == 0 {
		proc.Log().Debug("Synchronizer: no resources found to synchronize")
		if err = scheduleNextSync(data, proc); err != nil {
			return state, data, nil, gen.TerminateReasonPanic
		}
		return StateIdle, data, nil, nil
	}

	var resourcesToSynchronize []pkgmodel.Resource
	for _, update := range allResourceUpdates {
		resourcesToSynchronize = append(resourcesToSynchronize, update.Resource)
	}
	syncCommand := forma_command.NewFormaCommand(
		&pkgmodel.Forma{
			Targets:   []pkgmodel.Target{},
			Resources: resourcesToSynchronize,
		},
		&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		pkgmodel.CommandSync,
		allResourceUpdates,
		nil, // No target updates on sync
		"",
	)
	data.commandID = syncCommand.ID

	_, err = proc.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: proc.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *syncCommand})
	if err != nil {
		proc.Log().Error("failed to store batch forma command: %w", err)
		return state, data, nil, gen.TerminateReasonPanic
	}

	// Sync commands (READs) will never contain cycles so we can safely ignore the error here.
	cs, _ := changeset.NewChangesetFromResourceUpdates(allResourceUpdates, syncCommand.ID, pkgmodel.CommandSync)

	proc.Log().Debug("Ensuring ChangesetExecutor for sync command", "commandID", syncCommand.ID)
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: proc.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: syncCommand.ID},
	)
	if err != nil {
		proc.Log().Error("failed to ensure ChangesetExecutor for sync command %s: %w", syncCommand.ID, err)
		return state, data, nil, gen.TerminateReasonPanic
	}

	proc.Log().Debug("Starting ChangesetExecutor for sync command", "commandID", syncCommand.ID)
	err = proc.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(syncCommand.ID), Node: proc.Node().Name()},
		changeset.Start{Changeset: cs},
	)
	if err != nil {
		proc.Log().Error("failed to start ChangesetExecutor for sync command %s: %w", syncCommand.ID, err)
		return state, data, nil, gen.TerminateReasonPanic
	}

	return StateSynchronizing, data, nil, nil
}

func changesetCompleted(state gen.Atom, data SynchronizerData, message changeset.ChangesetCompleted, proc gen.Process) (gen.Atom, SynchronizerData, []statemachine.Action, error) {
	if message.CommandID != data.commandID {
		proc.Log().Error("Synchronizer received ChangesetCompleted for unknown command ID", "commandID", message.CommandID)
		return state, data, nil, nil
	}

	return StateIdle, data, nil, nil
}

func registerInProgressResource(state gen.Atom, data SynchronizerData, message messages.RegisterInProgressResource, proc gen.Process) (gen.Atom, SynchronizerData, []statemachine.Action, error) {
	data.excludedResources[message.ResourceURI] = struct{}{}
	proc.Log().Debug("Resource registered as in-progress, excluded from sync", "resourceURI", message.ResourceURI)
	return state, data, nil, nil
}

func unregisterInProgressResource(state gen.Atom, data SynchronizerData, message messages.UnregisterInProgressResource, proc gen.Process) (gen.Atom, SynchronizerData, []statemachine.Action, error) {
	delete(data.excludedResources, message.ResourceURI)
	proc.Log().Debug("Resource unregistered from in-progress, can be synced", "resourceURI", message.ResourceURI)
	return state, data, nil, nil
}

func scheduleNextSync(data SynchronizerData, proc gen.Process) error {
	if data.isScheduledSync {
		_, err := proc.SendAfter(proc.PID(), Synchronize{}, data.cfg.Interval)
		if err != nil {
			return fmt.Errorf("failed to schedule next resource synchronization %w", err)
		}
	}

	return nil
}
