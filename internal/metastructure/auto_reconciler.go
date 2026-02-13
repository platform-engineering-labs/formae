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
	"github.com/platform-engineering-labs/formae/pkg/plugin"
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
	datastore     datastore.Datastore
	pluginManager *plugin.Manager

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

// ReconcileComplete is sent when a stack reconciliation completes
type ReconcileComplete struct {
	StackLabel string
	CommandID  string
	Success    bool
}

func (ar *AutoReconciler) Init(args ...any) (statemachine.StateMachineSpec[AutoReconcilerData], error) {
	ds, ok := ar.Env("Datastore")
	if !ok {
		ar.Log().Error("Missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[AutoReconcilerData]{}, fmt.Errorf("auto_reconciler: missing 'Datastore' environment variable")
	}

	pm, ok := ar.Env("PluginManager")
	if !ok {
		ar.Log().Error("Missing 'PluginManager' environment variable")
		return statemachine.StateMachineSpec[AutoReconcilerData]{}, fmt.Errorf("auto_reconciler: missing 'PluginManager' environment variable")
	}

	data := AutoReconcilerData{
		datastore:        ds.(datastore.Datastore),
		pluginManager:    pm.(*plugin.Manager),
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
		statemachine.WithStateMessageHandler(AutoReconcilerStateIdle, handlePolicyAttached),
		statemachine.WithStateMessageHandler(AutoReconcilerStateIdle, handlePolicyRemoved),

		// Reconciling state handlers
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handleReconcileStack),
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handleReconcileComplete),
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handlePolicyAttached),
		statemachine.WithStateMessageHandler(AutoReconcilerStateReconciling, handlePolicyRemoved),
	), nil
}

func scheduleInitialReconciles(proc gen.Process, data *AutoReconcilerData) error {
	policies, err := data.datastore.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		return fmt.Errorf("failed to get stacks with auto-reconcile policy: %w", err)
	}

	now := time.Now()
	for _, p := range policies {
		nextDue := p.LastReconcileAt.Add(time.Duration(p.IntervalSeconds) * time.Second)
		delay := nextDue.Sub(now)
		if delay < 0 {
			delay = 0
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

func handleReconcileComplete(from gen.PID, state gen.Atom, data AutoReconcilerData, msg ReconcileComplete, proc gen.Process) (gen.Atom, AutoReconcilerData, []statemachine.Action, error) {
	proc.Log().Info("Reconcile complete stack=%s success=%v", msg.StackLabel, msg.Success)

	delete(data.activeReconciles, msg.StackLabel)

	// Schedule next reconcile if policy still attached
	if data.scheduled[msg.StackLabel] {
		scheduleNextReconcile(proc, &data, msg.StackLabel)
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

func startReconcile(proc gen.Process, data *AutoReconcilerData, stackLabel string) (string, error) {
	// Get resources at last reconcile as full Resource objects
	snapshots, err := data.datastore.GetResourcesAtLastReconcile(stackLabel)
	if err != nil {
		return "", fmt.Errorf("failed to get resources at last reconcile: %w", err)
	}

	if len(snapshots) == 0 {
		proc.Log().Debug("No resources to reconcile stack=%s", stackLabel)
		return "", fmt.Errorf("no resources to reconcile")
	}

	// Convert snapshots to Resource objects
	resources := make([]*pkgmodel.Resource, 0, len(snapshots))
	for _, snapshot := range snapshots {
		res := &pkgmodel.Resource{
			Ksuid:      snapshot.KSUID,
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
	existingTargets, err := data.datastore.LoadAllTargets()
	if err != nil {
		return "", fmt.Errorf("failed to load targets: %w", err)
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
		data.datastore,
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate resource updates: %w", err)
	}

	proc.Log().Debug("Generated %d resource updates for stack=%s", len(resourceUpdates), stackLabel)

	if len(resourceUpdates) == 0 {
		proc.Log().Debug("No drift detected, nothing to reconcile stack=%s", stackLabel)
		return "", nil // Not an error - just nothing to do
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
		"auto-reconciler",
	)

	// Store the forma command
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: proc.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *reconcileCommand},
	)
	if err != nil {
		return "", fmt.Errorf("failed to store reconcile command: %w", err)
	}

	// Create changeset
	cs, err := changeset.NewChangesetFromResourceUpdates(resourceUpdates, reconcileCommand.ID, pkgmodel.CommandApply)
	if err != nil {
		return "", fmt.Errorf("failed to create changeset: %w", err)
	}

	// Ensure ChangesetExecutor exists
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: proc.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: reconcileCommand.ID},
	)
	if err != nil {
		return "", fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution
	err = proc.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(reconcileCommand.ID), Node: proc.Node().Name()},
		changeset.Start{Changeset: cs},
	)
	if err != nil {
		return "", fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return reconcileCommand.ID, nil
}
