// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// AutoReconciler is the actor responsible for automatically reconciling stacks
// that have auto-reconcile policies attached. It uses the ergo statemachine to track
// active reconciliations and uses SendAfter to schedule per-stack reconcile intervals.
//
// State transitions:
//
//	+-------------------+                     +-------------------+
//	|    StateIdle      | --(reconcile)--->   | StateReconciling  |
//	+-------------------+                     +-------------------+
//	        ^                                         |
//	        |         (last reconcile completes)      |
//	        +-----------------------------------------+

// State constants for AutoReconciler (prefixed to avoid collision with Synchronizer states)
const (
	AutoReconcilerStateIdle        = gen.Atom("auto_reconciler_idle")
	AutoReconcilerStateReconciling = gen.Atom("auto_reconciler_reconciling")
)

// AutoReconciler embeds the ergo statemachine for state management
type AutoReconciler struct {
	statemachine.StateMachine[AutoReconcilerData]
}

// AutoReconcilerData holds the data associated with the statemachine
type AutoReconcilerData struct {
	datastore datastore.Datastore

	// activeReconciles tracks stacks currently being reconciled: stackLabel -> commandID
	activeReconciles map[string]string
	// scheduled tracks stacks with pending SendAfter messages
	scheduled map[string]bool
}

func NewAutoReconciler() gen.ProcessBehavior {
	return &AutoReconciler{}
}

// Messages processed by AutoReconciler

// ReconcileStack triggers reconciliation for a specific stack
type ReconcileStack struct {
	StackLabel string
}

func (ar *AutoReconciler) Init(args ...any) (statemachine.StateMachineSpec[AutoReconcilerData], error) {
	ds, ok := ar.Env("Datastore")
	if !ok {
		ar.Log().Error("Missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[AutoReconcilerData]{}, fmt.Errorf("auto_reconciler: missing 'Datastore' environment variable")
	}

	data := AutoReconcilerData{
		datastore:        ds.(datastore.Datastore),
		activeReconciles: make(map[string]string),
		scheduled:        make(map[string]bool),
	}

	// Schedule initial reconciles for all stacks with auto-reconcile policies
	if err := scheduleInitialReconciles(ar, &data); err != nil {
		ar.Log().Error("Failed to schedule initial reconciles: %v", err)
		// Don't fail init, just log the error
	}

	ar.Log().Info("Auto reconciler ready")

	return statemachine.NewStateMachineSpec(AutoReconcilerStateIdle,
		statemachine.WithData(data),

		// Idle state handlers
		statemachine.WithStateMessageHandler(AutoReconcilerStateIdle, handleReconcileStack),
		statemachine.WithStateMessageHandler(AutoReconcilerStateIdle, handleChangesetCompleted),
		statemachine.WithStateMessageHandler(AutoReconcilerStateIdle, handlePolicyAttached),
		statemachine.WithStateMessageHandler(AutoReconcilerStateIdle, handlePolicyRemoved),

		// Reconciling state handlers
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handleReconcileStack),
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handleChangesetCompleted),
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handlePolicyAttached),
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handlePolicyRemoved),
	), nil
}

func scheduleInitialReconciles(proc gen.Process, data *AutoReconcilerData) error {
	policies, err := data.datastore.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		return fmt.Errorf("failed to get stacks with auto-reconcile policy: %w", err)
	}

	proc.Log().Info("Found %d stacks with auto-reconcile policies", len(policies))

	now := time.Now()
	for _, p := range policies {
		nextDue := p.LastReconcileAt.Add(time.Duration(p.IntervalSeconds) * time.Second)
		delay := nextDue.Sub(now)
		if delay < time.Second {
			delay = time.Second // Minimum 1s delay - SendAfter(0) doesn't work reliably
		}

		data.scheduled[p.StackLabel] = true
		if _, err := proc.SendAfter(proc.PID(), ReconcileStack{StackLabel: p.StackLabel}, delay); err != nil {
			proc.Log().Error("Failed to schedule reconcile stack=%s: %v", p.StackLabel, err)
		} else {
			proc.Log().Info("Scheduled reconcile stack=%s delay=%s", p.StackLabel, delay)
		}
	}

	return nil
}

func handleReconcileStack(from gen.PID, state gen.Atom, data AutoReconcilerData, msg ReconcileStack, proc gen.Process) (gen.Atom, AutoReconcilerData, []statemachine.Action, error) {
	// Check if policy was removed (stale message)
	if !data.scheduled[msg.StackLabel] {
		proc.Log().Debug("Ignoring reconcile for unscheduled stack (policy may have been removed) stack=%s", msg.StackLabel)
		return state, data, nil, nil
	}

	// Check if already reconciling this stack (shouldn't happen, but be safe)
	if _, exists := data.activeReconciles[msg.StackLabel]; exists {
		proc.Log().Warning("Stack already reconciling, skipping stack=%s", msg.StackLabel)
		return state, data, nil, nil
	}

	proc.Log().Info("Starting auto-reconcile stack=%s", msg.StackLabel)

	commandID, err := startReconcile(proc, &data, msg.StackLabel)
	if err != nil {
		proc.Log().Debug("Failed to start reconcile stack=%s: %v", msg.StackLabel, err)
		// Schedule next reconcile anyway
		scheduleNextReconcile(proc, &data, msg.StackLabel)
		return state, data, nil, nil
	}

	// Empty commandID means nothing to reconcile (no drift)
	if commandID == "" {
		scheduleNextReconcile(proc, &data, msg.StackLabel)
		return state, data, nil, nil
	}

	data.activeReconciles[msg.StackLabel] = commandID

	// Transition to reconciling state if we weren't already
	return AutoReconcilerStateReconciling, data, nil, nil
}

func handleChangesetCompleted(from gen.PID, state gen.Atom, data AutoReconcilerData, msg changeset.ChangesetCompleted, proc gen.Process) (gen.Atom, AutoReconcilerData, []statemachine.Action, error) {
	// Find the stack label for this command ID (reverse lookup)
	var stackLabel string
	for label, cmdID := range data.activeReconciles {
		if cmdID == msg.CommandID {
			stackLabel = label
			break
		}
	}

	if stackLabel == "" {
		proc.Log().Debug("Received ChangesetCompleted for unknown command (not from auto-reconcile) commandID=%s", msg.CommandID)
		return state, data, nil, nil
	}

	success := msg.State == changeset.ChangeSetStateFinishedSuccessfully
	proc.Log().Info("Auto-reconcile complete stack=%s success=%v", stackLabel, success)

	delete(data.activeReconciles, stackLabel)

	// Schedule next reconcile if policy still attached
	if data.scheduled[stackLabel] {
		scheduleNextReconcile(proc, &data, stackLabel)
	}

	// Transition to idle if no active reconciles
	if len(data.activeReconciles) == 0 {
		return AutoReconcilerStateIdle, data, nil, nil
	}

	return state, data, nil, nil
}

func handlePolicyAttached(from gen.PID, state gen.Atom, data AutoReconcilerData, msg messages.PolicyAttached, proc gen.Process) (gen.Atom, AutoReconcilerData, []statemachine.Action, error) {
	proc.Log().Info("Auto-reconcile policy attached stack=%s interval=%ds", msg.StackLabel, msg.IntervalSeconds)

	// If already scheduled, don't reschedule (could happen if policy is updated)
	if data.scheduled[msg.StackLabel] {
		proc.Log().Debug("Stack already scheduled for reconcile, skipping stack=%s", msg.StackLabel)
		return state, data, nil, nil
	}

	// Schedule the first reconcile after the interval
	data.scheduled[msg.StackLabel] = true
	interval := time.Duration(msg.IntervalSeconds) * time.Second
	if _, err := proc.SendAfter(proc.PID(), ReconcileStack{StackLabel: msg.StackLabel}, interval); err != nil {
		proc.Log().Error("Failed to schedule reconcile stack=%s: %v", msg.StackLabel, err)
	} else {
		proc.Log().Info("Scheduled reconcile stack=%s delay=%s", msg.StackLabel, interval)
	}

	return state, data, nil, nil
}

func handlePolicyRemoved(from gen.PID, state gen.Atom, data AutoReconcilerData, msg messages.PolicyRemoved, proc gen.Process) (gen.Atom, AutoReconcilerData, []statemachine.Action, error) {
	proc.Log().Info("Auto-reconcile policy removed stack=%s", msg.StackLabel)
	delete(data.scheduled, msg.StackLabel)
	// Note: if a reconcile is in progress, it will complete but won't reschedule
	return state, data, nil, nil
}

func scheduleNextReconcile(proc gen.Process, data *AutoReconcilerData, stackLabel string) {
	// Get current interval from database (may have changed)
	policies, err := data.datastore.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		proc.Log().Error("Failed to get policy for scheduling stack=%s: %v", stackLabel, err)
		return
	}

	var interval time.Duration
	for _, p := range policies {
		if p.StackLabel == stackLabel {
			interval = time.Duration(p.IntervalSeconds) * time.Second
			break
		}
	}

	if interval == 0 {
		// Policy no longer exists
		delete(data.scheduled, stackLabel)
		return
	}

	if _, err := proc.SendAfter(proc.PID(), ReconcileStack{StackLabel: stackLabel}, interval); err != nil {
		proc.Log().Error("Failed to schedule next reconcile stack=%s: %v", stackLabel, err)
	} else {
		proc.Log().Debug("Scheduled next reconcile stack=%s interval=%s", stackLabel, interval)
	}
}

// reconcileResult holds the prepared reconcile command and changeset, ready for persistence and execution.
type reconcileResult struct {
	command   *forma_command.FormaCommand
	changeset changeset.Changeset
}

// prepareReconcile builds a reconcile FormaCommand and Changeset from the stack's last-reconcile snapshot.
// It returns nil (with no error) when no drift is detected. The caller is responsible for persisting
// the command and starting the changeset execution.
func prepareReconcile(ds datastore.Datastore, stackLabel string, clientID string) (*reconcileResult, error) {
	// Get resources at last reconcile as full Resource objects
	snapshots, err := ds.GetResourcesAtLastReconcile(stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to get resources at last reconcile: %w", err)
	}

	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no resources to reconcile")
	}

	// Convert snapshots to Resource objects.
	// Snapshot KSUIDs come from the last successful reconcile and may since have
	// been deleted. Clear stale KSUIDs so assignKSUIDs() can look up the current
	// live KSUID (or generate a fresh one). Deleted resources have a tombstone
	// record; their KSUID must never be reused.
	resources := make([]*pkgmodel.Resource, 0, len(snapshots))
	for _, snapshot := range snapshots {
		ksuid := snapshot.KSUID
		if ksuid != "" {
			uri := pkgmodel.NewFormaeURI(ksuid, "")
			existing, err := ds.LoadResource(uri)
			if err != nil || existing == nil {
				// KSUID was deleted — clear it so assignKSUIDs() resolves a fresh one
				ksuid = ""
			}
		}
		res := &pkgmodel.Resource{
			Ksuid:      ksuid,
			Type:       snapshot.Type,
			Label:      snapshot.Label,
			Target:     snapshot.Target,
			Stack:      stackLabel,
			NativeID:   snapshot.NativeID,
			Properties: snapshot.Properties,
			Schema:     snapshot.Schema,
			Managed:    true,
		}
		resources = append(resources, res)
	}

	// Convert to Forma
	forma := pkgmodel.FormaFromResources(resources)

	// Load existing targets for resource update generation
	existingTargets, err := ds.LoadAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to load targets: %w", err)
	}

	// Replace forma targets with actual existing targets
	// FormaFromResources only creates placeholder targets with labels
	existingTargetMap := make(map[string]*pkgmodel.Target)
	for _, t := range existingTargets {
		existingTargetMap[t.Label] = t
	}
	for i, formaTarget := range forma.Targets {
		if existing, ok := existingTargetMap[formaTarget.Label]; ok {
			forma.Targets[i] = *existing
		}
	}

	// Generate resource updates using the standard generator
	resourceUpdates, err := resource_update.GenerateResourceUpdates(
		forma,
		pkgmodel.CommandApply,
		pkgmodel.FormaApplyModeReconcile,
		resource_update.FormaCommandSourcePolicyAutoReconcile,
		existingTargets,
		ds,
		nil, // livePluginSchemas: auto-reconciler uses DB-sourced forma, schemas already stored
		nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate resource updates: %w", err)
	}

	if len(resourceUpdates) == 0 {
		return nil, nil // No drift detected
	}

	// Create the reconcile command
	reconcileCommand := forma_command.NewFormaCommand(
		forma,
		&config.FormaCommandConfig{
			Mode:  pkgmodel.FormaApplyModeReconcile,
			Force: true,
		},
		pkgmodel.CommandApply,
		resourceUpdates,
		nil, // No target updates
		nil, // No stack updates
		nil, // No policy updates
		clientID,
	)

	// Create changeset
	cs, err := changeset.NewChangeset(resourceUpdates, nil, reconcileCommand.ID, pkgmodel.CommandApply)
	if err != nil {
		return nil, fmt.Errorf("failed to create changeset: %w", err)
	}

	return &reconcileResult{
		command:   reconcileCommand,
		changeset: cs,
	}, nil
}

func startReconcile(proc gen.Process, data *AutoReconcilerData, stackLabel string) (string, error) {
	// Skip if stack has active commands (user-initiated operations take priority)
	hasActive, err := data.datastore.StackHasActiveCommands(stackLabel)
	if err != nil {
		return "", fmt.Errorf("failed to check for active commands: %w", err)
	}
	if hasActive {
		proc.Log().Info("Skipping auto-reconcile, stack has active commands stack=%s", stackLabel)
		return "", nil // Not an error - just skip and reschedule
	}

	result, err := prepareReconcile(data.datastore, stackLabel, "auto-reconciler")
	if err != nil {
		return "", err
	}
	if result == nil {
		proc.Log().Debug("No drift detected, nothing to reconcile stack=%s", stackLabel)
		return "", nil
	}

	proc.Log().Debug("Generated resource updates for stack=%s, starting reconcile command=%s", stackLabel, result.command.ID)

	// Store the forma command
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: proc.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *result.command},
	)
	if err != nil {
		return "", fmt.Errorf("failed to store reconcile command: %w", err)
	}

	// Ensure ChangesetExecutor exists
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: proc.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: result.command.ID},
	)
	if err != nil {
		return "", fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution with notification so we know when to schedule next
	err = proc.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(result.command.ID), Node: proc.Node().Name()},
		changeset.Start{Changeset: result.changeset, NotifyOnComplete: true},
	)
	if err != nil {
		return "", fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return result.command.ID, nil
}
