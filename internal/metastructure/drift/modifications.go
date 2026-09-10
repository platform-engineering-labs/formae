// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package drift reads what has moved out of band on a stack, and decides which
// of those movements the forma being applied already accounts for.
//
// It is its own package because two kinds of caller need the same answer: an
// ordinary apply, which refuses to overwrite a change nobody declared, and an
// unattended actor — auto-reconcile, rotation — which refuses for the same
// reason without a human present to confirm. Those callers cannot share code
// while the readers live in the package that imports them, and the alternative
// is a second copy: the rotation work produced exactly one such copy and
// deleted it, having found it had already drifted from the original by dropping
// an operation filter.
//
// One reader, therefore, rather than one per caller.
package drift

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// loadModificationsAndWitnesses loads one stack's drift window into the
// submission snapshot, plus the write witness for each modified resource
// (GetPropertiesAtLastWrite: the state formae's own last write observed) and
// its stored ownership record (GetOwnedMembers: the co-owned regime's prior
// declaration, read from the latest resource row). Window load failures fail
// the submission; a witness or record fetch failure degrades to no witness /
// no record for that resource, which classifies its movement as tolerated
// (witness) or everything-undeclared (record), the pre-existing behavior.
func LoadModificationsAndWitnesses(ds datastore.Datastore, stackLabel string, modificationsByStack map[string][]datastore.ResourceModification, witnessByKsuid map[string]json.RawMessage, recordByKsuid map[string]pkgmodel.OwnedMembers) error {
	modifications, err := LoadModifications(ds, stackLabel)
	if err != nil {
		slog.Error("Failed to load modifications since last reconcile", "stack", stackLabel, "error", err)
		return fmt.Errorf("failed to load modifications for stack %s: %w", stackLabel, err)
	}
	if len(modifications) == 0 {
		return nil
	}
	var accepted map[string]pkgmodel.OwnedMembers
	if reader, ok := ds.(datastore.DesiredOwnershipReader); ok {
		var err error
		accepted, err = reader.GetDesiredOwnership(stackLabel)
		if err != nil {
			return err
		}
	}
	modificationsByStack[stackLabel] = modifications
	for _, mod := range modifications {
		if mod.Operation != "update" || mod.Ksuid == "" {
			continue
		}
		if _, done := witnessByKsuid[mod.Ksuid]; !done {
			witness, werr := ds.GetPropertiesAtLastWrite(mod.Ksuid)
			if werr != nil {
				slog.Warn("Failed to load write witness for drift classification", "ksuid", mod.Ksuid, "error", werr)
			} else {
				witnessByKsuid[mod.Ksuid] = witness
			}
		}
		if _, done := recordByKsuid[mod.Ksuid]; !done {
			record, acceptedRecord := accepted[mod.Ksuid]
			var rerr error
			if !acceptedRecord {
				record, rerr = ds.GetOwnedMembers(mod.Ksuid)
			}
			if rerr != nil {
				slog.Warn("Failed to load ownership record for drift classification", "ksuid", mod.Ksuid, "error", rerr)
				continue
			}
			recordByKsuid[mod.Ksuid] = record
		}
	}
	return nil
}

// LoadModifications excludes completed explicit destruction from the raw drift
// window. Patch/sync tombstones remain interventions to review; a destroy is
// already durable desired absence. Read through the caller's protected datastore:
// even a discarded candidate must register its physical identity/incarnation.
func LoadModifications(ds datastore.Datastore, stackLabel string) ([]datastore.ResourceModification, error) {
	modifications, err := ds.GetResourceModificationsSinceLastReconcile(stackLabel)
	if err != nil || len(modifications) == 0 {
		return modifications, err
	}
	reader, ok := ds.(datastore.ResourceObservationReader)
	if !ok {
		return modifications, nil
	}
	desired, err := ds.GetResourcesAtLastReconcile(stackLabel)
	if err != nil {
		return nil, err
	}
	desiredIDs := map[string]bool{}
	for _, snapshot := range desired {
		desiredIDs[snapshot.KSUID] = true
	}
	settled := map[string]bool{}
	commands := map[string]*forma_command.FormaCommand{}
	for _, mod := range modifications {
		if _, seen := settled[mod.Ksuid]; seen {
			continue
		}
		settled[mod.Ksuid] = false
		if desiredIDs[mod.Ksuid] {
			continue
		}
		observation, err := reader.GetResourceObservation(mod.Ksuid)
		if err != nil {
			return nil, err
		}
		if observation == nil || observation.Operation != "delete" || !observation.ConfirmedDeletion || observation.StackID == "" || observation.Stack != stackLabel {
			continue
		}
		current, err := ds.GetStackByLabel(stackLabel)
		if err != nil {
			return nil, err
		}
		if current != nil && current.ID != observation.StackID {
			continue
		}
		command, loaded := commands[observation.CommandID]
		if !loaded {
			command, err = ds.GetFormaCommandByCommandID(observation.CommandID)
			if err != nil {
				return nil, err
			}
			commands[observation.CommandID] = command
		}
		if command == nil || command.Command != pkgmodel.CommandDestroy || (command.State != forma_command.CommandStateSuccess && command.State != forma_command.CommandStateFailed) {
			continue
		}
		switch command.Source {
		case "", forma_command.SourceUser, forma_command.SourceAutoReconciler, forma_command.SourceStackExpirer:
		default:
			continue
		}
		// Do not infer an old command's stack identity from today's label mapping.
		matchingStack := false
		for _, membership := range command.Stacks {
			if membership.ID == observation.StackID && membership.Label == stackLabel {
				matchingStack = true
			}
		}
		if !matchingStack {
			continue
		}
		for _, update := range command.ResourceUpdates {
			if update.DesiredState.Ksuid == mod.Ksuid && update.StackLabel == stackLabel && update.Source == resource_update.FormaCommandSourceUser && update.Operation == resource_update.OperationDelete {
				settled[mod.Ksuid] = true
				break
			}
		}
	}
	result := make([]datastore.ResourceModification, 0, len(modifications))
	for _, mod := range modifications {
		if !settled[mod.Ksuid] {
			result = append(result, mod)
		}
	}
	return result, nil
}

// StackLabelsFromForma includes explicitly declared empty stacks in the scope.
func StackLabelsFromForma(forma *pkgmodel.Forma) []string {
	seen := make(map[string]bool)
	var labels []string
	for _, stack := range forma.Stacks {
		if !seen[stack.Label] {
			seen[stack.Label] = true
			labels = append(labels, stack.Label)
		}
	}
	for _, r := range forma.Resources {
		if !seen[r.Stack] {
			seen[r.Stack] = true
			labels = append(labels, r.Stack)
		}
	}
	return labels
}

// toAPIResourceModification converts a datastore ResourceModification into its
// API-model counterpart. For update operations it also computes the JSON-patch
// document describing the drift between the properties at the last reconcile
// and the current (cloud) properties. A failed patch computation degrades to
// label-only display — it never fails the caller.
func ToAPIResourceModification(modification datastore.ResourceModification) apimodel.ResourceModification {
	patchDoc := json.RawMessage(nil)
	if modification.Operation == "update" && len(modification.OldProperties) > 0 && len(modification.Properties) > 0 {
		if p, perr := patch.DriftPatch(modification.OldProperties, modification.Properties); perr == nil {
			patchDoc = p
		} else {
			slog.Warn("failed to compute drift patch", "stack", modification.Stack, "label", modification.Label, "error", perr)
		}
	}
	return apimodel.ResourceModification{
		ResourceID:    modification.Ksuid,
		Stack:         modification.Stack,
		Type:          modification.Type,
		Label:         modification.Label,
		Operation:     modification.Operation,
		PatchDocument: patchDoc,
		Properties:    modification.Properties,
		OldProperties: modification.OldProperties,
	}
}

// filterUnabsorbedModifications returns only those modifications that have NOT been
// absorbed into the provided forma. A modification is considered absorbed when:
//   - The forma contains a resource with matching stack, type, and label
//   - No resource update was generated for that resource (i.e. its properties already
//     match the current state in the datastore)
//
// This prevents false drift rejection when the user has already incorporated
// out-of-band changes into their forma (e.g. via extract) before applying.
func FilterUnabsorbedModifications(
	modifications []datastore.ResourceModification,
	forma *pkgmodel.Forma,
	fa *forma_command.FormaCommand,
) []datastore.ResourceModification {
	// Build a set of resources that have pending updates in the FormaCommand
	type resourceKey struct {
		stack    string
		typeName string
		label    string
	}
	resourcesWithUpdates := make(map[resourceKey]struct{})
	for _, ru := range fa.ResourceUpdates {
		// A convergence-only update propagates a source movement the stored
		// state has already absorbed (e.g. a synced secret rotation reaching
		// its consumer). It asserts no state differing from the current one,
		// so it must not block absorbing the modification it follows from.
		// A record-only update is the same shape for the ownership record:
		// it commits a claim over current state (empty patch, current
		// properties) — the absorb path itself — and asserts no change.
		if ru.ConvergenceOnly() || ru.RecordOnly {
			continue
		}
		resourcesWithUpdates[resourceKey{
			stack:    ru.StackLabel,
			typeName: ru.DesiredState.Type,
			label:    ru.DesiredState.Label,
		}] = struct{}{}
	}

	// Build a set of resources present in the forma
	formaResources := make(map[resourceKey]struct{})
	// A forma resource that declares an `alias` covers its previous
	// label too. Index aliases by (stack, type, alias) so a drift recorded
	// under the old label is absorbed when the forma renames the resource.
	formaAliases := make(map[resourceKey]struct{})
	for _, r := range forma.Resources {
		formaResources[resourceKey{
			stack:    r.Stack,
			typeName: r.Type,
			label:    r.Label,
		}] = struct{}{}
		if r.Alias != "" {
			formaAliases[resourceKey{
				stack:    r.Stack,
				typeName: r.Type,
				label:    r.Alias,
			}] = struct{}{}
		}
	}

	var unabsorbed []datastore.ResourceModification
	for _, mod := range modifications {
		key := resourceKey{
			stack:    mod.Stack,
			typeName: mod.Type,
			label:    mod.Label,
		}
		// A modification is absorbed if:
		// 1. The resource is present in the forma, AND
		// 2. No resource update was generated for it (properties match current state)
		_, inForma := formaResources[key]
		_, hasUpdate := resourcesWithUpdates[key]
		if inForma && !hasUpdate {
			continue // absorbed
		}
		// Alias-aware absorption. A modification keyed by the OLD
		// label is absorbed by a forma resource declaring `alias = <old>`.
		// The rename update (if any) takes the modification with it.
		if _, isAlias := formaAliases[key]; isAlias {
			continue
		}
		unabsorbed = append(unabsorbed, mod)
	}
	return unabsorbed
}
