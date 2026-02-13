// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"
	"time"

	"ergo.services/ergo/act"
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
// that have auto-reconcile policies attached. It maintains a state machine to track
// active reconciliations and uses SendAfter to schedule per-stack reconcile intervals.

type autoReconcilerState int

const (
	autoReconcilerStateIdle autoReconcilerState = iota
	autoReconcilerStateReconciling
)

type AutoReconciler struct {
	act.Actor

	datastore     datastore.Datastore
	pluginManager *plugin.Manager
	state         autoReconcilerState

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

func (ar *AutoReconciler) Init(args ...any) error {
	ds, ok := ar.Env("Datastore")
	if !ok {
		ar.Log().Error("Missing 'Datastore' environment variable")
		return fmt.Errorf("auto_reconciler: missing 'Datastore' environment variable")
	}
	ar.datastore = ds.(datastore.Datastore)

	pm, ok := ar.Env("PluginManager")
	if !ok {
		ar.Log().Error("Missing 'PluginManager' environment variable")
		return fmt.Errorf("auto_reconciler: missing 'PluginManager' environment variable")
	}
	ar.pluginManager = pm.(*plugin.Manager)

	ar.state = autoReconcilerStateIdle
	ar.activeReconciles = make(map[string]string)
	ar.scheduled = make(map[string]bool)

	// Schedule initial reconciles for all stacks with auto-reconcile policies
	if err := ar.scheduleInitialReconciles(); err != nil {
		ar.Log().Error("Failed to schedule initial reconciles: %v", err)
		// Don't fail init, just log the error
	}

	ar.Log().Info("Auto reconciler ready")
	return nil
}

func (ar *AutoReconciler) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case ReconcileStack:
		ar.handleReconcileStack(msg)
	case ReconcileComplete:
		ar.handleReconcileComplete(msg)
	case messages.PolicyAttached:
		ar.handlePolicyAttached(msg)
	case messages.PolicyRemoved:
		ar.handlePolicyRemoved(msg)
	default:
		ar.Log().Warning("Received unknown message type: %T", message)
	}
	return nil
}

func (ar *AutoReconciler) scheduleInitialReconciles() error {
	policies, err := ar.datastore.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		return fmt.Errorf("failed to get stacks with auto-reconcile policy: %w", err)
	}

	// Minimum delay for SendAfter - Ergo's SendAfter may not fire with delay=0
	const minDelay = 1 * time.Second

	now := time.Now()
	for _, p := range policies {
		nextDue := p.LastReconcileAt.Add(time.Duration(p.IntervalSeconds) * time.Second)
		delay := nextDue.Sub(now)
		if delay < minDelay {
			delay = minDelay // Use minimum delay to ensure timer fires
		}

		ar.scheduled[p.StackLabel] = true
		if _, err := ar.SendAfter(ar.PID(), ReconcileStack{StackLabel: p.StackLabel}, delay); err != nil {
			ar.Log().Error("Failed to schedule reconcile stack=%s: %v", p.StackLabel, err)
		} else {
			ar.Log().Info("Scheduled reconcile stack=%s delay=%s", p.StackLabel, delay)
		}
	}

	return nil
}

func (ar *AutoReconciler) handleReconcileStack(msg ReconcileStack) {
	// Check if policy was removed (stale message)
	if !ar.scheduled[msg.StackLabel] {
		ar.Log().Debug("Ignoring reconcile for unscheduled stack (policy may have been removed) stack=%s", msg.StackLabel)
		return
	}

	// Check if already reconciling this stack (shouldn't happen, but be safe)
	if _, exists := ar.activeReconciles[msg.StackLabel]; exists {
		ar.Log().Warning("Stack already reconciling, skipping stack=%s", msg.StackLabel)
		return
	}

	ar.Log().Info("Starting auto-reconcile stack=%s", msg.StackLabel)

	commandID, err := ar.startReconcile(msg.StackLabel)
	if err != nil {
		ar.Log().Debug("Failed to start reconcile stack=%s: %v", msg.StackLabel, err)
		// Schedule next reconcile anyway
		ar.scheduleNextReconcile(msg.StackLabel)
		return
	}

	// Empty commandID means nothing to reconcile (no drift)
	if commandID == "" {
		ar.scheduleNextReconcile(msg.StackLabel)
		return
	}

	ar.activeReconciles[msg.StackLabel] = commandID
	ar.state = autoReconcilerStateReconciling
}

func (ar *AutoReconciler) handleReconcileComplete(msg ReconcileComplete) {
	ar.Log().Info("Reconcile complete stack=%s success=%v", msg.StackLabel, msg.Success)

	delete(ar.activeReconciles, msg.StackLabel)

	// Schedule next reconcile if policy still attached
	if ar.scheduled[msg.StackLabel] {
		ar.scheduleNextReconcile(msg.StackLabel)
	}

	// Transition to idle if no active reconciles
	if len(ar.activeReconciles) == 0 {
		ar.state = autoReconcilerStateIdle
	}
}

func (ar *AutoReconciler) handlePolicyAttached(msg messages.PolicyAttached) {
	ar.Log().Info("Auto-reconcile policy attached stack=%s interval=%ds", msg.StackLabel, msg.IntervalSeconds)

	// If already scheduled, don't reschedule (could happen if policy is updated)
	if ar.scheduled[msg.StackLabel] {
		ar.Log().Debug("Stack already scheduled for reconcile, skipping stack=%s", msg.StackLabel)
		return
	}

	// Schedule the first reconcile after the interval
	ar.scheduled[msg.StackLabel] = true
	interval := time.Duration(msg.IntervalSeconds) * time.Second
	if _, err := ar.SendAfter(ar.PID(), ReconcileStack{StackLabel: msg.StackLabel}, interval); err != nil {
		ar.Log().Error("Failed to schedule reconcile stack=%s: %v", msg.StackLabel, err)
	} else {
		ar.Log().Info("Scheduled reconcile stack=%s delay=%s", msg.StackLabel, interval)
	}
}

func (ar *AutoReconciler) handlePolicyRemoved(msg messages.PolicyRemoved) {
	ar.Log().Info("Auto-reconcile policy removed stack=%s", msg.StackLabel)
	delete(ar.scheduled, msg.StackLabel)
	// Note: if a reconcile is in progress, it will complete but won't reschedule
}

func (ar *AutoReconciler) scheduleNextReconcile(stackLabel string) {
	// Get current interval from database (may have changed)
	policies, err := ar.datastore.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		ar.Log().Error("Failed to get policy for scheduling stack=%s: %v", stackLabel, err)
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
		delete(ar.scheduled, stackLabel)
		return
	}

	if _, err := ar.SendAfter(ar.PID(), ReconcileStack{StackLabel: stackLabel}, interval); err != nil {
		ar.Log().Error("Failed to schedule next reconcile stack=%s: %v", stackLabel, err)
	} else {
		ar.Log().Debug("Scheduled next reconcile stack=%s interval=%s", stackLabel, interval)
	}
}

func (ar *AutoReconciler) startReconcile(stackLabel string) (string, error) {
	// Get resources at last reconcile as full Resource objects
	snapshots, err := ar.datastore.GetResourcesAtLastReconcile(stackLabel)
	if err != nil {
		return "", fmt.Errorf("failed to get resources at last reconcile: %w", err)
	}

	if len(snapshots) == 0 {
		ar.Log().Debug("No resources to reconcile stack=%s", stackLabel)
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
	existingTargets, err := ar.datastore.LoadAllTargets()
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
		ar.datastore,
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate resource updates: %w", err)
	}

	ar.Log().Debug("Generated %d resource updates for stack=%s", len(resourceUpdates), stackLabel)

	if len(resourceUpdates) == 0 {
		ar.Log().Debug("No drift detected, nothing to reconcile stack=%s", stackLabel)
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
	_, err = ar.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: ar.Node().Name()},
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
	_, err = ar.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: ar.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: reconcileCommand.ID},
	)
	if err != nil {
		return "", fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution
	err = ar.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(reconcileCommand.ID), Node: ar.Node().Name()},
		changeset.Start{Changeset: cs},
	)
	if err != nil {
		return "", fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return reconcileCommand.ID, nil
}
