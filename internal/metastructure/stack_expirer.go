// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
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
		if expirerCfg.Disabled {
			s.Log().Info("Stack expirer disabled via config")
			return nil
		}
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

	// For each expired stack, trigger a destroy command. Expiry destroys real
	// resources, so log the deadline and the anchor it was measured from — an
	// unexpected destroy should be explainable from the log alone.
	for _, stackInfo := range expiredStacks {
		s.Log().Info("Expiring stack label=%s onDependents=%s deadline=%s createdAt=%s",
			stackInfo.StackLabel, stackInfo.OnDependents,
			stackInfo.Deadline(), stackInfo.StackCreatedAt.UTC().Format(time.RFC3339))

		if err := s.destroyExpiredStack(stackInfo); err != nil {
			if errors.Is(err, datastore.ErrStaleAdmission) || errors.Is(err, datastore.ErrAdmissionConflict) ||
				errors.Is(err, datastore.ErrCommandConflict) {
				s.Log().Debug("Expired stack attempt refused label=%s: %v", stackInfo.StackLabel, err)
			} else {
				s.Log().Error("Failed to destroy expired stack label=%s: %v", stackInfo.StackLabel, err)
			}
			// Continue with other stacks even if one fails
		}
	}

	// Schedule the next check
	s.scheduleNextExpirationCheck()
}

// destroyExpiredStack creates and executes a destroy command for an expired stack.
func (s *StackExpirer) destroyExpiredStack(stackInfo datastore.ExpiredStackInfo) error {
	certified, err := certifyExpiredStack(s.datastore, stackInfo)
	if err != nil {
		return err
	}
	if certified == nil {
		return nil
	}
	if certified.empty {
		retirer, ok := s.datastore.(datastore.ExpiredEmptyStackRetirer)
		if !ok {
			return fmt.Errorf("datastore does not support certified expired stack retirement")
		}
		_, err = retirer.TryRetireExpiredEmptyStack(stackInfo, certified.guards, "")
		return err
	}
	result := certified.result
	if result == nil {
		return nil
	}
	digest, err := resolutionHash(struct {
		StackID, StackLabel, OnDependents, ExpiresAt string
		StackCreatedAt                               time.Time
		TTLSeconds                                   *int64
	}{stackInfo.StackID, stackInfo.StackLabel, stackInfo.OnDependents, stackInfo.ExpiresAt, stackInfo.StackCreatedAt, stackInfo.TTLSeconds})
	if err != nil {
		return fmt.Errorf("hash expiry decision: %w", err)
	}
	admission := datastore.CommandAdmission{
		Guards: certified.guards, PrincipalScope: "stack-expirer", IdempotencyKey: result.command.ID,
		RequestDigest: digest, Receipt: []byte(`{"producer":"stack-expirer"}`),
	}

	// Store the forma command
	_, err = messages.UnwrapCall(s.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: s.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *result.command, Admission: &admission},
	))
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

type certifiedExpiredStack struct {
	result *destroyExpiredResult
	guards []datastore.RevisionGuard
	empty  bool
}

type deferredExpiredRetirement struct {
	*planningDatastore
	attempted bool
}

func (d *deferredExpiredRetirement) TryRetireEmptyStack(_, _, _ string) (bool, error) {
	d.attempted = true
	return false, nil
}

func sameExpiredCandidate(left, right datastore.ExpiredStackInfo) bool {
	if left.StackID != right.StackID || left.StackLabel != right.StackLabel ||
		left.OnDependents != right.OnDependents || left.ExpiresAt != right.ExpiresAt ||
		!left.StackCreatedAt.Equal(right.StackCreatedAt) {
		return false
	}
	if left.TTLSeconds == nil || right.TTLSeconds == nil {
		return left.TTLSeconds == nil && right.TTLSeconds == nil
	}
	return *left.TTLSeconds == *right.TTLSeconds
}

func certifyExpiredStack(ds datastore.Datastore, candidate datastore.ExpiredStackInfo) (*certifiedExpiredStack, error) {
	scope := newPlanningDatastore(ds, &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: candidate.StackLabel}}})
	// Resolve the current stack incarnation before taking the first certificate
	// sample. Preparation reads it again inside the certified interval; this read
	// only includes its identity guard in the initial sample.
	if _, err := scope.GetStackByLabel(candidate.StackLabel); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 16; attempt++ {
		var result *destroyExpiredResult
		var empty, eligible bool
		guards, err := scope.certify(func() error {
			eligible = false
			empty = false
			result = nil
			current, err := scope.GetExpiredStacks()
			if err != nil {
				return err
			}
			for _, info := range current {
				if sameExpiredCandidate(candidate, info) {
					eligible = true
					break
				}
			}
			if !eligible {
				return nil
			}
			deferred := &deferredExpiredRetirement{planningDatastore: scope}
			result, err = prepareDestroyExpiredStack(deferred, candidate, "stack-expirer", "stack-expirer-cleanup")
			empty = deferred.attempted
			if err != nil || result == nil {
				return err
			}
			return result.command.ResolveStackIdentities(scope)
		})
		if errors.Is(err, errPlanningScopeExpanded) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !eligible {
			return nil, nil
		}
		return &certifiedExpiredStack{result: result, guards: guards, empty: empty}, nil
	}
	return nil, fmt.Errorf("%w: expiry planning scope did not stabilize after 16 attempts", datastore.ErrStaleAdmission)
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
	// The query matches absolute deadlines by string comparison, which cannot
	// reject an impossible calendar date. Re-check with a real parse before
	// destroying anything: a deadline nobody can read is not a deadline.
	if stackInfo.HasUnreadableDeadline() {
		slog.Warn("Refusing to expire stack: its deadline is not a readable instant",
			"stack", stackInfo.StackLabel, "expiresAt", stackInfo.ExpiresAt)
		return nil, nil
	}

	// Load all resources in the stack
	resources, err := ds.LoadResourcesByStack(stackInfo.StackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to load stack %s: %w", stackInfo.StackLabel, err)
	}
	if len(resources) == 0 {
		// Physical emptiness alone cannot retire failed-create or generator intent.
		retirer, ok := ds.(datastore.EmptyStackRetirer)
		if !ok {
			return nil, fmt.Errorf("datastore does not support atomic stack retirement")
		}
		_, err := retirer.TryRetireEmptyStack(stackInfo.StackID, stackInfo.StackLabel, "")
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
		ds,
		nil, nil,
		false,
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
		nil,                          // No generator updates on destroy
		clientID,
		"",
		"",
		forma_command.SourceStackExpirer,
	)

	// Generate any synthetic Resolve target ops, then build the changeset.
	synth, err := target_update.SynthesizeResolveTargetUpdates(
		resource_update.ReferencedTargetLabels(resourceUpdates),
		resource_update.SourceTargetByKsuid(resourceUpdates),
		nil, ds)
	if err != nil {
		return nil, fmt.Errorf("failed to create changeset: %w", err)
	}
	// No generator draws: a destroy writes no property, and the stack's
	// generators go with it.
	cs, err := changeset.NewChangeset(resourceUpdates, synth, nil, destroyCommand.ID, pkgmodel.CommandDestroy, destroyCommand.Config.Mode)
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
