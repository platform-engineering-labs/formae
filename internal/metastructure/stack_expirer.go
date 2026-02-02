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
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// StackExpirer is the actor responsible for automatically destroying stacks
// that have exceeded their TTL. It runs on a scheduled interval and checks
// for expired stacks in the database.

const (
	// DefaultStackExpirerInterval is how often the expirer checks for expired stacks.
	DefaultStackExpirerInterval = 5 * time.Second
)

type StackExpirer struct {
	act.Actor

	datastore datastore.Datastore
	interval  time.Duration
}

func NewStackExpirer() gen.ProcessBehavior {
	return &StackExpirer{}
}

// Messages processed by StackExpirer

type CheckExpiredStacks struct{}

func (s *StackExpirer) Init(args ...any) error {
	ds, ok := s.Env("Datastore")
	if !ok {
		s.Log().Error("Missing 'Datastore' environment variable")
		return fmt.Errorf("stack_expirer: missing 'Datastore' environment variable")
	}

	s.datastore = ds.(datastore.Datastore)
	s.interval = DefaultStackExpirerInterval

	if _, err := s.SendAfter(s.PID(), CheckExpiredStacks{}, DefaultStackExpirerInterval); err != nil {
		return fmt.Errorf("failed to send initial check message: %s", err)
	}
	s.Log().Info("Stack expirer ready", "interval", DefaultStackExpirerInterval)

	return nil
}

func (s *StackExpirer) HandleMessage(from gen.PID, message any) error {
	switch message.(type) {
	case CheckExpiredStacks:
		s.checkExpiredStacks()
	default:
		s.Log().Warning("Received unknown message type", "type", fmt.Sprintf("%T", message))
	}
	return nil
}

func (s *StackExpirer) checkExpiredStacks() {
	// Query for expired stacks
	expiredStacks, err := s.datastore.ListExpiredStacks()
	if err != nil {
		s.Log().Error("Failed to query expired stacks", "error", err)
		s.scheduleNextExpirationCheck()
		return
	}

	if len(expiredStacks) == 0 {
		s.scheduleNextExpirationCheck()
		return
	}

	// For each expired stack, trigger a destroy command directly
	for _, stack := range expiredStacks {
		s.Log().Info("Expiring stack", "label", stack.Label)

		if err := s.destroyExpiredStack(stack.Label); err != nil {
			s.Log().Error("Failed to destroy expired stack", "label", stack.Label, "error", err)
			// Continue with other stacks even if one fails
		}
	}

	// Schedule the next check
	s.scheduleNextExpirationCheck()
}

// destroyExpiredStack directly creates and executes a destroy command for an expired stack,
// following the same pattern as the Synchronizer actor.
func (s *StackExpirer) destroyExpiredStack(stackLabel string) error {
	// Load all resources in the stack
	resources, err := s.datastore.LoadResourcesByStack(stackLabel)
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
	existingTargets, err := s.datastore.LoadAllTargets()
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
		s.datastore,
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
	_, err = s.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: s.Node().Name()},
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
	_, err = s.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: s.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: destroyCommand.ID},
	)
	if err != nil {
		return fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution
	err = s.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(destroyCommand.ID), Node: s.Node().Name()},
		changeset.Start{Changeset: cs, NotifyOnComplete: false},
	)
	if err != nil {
		return fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return nil
}

func (s *StackExpirer) scheduleNextExpirationCheck() {
	if _, err := s.SendAfter(s.PID(), CheckExpiredStacks{}, s.interval); err != nil {
		s.Log().Error("Failed to schedule next expiration check", "error", err)
	}
}
