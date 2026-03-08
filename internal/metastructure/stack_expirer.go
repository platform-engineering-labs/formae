// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// StackExpirer is the actor responsible for automatically destroying stacks
// that have exceeded their TTL policy. It runs on a scheduled interval and
// checks for expired stacks in the database.

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

	if cfg, ok := s.Env("StackExpirerConfig"); ok {
		expirerCfg := cfg.(pkgmodel.StackExpirerConfig)
		if expirerCfg.Interval > 0 {
			s.interval = expirerCfg.Interval
		}
	}

	if _, err := s.SendAfter(s.PID(), CheckExpiredStacks{}, s.interval); err != nil {
		return fmt.Errorf("failed to send initial check message: %s", err)
	}
	s.Log().Info("Stack expirer ready, interval=%s", s.interval)

	return nil
}

func (s *StackExpirer) HandleMessage(from gen.PID, message any) error {
	switch message.(type) {
	case CheckExpiredStacks:
		s.checkExpiredStacks()
	default:
		s.Log().Warning("Received unknown message type: %T", message)
	}
	return nil
}

func (s *StackExpirer) checkExpiredStacks() {
	// Query for expired stacks
	expiredStacks, err := s.datastore.GetExpiredStacks()
	if err != nil {
		s.Log().Error("Failed to query expired stacks: %v", err)
		s.scheduleNextExpirationCheck()
		return
	}

	if len(expiredStacks) == 0 {
		s.scheduleNextExpirationCheck()
		return
	}

	// For each expired stack, trigger a destroy command
	for _, stackInfo := range expiredStacks {
		s.Log().Info("Expiring stack label=%s onDependents=%s", stackInfo.StackLabel, stackInfo.OnDependents)

		if err := s.destroyExpiredStack(stackInfo); err != nil {
			s.Log().Error("Failed to destroy expired stack label=%s: %v", stackInfo.StackLabel, err)
			// Continue with other stacks even if one fails
		}
	}

	// Schedule the next check
	s.scheduleNextExpirationCheck()
}

// destroyExpiredStack creates and executes a destroy command for an expired stack.
func (s *StackExpirer) destroyExpiredStack(stackInfo datastore.ExpiredStackInfo) error {
	result, err := prepareDestroyExpiredStack(s.datastore, stackInfo, "stack-expirer", "stack-expirer-cleanup")
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}

	// Store the forma command
	_, err = s.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: s.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *result.command},
	)
	if err != nil {
		return fmt.Errorf("failed to store destroy command: %w", err)
	}

	// Ensure ChangesetExecutor exists
	_, err = s.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: s.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: result.command.ID},
	)
	if err != nil {
		return fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution
	err = s.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(result.command.ID), Node: s.Node().Name()},
		changeset.Start{Changeset: result.changeset},
	)
	if err != nil {
		return fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return nil
}

type destroyExpiredResult struct {
	command   *forma_command.FormaCommand
	changeset changeset.Changeset
}

// prepareDestroyExpiredStack builds a destroy FormaCommand and Changeset for an expired stack.
// Returns nil (with no error) when the stack has no resources (cleaned up directly),
// when expiration is aborted due to external dependents, or when no updates are needed.
// The caller is responsible for persisting the command and starting the changeset execution.
func prepareDestroyExpiredStack(ds datastore.Datastore, stackInfo datastore.ExpiredStackInfo, clientID string, cleanupClientID string) (*destroyExpiredResult, error) {
	// Load all resources in the stack
	resources, err := ds.LoadResourcesByStack(stackInfo.StackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to load stack %s: %w", stackInfo.StackLabel, err)
	}
	if len(resources) == 0 {
		// Stack is expired but has no resources - delete the empty stack directly
		_, err := ds.DeleteStack(stackInfo.StackLabel, cleanupClientID)
		if err != nil {
			return nil, fmt.Errorf("failed to delete empty expired stack %s: %w", stackInfo.StackLabel, err)
		}
		return nil, nil
	}

	// Check for dependents if onDependents is "abort"
	if stackInfo.OnDependents == "abort" {
		// Collect all KSUIDs for a single batched query
		ksuids := make([]string, len(resources))
		for i, res := range resources {
			ksuids[i] = res.Ksuid
		}

		dependentsMap, err := ds.FindResourcesDependingOnMany(ksuids)
		if err != nil {
			return nil, fmt.Errorf("failed to check dependents for stack %s: %w", stackInfo.StackLabel, err)
		}

		// Filter out dependents that are in the same stack (those will be deleted together)
		for _, dependents := range dependentsMap {
			for _, dep := range dependents {
				if dep.Stack != stackInfo.StackLabel {
					return nil, nil // Skip this stack, try again next interval
				}
			}
		}
	}

	// Build a Forma object for resource update generation
	forma := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: stackInfo.StackLabel}},
		Resources: make([]pkgmodel.Resource, len(resources)),
	}
	for i, r := range resources {
		forma.Resources[i] = *r
	}

	// Load existing targets for resource update generation
	existingTargets, err := ds.LoadAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to load targets: %w", err)
	}

	// Generate resource updates for destruction
	resourceUpdates, err := resource_update.GenerateResourceUpdates(
		forma,
		pkgmodel.CommandDestroy,
		pkgmodel.FormaApplyModeReconcile,
		resource_update.FormaCommandSourceUser, // Treat expiration as user-initiated
		existingTargets,
		s.datastore,
		nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate resource updates: %w", err)
	}

	if len(resourceUpdates) == 0 {
		return nil, nil
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
		nil,                          // No target updates on destroy
		[]stack_update.StackUpdate{}, // No stack updates on destroy
		nil,                          // No policy updates on destroy
		clientID,
	)

	// Create changeset
	cs, err := changeset.NewChangeset(resourceUpdates, nil, destroyCommand.ID, pkgmodel.CommandDestroy)
	if err != nil {
		return nil, fmt.Errorf("failed to create changeset: %w", err)
	}

	return &destroyExpiredResult{
		command:   destroyCommand,
		changeset: cs,
	}, nil
}

func (s *StackExpirer) scheduleNextExpirationCheck() {
	if _, err := s.SendAfter(s.PID(), CheckExpiredStacks{}, s.interval); err != nil {
		s.Log().Error("Failed to schedule next expiration check: %v", err)
	}
}
