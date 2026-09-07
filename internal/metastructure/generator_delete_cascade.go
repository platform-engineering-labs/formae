// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// generatorDeletesForDestroy plans the generator deletes a destroy owes.
//
// A generator has no standalone form: it belongs to one stack, and it goes
// when that stack goes. That is what an inline policy does too, but a policy
// gets there by a route a generator has no access to — DeleteStack cascades to
// the policies table and not to the generators one — so nothing removed a
// generator's row until this ran. A destroy left the row live, and because the
// reverse index and the identity lookup both window on the generator's KSUID
// rather than on its stack, a resource in another stack went on resolving its
// binding against a generator whose stack no longer existed.
//
// A stack goes when the command empties it, which is the same test
// cleanupEmptyStacks applies once the changeset is done: no resources left.
// Asking it here rather than there is what makes the delete possible at all —
// DeleteGenerator resolves the stack label to a stack id, so a delete issued
// after the stack is tombstoned is a silent no-op — and it is also what lets
// the refusal below be planned from the same set the deletes are.
//
// Only the stacks this command deletes something from are considered: a
// destroy that removes some of a stack's resources leaves the stack, and its
// generators, alone.
func generatorDeletesForDestroy(
	resourceUpdates []resource_update.ResourceUpdate,
	ds datastore.Datastore,
) ([]generator_update.GeneratorUpdate, error) {
	deletedPerStack := make(map[string]map[string]bool)
	for i := range resourceUpdates {
		ru := &resourceUpdates[i]
		if ru.Operation != resource_update.OperationDelete || ru.DesiredState.Ksuid == "" {
			continue
		}
		stack := ru.DesiredState.Stack
		if stack == "" {
			continue
		}
		if deletedPerStack[stack] == nil {
			deletedPerStack[stack] = make(map[string]bool)
		}
		deletedPerStack[stack][ru.DesiredState.Ksuid] = true
	}
	if len(deletedPerStack) == 0 {
		return nil, nil
	}

	stacks := make([]string, 0, len(deletedPerStack))
	for stack := range deletedPerStack {
		stacks = append(stacks, stack)
	}
	// The map iteration order is not the order the updates are read in, and
	// the deletes reach an operator through the simulated plan.
	slices.Sort(stacks)

	now := util.TimeNow()
	var deletes []generator_update.GeneratorUpdate
	for _, stack := range stacks {
		generators, err := ds.LoadGeneratorsByStack(stack)
		if err != nil {
			return nil, fmt.Errorf("failed to load the generators owned by stack %q: %w", stack, err)
		}
		if len(generators) == 0 {
			continue
		}

		live, err := ds.CountResourcesInStack(stack)
		if err != nil {
			return nil, fmt.Errorf("failed to count the resources in stack %q: %w", stack, err)
		}
		// Every delete is derived from a live row, so the two counts can only
		// agree when the command takes the whole stack.
		if live != len(deletedPerStack[stack]) {
			slog.Debug("Stack survives this destroy, leaving its generators in place",
				"stack", stack, "liveResources", live, "deleted", len(deletedPerStack[stack]))
			continue
		}

		for _, generator := range generators {
			deletes = append(deletes, generator_update.GeneratorUpdate{
				Generator:  generator,
				Operation:  generator_update.GeneratorOperationDelete,
				State:      generator_update.GeneratorUpdateStateNotStarted,
				StackLabel: stack,
				StartTs:    now,
				ModifiedTs: now,
			})
		}
	}

	return deletes, nil
}

// planGeneratorDeleteCascade answers, for every generator this command
// deletes, which live resources would be left binding it, and what destroying
// those resources alongside it would look like.
//
// It returns both because the caller has to choose between them: naming the
// dependents in a refusal, or planning the cascade. The two are one walk over
// one set of rows, and computing them apart would let them disagree about what
// a dependent is.
//
// A dependent is a live destination of the generator that this command does
// NOT tear down and that still binds the generator when the command is done:
//
//   - a resource the command deletes is going anyway, which is what makes
//     destroying a self-contained stack — generator and destination together —
//     an ordinary destroy rather than a refusal;
//   - a resource the command plans a write for is judged on its DESIRED
//     document, so an apply that drops a generator and rewrites its
//     destination to a literal in the same forma is not a dangling reference
//     and is not refused;
//   - a resource the command does not reach at all keeps what it has stored,
//     which is a binding to a generator that is about to stop existing.
//
// This is the one place both arms of a generator delete are judged. A destroy
// of the generator's stack and a reconcile that drops the generator's
// declaration produce the same GeneratorOperationDelete, so they get the same
// answer here without either arm carrying a rule of its own.
func planGeneratorDeleteCascade(
	generatorUpdates []generator_update.GeneratorUpdate,
	resourceUpdates []resource_update.ResourceUpdate,
	existingTargets []*pkgmodel.Target,
	source resource_update.FormaCommandSource,
	ds datastore.Datastore,
) ([]resource_update.ResourceUpdate, []apimodel.GeneratorDependent, error) {
	var deletes []*generator_update.GeneratorUpdate
	for i := range generatorUpdates {
		if generatorUpdates[i].Operation == generator_update.GeneratorOperationDelete &&
			generatorUpdates[i].Generator != nil {
			deletes = append(deletes, &generatorUpdates[i])
		}
	}
	if len(deletes) == 0 {
		return nil, nil, nil
	}

	beingDeleted := make(map[string]bool, len(resourceUpdates))
	planned := make(map[string]bool, len(resourceUpdates))
	desiredByKsuid := make(map[string][]byte, len(resourceUpdates))
	for i := range resourceUpdates {
		ru := &resourceUpdates[i]
		for _, ksuid := range []string{ru.PriorState.Ksuid, ru.DesiredState.Ksuid} {
			if ksuid == "" {
				continue
			}
			planned[ksuid] = true
			if ru.Operation == resource_update.OperationDelete || ru.Operation == resource_update.OperationReaped {
				beingDeleted[ksuid] = true
			}
		}
		if ru.DesiredState.Ksuid != "" && ru.Operation != resource_update.OperationDelete {
			desiredByKsuid[ru.DesiredState.Ksuid] = ru.DesiredState.Properties
		}
	}

	existingTargetMap := make(map[string]*pkgmodel.Target, len(existingTargets))
	for _, target := range existingTargets {
		existingTargetMap[target.Label] = target
	}

	var cascade []resource_update.ResourceUpdate
	var dependents []apimodel.GeneratorDependent
	cascaded := make(map[string]bool)

	for _, gu := range deletes {
		label := gu.Generator.GetLabel()
		// A generator loaded from the datastore carries no KSUID of its own —
		// PasswordGenerator.ID is json:"-", so it lives only in the generators
		// table — and a delete update is always built from a load, on both
		// arms. Resolving it here, from the label and stack the update is
		// filed under, is what gives both arms the same identity to ask the
		// reverse index about.
		identity, err := ds.GetGeneratorIdentity(label, gu.StackLabel)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve the identity of generator %q in stack %q: %w",
				label, gu.StackLabel, err)
		}
		if identity.ID == "" {
			continue
		}

		bound, err := ds.FindResourcesReferencingGenerator(identity.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to find the resources bound to generator %q in stack %q: %w",
				label, gu.StackLabel, err)
		}

		for _, destination := range bound {
			if destination == nil || destination.Ksuid == "" || beingDeleted[destination.Ksuid] {
				continue
			}
			if planned[destination.Ksuid] &&
				!bindsGenerator(desiredByKsuid[destination.Ksuid], identity.ID) {
				continue
			}

			dependents = append(dependents, apimodel.GeneratorDependent{
				GeneratorLabel: label,
				GeneratorStack: gu.StackLabel,
				ResourceLabel:  destination.Label,
				ResourceType:   destination.Type,
				Stack:          destination.Stack,
			})

			// One resource can bind two generators this command deletes, and
			// the changeset keeps one node per operation URI: a second delete
			// for it would be dropped on the floor with nothing terminalizing
			// it.
			if cascaded[destination.Ksuid] {
				continue
			}
			cascaded[destination.Ksuid] = true

			target, ok := existingTargetMap[destination.Target]
			if !ok {
				// Refusing beats cascading into a resource we cannot plan a
				// delete for: leaving it bound to a deleted generator is the
				// state this whole check exists to prevent.
				return nil, nil, fmt.Errorf(
					"cannot destroy resource %q in stack %q alongside generator %q: its target %q is unknown",
					destination.Label, destination.Stack, label, destination.Target)
			}

			destroy, err := resource_update.NewResourceUpdateForDestroy(*destination, *target, source)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to plan the destroy of %q bound to generator %q: %w",
					destination.Label, label, err)
			}
			destroy.IsCascade = true
			destroy.CascadeSource = label
			cascade = append(cascade, destroy)
		}
	}

	// The list is read by an operator who has to go and find each resource,
	// and no datastore backend orders the rows it answers with, so two runs of
	// the same refused command would otherwise name them in a different order.
	slices.SortFunc(dependents, func(a, b apimodel.GeneratorDependent) int {
		return cmp.Or(
			strings.Compare(a.GeneratorStack, b.GeneratorStack),
			strings.Compare(a.GeneratorLabel, b.GeneratorLabel),
			strings.Compare(a.Stack, b.Stack),
			strings.Compare(a.ResourceLabel, b.ResourceLabel),
			strings.Compare(a.ResourceType, b.ResourceType),
		)
	})

	return cascade, dependents, nil
}

// bindsGenerator reports whether a resource's desired properties still bind
// any property to this generator. It reads the properties document directly
// rather than through pkgmodel.BindsGenerator, which takes a whole stored
// resource row; the walk underneath is the same one, so the answer is the same
// as the reverse index's.
func bindsGenerator(properties []byte, generatorKsuid string) bool {
	if len(properties) == 0 || generatorKsuid == "" {
		return false
	}
	for _, occurrence := range pkgmodel.FindGenObjectsFromProperties(properties) {
		if occurrence.Generator == generatorKsuid {
			return true
		}
	}
	return false
}
