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
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// StackExpirer is the actor responsible for automatically destroying stacks
// that have exceeded their TTL. It runs on a scheduled interval and checks
// for expired stacks in the database.

func NewStackExpirer() gen.ProcessBehavior {
	return &StackExpirer{}
}

const (
	StackExpirerStateIdle = gen.Atom("idle")

	// DefaultStackExpirerInterval is how often the expirer checks for expired stacks.
	DefaultStackExpirerInterval = 5 * time.Second
)

type StackExpirer struct {
	statemachine.StateMachine[StackExpirerData]
}

type StackExpirerData struct {
	datastore datastore.Datastore
	interval  time.Duration
}

// Messages processed by StackExpirer

type CheckExpiredStacks struct{}

func (s *StackExpirer) Init(args ...any) (statemachine.StateMachineSpec[StackExpirerData], error) {
	ds, ok := s.Env("Datastore")
	if !ok {
		s.Log().Error("Missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[StackExpirerData]{}, fmt.Errorf("stack_expirer: missing 'Datastore' environment variable")
	}

	data := StackExpirerData{
		datastore: ds.(datastore.Datastore),
		interval:  DefaultStackExpirerInterval,
	}

	spec := statemachine.NewStateMachineSpec(StackExpirerStateIdle,
		statemachine.WithData(data),
		statemachine.WithStateMessageHandler(StackExpirerStateIdle, checkExpiredStacks),
	)
	if _, err := s.SendAfter(s.PID(), CheckExpiredStacks{}, DefaultStackExpirerInterval); err != nil {
		return statemachine.StateMachineSpec[StackExpirerData]{}, fmt.Errorf("failed to send initial check message: %s", err)
	}
	s.Log().Info("Stack expirer ready", "interval", DefaultStackExpirerInterval)

	return spec, nil
}

func checkExpiredStacks(from gen.PID, state gen.Atom, data StackExpirerData, message CheckExpiredStacks, proc gen.Process) (gen.Atom, StackExpirerData, []statemachine.Action, error) {
	// Query for expired stacks
	expiredStacks, err := data.datastore.ListExpiredStacks()
	if err != nil {
		proc.Log().Error("Failed to query expired stacks", "error", err)
		_ = scheduleNextExpirationCheck(data, proc)
		return StackExpirerStateIdle, data, nil, nil
	}

	if len(expiredStacks) == 0 {
		_ = scheduleNextExpirationCheck(data, proc)
		return StackExpirerStateIdle, data, nil, nil
	}

	// For each expired stack, trigger a destroy command directly
	for _, stack := range expiredStacks {
		proc.Log().Info("Expiring stack", "label", stack.Label)

		if err := destroyExpiredStack(data, stack.Label, proc); err != nil {
			proc.Log().Error("Failed to destroy expired stack", "label", stack.Label, "error", err)
			// Continue with other stacks even if one fails
		}
	}

	// Schedule the next check
	if err := scheduleNextExpirationCheck(data, proc); err != nil {
		proc.Log().Error("Failed to schedule next expiration check", "error", err)
	}

	return StackExpirerStateIdle, data, nil, nil
}

// destroyExpiredStack directly creates and executes a destroy command for an expired stack,
// following the same pattern as the Synchronizer actor.
func destroyExpiredStack(data StackExpirerData, stackLabel string, proc gen.Process) error {
	// Load all resources in the stack
	resources, err := data.datastore.LoadResourcesByStack(stackLabel)
	if err != nil {
		return fmt.Errorf("failed to load stack %s: %w", stackLabel, err)
	}
	if len(resources) == 0 {
		return nil
	}

	// Build a Forma object for resource update generation
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: stackLabel}},
		Resources: make([]pkgmodel.Resource, len(resources)),
	}
	for i, r := range resources {
		forma.Resources[i] = *r
	}

	// Load existing targets for resource update generation
	existingTargets, err := data.datastore.LoadAllTargets()
	if err != nil {
		return fmt.Errorf("failed to load targets: %w", err)
	}

	// Generate resource updates for destruction
	resourceUpdates, err := resource_update.GenerateResourceUpdates(
		forma,
		pkgmodel.CommandDestroy,
		pkgmodel.FormaApplyModeReconcile,
		resource_update.FormaCommandSourceUser, // Treat expiration as user-initiated
		existingTargets,
		data.datastore,
	)
	if err != nil {
		return fmt.Errorf("failed to generate resource updates: %w", err)
	}

	if len(resourceUpdates) == 0 {
		return nil
	}

	// Create the destroy command
	destroyCommand := forma_command.NewFormaCommand(
		forma,
		&config.FormaCommandConfig{
			Mode:  pkgmodel.FormaApplyModeReconcile,
			Force: true,
		},
		pkgmodel.CommandDestroy,
		resourceUpdates,
		nil, // No target updates on destroy
		nil, // No stack updates on destroy
		"stack-expirer",
	)

	// Store the forma command
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: proc.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *destroyCommand},
	)
	if err != nil {
		return fmt.Errorf("failed to store destroy command: %w", err)
	}

	// Create changeset
	cs, err := changeset.NewChangesetFromResourceUpdates(resourceUpdates, destroyCommand.ID, pkgmodel.CommandDestroy)
	if err != nil {
		return fmt.Errorf("failed to create changeset: %w", err)
	}

	// Ensure ChangesetExecutor exists
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: proc.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: destroyCommand.ID},
	)
	if err != nil {
		return fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution
	err = proc.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(destroyCommand.ID), Node: proc.Node().Name()},
		changeset.Start{Changeset: cs, NotifyOnComplete: false},
	)
	if err != nil {
		return fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return nil
}

func scheduleNextExpirationCheck(data StackExpirerData, proc gen.Process) error {
	interval := data.interval

	if _, err := proc.SendAfter(proc.PID(), CheckExpiredStacks{}, interval); err != nil {
		proc.Log().Error("Failed to schedule next expiration check", "error", err)
		return err
	}
	return nil
}
