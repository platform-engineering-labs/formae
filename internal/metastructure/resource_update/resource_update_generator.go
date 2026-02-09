// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"

	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// GenerateResourceUpdates converts a Forma and command parameters into ResourceUpdates that can be executed
func GenerateResourceUpdates(
	forma *pkgmodel.Forma,
	command pkgmodel.Command,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
	existingTargets []*pkgmodel.Target,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	var referenceLabels map[string]string
	var err error

	// Only translate references for commands that contain new/modified resources with triplet URIs
	// Skip for synchronization (database reads), discovery (pre-assigned KSUIDs), and destroy (database resources)
	doTranslateFormaeReferencesToKsuid := source != FormaCommandSourceSynchronize &&
		source != FormaCommandSourceDiscovery &&
		command != pkgmodel.CommandDestroy

	if doTranslateFormaeReferencesToKsuid {
		referenceLabels, err = translateFormaeReferencesToKsuid(forma, ds)
		if err != nil {
			return nil, fmt.Errorf("failed to translate references to KSUID: %w", err)
		}
	}

	var resourceUpdates []ResourceUpdate

	var targetMap = make(map[string]*pkgmodel.Target)
	for _, target := range existingTargets {
		targetMap[target.Label] = target
	}

	for _, target := range forma.Targets {
		if _, exists := targetMap[target.Label]; !exists {
			slog.Debug("Target does not exist in existing targets - adding it", "target", target.Label)
			targetMap[target.Label] = &target // Add it to the map for consistency
		}
	}

	// Validate stack references for commands that modify resources, sync commands are triggered from the agent
	// and are guaranteed to reference existing stacks only
	if command == pkgmodel.CommandDestroy || command == pkgmodel.CommandApply {
		if err := validateStackReferences(forma, ds); err != nil {
			return nil, err
		}
	}

	for _, r := range forma.Resources {
		if _, exists := targetMap[r.Target]; !exists {
			return nil, apimodel.TargetReferenceNotFoundError{
				TargetLabel: r.Target,
			}
		}
	}

	switch command {
	case pkgmodel.CommandDestroy:
		resourceUpdates, err = generateResourceUpdatesForDestroy(forma, source, targetMap, ds)
	case pkgmodel.CommandApply:
		resourceUpdates, err = generateResourceUpdatesForApply(forma, mode, source, targetMap, ds)
	case pkgmodel.CommandSync:
		resourceUpdates, err = generateResourceUpdatesForSync(forma, source, targetMap, ds)
	default:
		return nil, fmt.Errorf("unsupported command type: %s", command)
	}

	if err != nil {
		return nil, err
	}

	// Populate targets for each resource update
	for _, resourceUpdate := range resourceUpdates {
		targetLabel := resourceUpdate.DesiredState.Target

		// First check forma targets
		for _, target := range forma.Targets {
			if target.Label == targetLabel {
				resourceUpdate.ResourceTarget = target
				break
			}
		}

		// If not found, check existing targets
		if resourceUpdate.ResourceTarget.Label == "" {
			for _, target := range existingTargets {
				if target.Label == targetLabel {
					resourceUpdate.ResourceTarget = *target
					break
				}
			}
		}
	}

	if doTranslateFormaeReferencesToKsuid {
		for i := range resourceUpdates {
			resourceUpdates[i].ReferenceLabels = referenceLabels
		}
	}

	return resourceUpdates, nil
}

// stackExistsInForma checks if a stack label exists in the Forma.Stacks slice
func stackExistsInForma(forma *pkgmodel.Forma, stackLabel string) bool {
	for _, stack := range forma.Stacks {
		if stack.Label == stackLabel {
			return true
		}
	}
	return false
}

func validateStackReferences(forma *pkgmodel.Forma, ds ResourceDataLookup) error {
	// Find all stacks referenced by resources that aren't in Forma.Stacks
	stacksToValidate := make(map[string]bool)
	for _, resource := range forma.Resources {
		if !stackExistsInForma(forma, resource.Stack) {
			stacksToValidate[resource.Stack] = true
		}
	}

	// Validate each of these stacks exists in the database
	for stackLabel := range stacksToValidate {
		existingResources, err := ds.LoadResourcesByStack(stackLabel)
		if err != nil {
			return fmt.Errorf("failed to load stack %s: %w", stackLabel, err)
		}
		if len(existingResources) == 0 {
			return apimodel.StackReferenceNotFoundError{
				StackLabel: stackLabel,
			}
		}
	}

	return nil
}

func generateResourceUpdatesForDestroy(
	forma *pkgmodel.Forma,
	source FormaCommandSource,
	targetMap map[string]*pkgmodel.Target,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	var resourceDestroys []ResourceUpdate

	// Track KSUIDs of resources being explicitly deleted
	explicitDeleteKSUIDs := make(map[string]bool)

	for _, stack := range forma.SplitByStack() {
		existingResources, err := ds.LoadResourcesByStack(stack.SingleStackLabel())
		if err != nil {
			slog.Error("Failed to load stack", "error", err)
			continue
		}

		if len(existingResources) == 0 {
			// Stack doesn't exist, nothing to delete
			continue
		}

		// Create delete resource updates for existing resources
		for _, existingResource := range existingResources {
			for _, formaResource := range forma.Resources {
				if formaResource.Stack == stack.SingleStackLabel() &&
					formaResource.Label == existingResource.Label &&
					formaResource.Type == existingResource.Type {
					resourceDestroy, err := NewResourceUpdateForDestroy(
						*existingResource,
						*targetMap[existingResource.Target],
						source,
					)
					if err != nil {
						return nil, fmt.Errorf("failed to create resource destroy for %s: %w", existingResource.Label, err)
					}

					explicitDeleteKSUIDs[existingResource.Ksuid] = true
					resourceDestroys = append(resourceDestroys, resourceDestroy)
				}
			}
		}
	}

	// Find cascade deletes - resources that reference the resources being deleted
	cascadeDeletes, err := findCascadeDeletes(explicitDeleteKSUIDs, targetMap, source, ds)
	if err != nil {
		return nil, fmt.Errorf("failed to find cascade deletes: %w", err)
	}

	resourceDestroys = append(resourceDestroys, cascadeDeletes...)

	return resourceDestroys, nil
}

// findCascadeDeletes finds all resources that must be deleted because they reference
// resources being deleted. Uses BFS to find the full cascade chain.
func findCascadeDeletes(
	toDelete map[string]bool,
	targetMap map[string]*pkgmodel.Target,
	source FormaCommandSource,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	var cascadeDeletes []ResourceUpdate
	processed := make(map[string]bool)

	// Copy toDelete to processed - these are already being deleted
	for ksuid := range toDelete {
		processed[ksuid] = true
	}

	// BFS queue - start with all explicit deletes
	queue := make([]string, 0, len(toDelete))
	for ksuid := range toDelete {
		queue = append(queue, ksuid)
	}

	for len(queue) > 0 {
		currentKSUID := queue[0]
		queue = queue[1:]

		// Find resources that depend on the current resource
		dependents, err := ds.FindResourcesDependingOn(currentKSUID)
		if err != nil {
			return nil, fmt.Errorf("failed to find resources depending on %s: %w", currentKSUID, err)
		}

		for _, dependent := range dependents {
			if processed[dependent.Ksuid] {
				continue
			}
			processed[dependent.Ksuid] = true

			// Skip unmanaged (discovered) resources - they have implicit lifecycle
			// ties to their parents and the provider will handle their deletion automatically.
			// If they can't be deleted, the user will see an error from the provider explaining why.
			if dependent.Stack == constants.UnmanagedStack {
				slog.Debug("Skipping cascade delete for unmanaged resource",
					"resource", dependent.Label,
					"type", dependent.Type,
					"dependsOn", currentKSUID)
				continue
			}

			// Find the source resource label for the cascade message
			sourceLabel := currentKSUID // fallback to KSUID
			if _, isExplicit := toDelete[currentKSUID]; isExplicit {
				sourceLabel = currentKSUID
			}

			target, ok := targetMap[dependent.Target]
			if !ok {
				slog.Warn("Target not found for cascade delete", "target", dependent.Target, "resource", dependent.Label)
				continue
			}

			resourceDestroy, err := NewResourceUpdateForDestroy(
				*dependent,
				*target,
				source,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create cascade destroy for %s: %w", dependent.Label, err)
			}

			resourceDestroy.IsCascade = true
			resourceDestroy.CascadeSource = sourceLabel

			cascadeDeletes = append(cascadeDeletes, resourceDestroy)

			// Add to queue for further cascade detection
			queue = append(queue, dependent.Ksuid)
		}
	}

	return cascadeDeletes, nil
}

func generateResourceUpdatesForApply(
	forma *pkgmodel.Forma,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
	targetMap map[string]*pkgmodel.Target,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	for _, target := range forma.Targets {
		if existingTarget, ok := targetMap[target.Label]; ok {
			if existingTarget.Namespace != target.Namespace {
				return nil, apimodel.TargetAlreadyExistsError{
					TargetLabel:       target.Label,
					ExistingNamespace: existingTarget.Namespace,
					FormaNamespace:    target.Namespace,
					MismatchType:      "namespace",
				}
			}

			if !util.JsonEqualRaw(existingTarget.Config, target.Config) {
				return nil, apimodel.TargetAlreadyExistsError{
					TargetLabel:    target.Label,
					MismatchType:   "config",
					ExistingConfig: existingTarget.Config,
					FormaConfig:    target.Config,
				}
			}
		}
	}

	switch mode {
	case pkgmodel.FormaApplyModeReconcile:
		return generateResourceUpdatesForReconcile(forma, mode, source, targetMap, ds)
	case pkgmodel.FormaApplyModePatch:
		return generateResourceUpdatesForPatch(forma, mode, source, targetMap, ds)
	default:
		return nil, fmt.Errorf("forma apply mode %s not supported", mode)
	}
}

func generateResourceUpdatesForSync(
	forma *pkgmodel.Forma,
	source FormaCommandSource,
	targetMap map[string]*pkgmodel.Target,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	var resourceUpdates []ResourceUpdate

	for _, stack := range forma.SplitByStack() {
		existingResources, err := ds.LoadResourcesByStack(stack.SingleStackLabel())
		if err != nil {
			slog.Error("Failed to load stack", "error", err)
			continue
		}

		if source == FormaCommandSourceDiscovery {
			for _, r := range forma.Resources {
				if r.Stack == stack.SingleStackLabel() {
					ru, err := NewResourceUpdateForSyncWithFilter(r, *targetMap[r.Target], source)
					if err != nil {
						return nil, fmt.Errorf("failed to create resource update sync for %s: %w", r.Label, err)
					}
					resourceUpdates = append(resourceUpdates, ru)
				}
			}

			// Avoid accessing existingResources for alternate code path below
			continue
		}

		// Normal sync - create read resource updates for existing resources
		for _, existingResource := range existingResources {
			for _, resource := range forma.Resources {
				if resource.Stack == stack.SingleStackLabel() &&
					resource.Label == existingResource.Label &&
					resource.Type == existingResource.Type {

					resourceUpdate, err := NewResourceUpdateForSync(
						*existingResource,
						*targetMap[existingResource.Target],
						source,
					)
					if err != nil {
						return nil, fmt.Errorf("failed to create resource update sync for %s: %w", existingResource.Label, err)
					}
					resourceUpdates = append(resourceUpdates, resourceUpdate)
				}
			}
		}
	}

	return resourceUpdates, nil
}

func generateResourceUpdatesForReconcile(
	forma *pkgmodel.Forma,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
	targetMap map[string]*pkgmodel.Target,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	var resourceCreates []ResourceUpdate
	var resourceUpdates []ResourceUpdate
	var resourceReplaces []ResourceUpdate
	var implicitDeleteResources []ResourceUpdate

	allResourcesByStack, err := ds.LoadAllResourcesByStack()
	if err != nil {
		return nil, fmt.Errorf("failed to load existing stacks: %w", err)
	}

	for _, stack := range forma.SplitByStack() {
		existingResources, err := ds.LoadResourcesByStack(stack.SingleStackLabel())
		if err != nil {
			slog.Error("Failed to load stack", "error", err)
			continue
		}

		// Existing stack not found which means that all resources will be created.
		if len(existingResources) == 0 {
			for _, newResource := range stack.Resources {
				if existingUnmanaged, ok := findUnmanagedResource(newResource, allResourcesByStack); ok {
					readOnlyProperties, err := resolver.LoadResolvablePropertiesFromStacks(newResource, allResourcesByStack)
					if err != nil {
						return nil, fmt.Errorf("failed to load resolvable properties: %w", err)
					}

					resourceUpdate, err := NewResourceUpdateForExisting(
						readOnlyProperties,
						existingUnmanaged,
						newResource,
						*targetMap[existingUnmanaged.Target],
						*targetMap[newResource.Target],
						mode,
						source,
					)
					if err != nil {
						return nil, fmt.Errorf("failed to generate resource update for existing unmanaged resource: %w", err)
					}

					for _, update := range resourceUpdate {
						switch update.Operation {
						case OperationUpdate:
							resourceUpdates = append(resourceUpdates, update)
						case OperationDelete:
							resourceReplaces = append(resourceReplaces, update)
						case OperationCreate:
							resourceReplaces = append(resourceReplaces, update)
						default:
							resourceReplaces = append(resourceReplaces, update)
						}
					}
				} else {
					resourceCreate, err := NewResourceUpdateForCreate(
						newResource,
						*targetMap[newResource.Target],
						source,
					)
					if err != nil {
						return nil, err
					}
					resourceCreates = append(resourceCreates, resourceCreate)
				}
			}
			continue
		}

		// Now process existing resources
		for _, existingResource := range existingResources {
			found := false
			for _, newResource := range stack.Resources {
				if newResource.Label == existingResource.Label &&
					newResource.Type == existingResource.Type &&
					newResource.Target == existingResource.Target &&
					(newResource.Stack == existingResource.Stack || existingResource.Stack == constants.UnmanagedStack) {

					found = true

					readOnlyProperties, err := resolver.LoadResolvablePropertiesFromStacks(newResource, allResourcesByStack)

					if err != nil {
						return nil, fmt.Errorf("failed to load resolvable properties: %w", err)
					}
					existingResourceUpdates, err := NewResourceUpdateForExisting(
						readOnlyProperties,
						*existingResource,
						newResource,
						*targetMap[existingResource.Target],
						*targetMap[newResource.Target],
						mode,
						source,
					)

					if err != nil {
						return nil, fmt.Errorf("failed to generate resource update for existing resource: %w",
							err)
					}

					if len(existingResourceUpdates) == 0 {
						slog.Debug("No changes detected for resource", "resource", existingResource.Label)
						continue
					}
					if len(existingResourceUpdates) == 1 {
						slog.Debug("Update resource update generated", "resource", existingResource.Label)
					}

					if len(existingResourceUpdates) == 2 {
						slog.Debug("Replace resource update generated", "resource", existingResource.Label)

					}

					for _, update := range existingResourceUpdates {
						switch update.Operation {
						case OperationUpdate:
							resourceUpdates = append(resourceUpdates, update)
						case OperationDelete:
							resourceReplaces = append(resourceReplaces, update)
						case OperationCreate:
							resourceReplaces = append(resourceReplaces, update)
						default:
							// For any other operations, add to resourceReplaces
							resourceReplaces = append(resourceReplaces, update)
						}
					}
				}
			}

			if !found {
				// Resource exists in the stack but not in the new resources, so it will be deleted implicitly
				resourceDelete, err := NewResourceUpdateForDestroy(
					*existingResource,
					*targetMap[existingResource.Target],
					source,
				)
				if err != nil {
					return nil, fmt.Errorf("failed to create resource delete for %s: %w", existingResource.Label, err)
				}
				implicitDeleteResources = append(implicitDeleteResources, resourceDelete)
			}
		}

		for _, newResource := range stack.Resources {
			found := false
			for _, existingResource := range existingResources {
				if newResource.Label == existingResource.Label &&
					newResource.Type == existingResource.Type &&
					newResource.Target == existingResource.Target &&
					(newResource.Stack == existingResource.Stack || existingResource.Stack == constants.UnmanagedStack) {
					found = true
					break
				}
			}

			if !found {
				// Check if this resource exists as an unmanaged resource
				if existingUnmanaged, ok := findUnmanagedResource(newResource, allResourcesByStack); ok {
					readOnlyProperties, err := resolver.LoadResolvablePropertiesFromStacks(newResource, allResourcesByStack)
					if err != nil {
						return nil, fmt.Errorf("failed to load resolvable properties: %w", err)
					}

					resourceUpdate, err := NewResourceUpdateForExisting(
						readOnlyProperties,
						existingUnmanaged,
						newResource,
						*targetMap[existingUnmanaged.Target],
						*targetMap[newResource.Target],
						mode,
						source,
					)
					if err != nil {
						return nil, fmt.Errorf("failed to generate resource update for unmanaged resource: %w", err)
					}

					for _, update := range resourceUpdate {
						switch update.Operation {
						case OperationUpdate:
							resourceUpdates = append(resourceUpdates, update)
						case OperationDelete:
							resourceReplaces = append(resourceReplaces, update)
						case OperationCreate:
							resourceReplaces = append(resourceReplaces, update)
						default:
							resourceReplaces = append(resourceReplaces, update)
						}
					}
				} else {
					// New resource that doesn't exist anywhere, so it will be created
					resourceCreate, err := NewResourceUpdateForCreate(
						newResource,
						*targetMap[newResource.Target],
						source,
					)
					if err != nil {
						return nil, err
					}

					resourceCreates = append(resourceCreates, resourceCreate)
				}
			}
		}
	}

	// After processing all stacks, find dependencies for delete operations
	allDeleteUpdates := append(resourceReplaces, implicitDeleteResources...)

	dependencyDeletes := findDependencyUpdates(allDeleteUpdates, allResourcesByStack, targetMap, source)

	// Convert updates to replacements if they have dependency deletes

	// Combine all updates
	var allResourceUpdates []ResourceUpdate
	allResourceUpdates = append(allResourceUpdates, resourceCreates...)
	allResourceUpdates = append(allResourceUpdates, resourceReplaces...)
	allResourceUpdates = append(allResourceUpdates, resourceUpdates...)
	allResourceUpdates = append(allResourceUpdates, implicitDeleteResources...)

	// Convert dependency deletes to replacements if the resource is in Forma
	// and has no update or create operation
	convertedDependencyDeletes := convertDependencyDeletesToReplacements(allResourceUpdates, dependencyDeletes, forma, targetMap, source)
	allResourceUpdates = append(allResourceUpdates, convertedDependencyDeletes...)

	finalResourceUpdates := convertUpdatesToReplacementsForDependencies(allResourceUpdates, dependencyDeletes, source)
	return finalResourceUpdates, nil
}

func findUnmanagedResource(resource pkgmodel.Resource, allResources map[string][]*pkgmodel.Resource) (pkgmodel.Resource, bool) {
	unmanagedResources, exists := allResources[constants.UnmanagedStack]
	if !exists {
		return pkgmodel.Resource{}, false
	}
	for _, res := range unmanagedResources {
		if res.Type == resource.Type && res.Label == resource.Label {
			return *res, true
		}
	}
	return pkgmodel.Resource{}, false
}

func generateResourceUpdatesForPatch(
	forma *pkgmodel.Forma,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
	targetMap map[string]*pkgmodel.Target,
	ds ResourceDataLookup) ([]ResourceUpdate, error) {

	var resourceCreates []ResourceUpdate
	var resourceUpdates []ResourceUpdate
	var resourceReplaces []ResourceUpdate

	allResourcesByStack, err := ds.LoadAllResourcesByStack()
	if err != nil {
		return nil, fmt.Errorf("failed to load existing stacks: %w", err)
	}

	for _, stack := range forma.SplitByStack() {
		stackResources, err := ds.LoadResourcesByStack(stack.SingleStackLabel())
		if err != nil {
			slog.Error("Failed to load stack", "error", err)
			continue
		}

		// Existing stack not found which means that all resources will be created.
		if len(stackResources) == 0 {
			for _, newResource := range stack.Resources {
				resourceCreate, err := NewResourceUpdateForCreate(
					newResource,
					*targetMap[newResource.Target],
					source,
				)
				if err != nil {
					return nil, err
				}
				resourceCreates = append(resourceCreates, resourceCreate)
			}
			continue
		}

		// Process new resources in the stack - include unmanaged resources
		existingResources := make([]*pkgmodel.Resource, 0, len(stackResources))
		existingResources = append(existingResources, stackResources...)
		if unmanagedResources, ok := allResourcesByStack[constants.UnmanagedStack]; ok {
			existingResources = append(existingResources, unmanagedResources...)
		}

		for _, newResource := range stack.Resources {
			resourceExists := false

			for _, existingResource := range existingResources {
				// Check for existing resource with same label and type
				if existingResource.Label == newResource.Label && existingResource.Type == newResource.Type {
					resourceExists = true

					// Use NewResourceUpdateForExisting to handle all the logic
					readOnlyProperties, err := resolver.LoadResolvablePropertiesFromStacks(newResource, allResourcesByStack)
					if err != nil {
						return nil, fmt.Errorf("failed to load resolvable properties: %w", err)
					}

					existingResourceUpdates, err := NewResourceUpdateForExisting(
						readOnlyProperties,
						*existingResource,
						newResource,
						*targetMap[existingResource.Target],
						*targetMap[newResource.Target],
						mode,
						source,
					)

					if err != nil {
						return nil, fmt.Errorf("failed to generate resource update for existing resource: %w", err)
					}

					// Process the returned updates
					for _, update := range existingResourceUpdates {
						switch update.Operation {
						case OperationUpdate:
							resourceUpdates = append(resourceUpdates, update)
						case OperationDelete:
							resourceReplaces = append(resourceReplaces, update)
						case OperationCreate:
							resourceReplaces = append(resourceReplaces, update)
						default:
							// For any other operations, add to resourceReplaces
							resourceReplaces = append(resourceReplaces, update)
						}
					}
					break
				}
			}

			// If resource doesn't exist in the existing stack, create it
			if !resourceExists {
				resourceCreate, err := NewResourceUpdateForCreate(
					newResource,
					*targetMap[newResource.Target],
					source,
				)
				if err != nil {
					return nil, err
				}
				resourceCreates = append(resourceCreates, resourceCreate)
			}
		}
	}

	allUpdates := append(append(resourceCreates, resourceUpdates...), resourceReplaces...)

	dependencyDeletes := findDependencyUpdates(resourceReplaces, allResourcesByStack, targetMap, source)
	finalResourceUpdates := convertUpdatesToReplacementsForDependencies(allUpdates, dependencyDeletes, source)
	return finalResourceUpdates, nil
}

// findResourcesThatDependOn finds all resources that have dependencies on the given resource
func findResourcesThatDependOn(targetResource pkgmodel.Resource, allResources map[string][]*pkgmodel.Resource) ([]pkgmodel.Resource, error) {
	var dependentResources []pkgmodel.Resource
	targetURI := targetResource.URI()

	for _, resources := range allResources {
		for _, resource := range resources {
			// Skip the target resource itself
			if resource.Label == targetResource.Label &&
				resource.Stack == targetResource.Stack &&
				resource.Type == targetResource.Type {
				continue
			}

			// Check if this resource has a dependency on the target resource
			uris := resolver.ExtractResolvableURIs(*resource)
			for _, uri := range uris {
				if uri.Stripped() == targetURI.Stripped() {
					dependentResources = append(dependentResources, *resource)
					break
				}
			}
		}
	}

	return dependentResources, nil
}

// findDependencyDeletes finds resources that need to be deleted because they depend on resources being deleted
func findDependencyUpdates(allDeleteUpdates []ResourceUpdate, allResources map[string][]*pkgmodel.Resource, targetMap map[string]*pkgmodel.Target, source FormaCommandSource) []ResourceUpdate {
	var dependencyDeletes []ResourceUpdate

	for _, deleteUpdate := range allDeleteUpdates {
		if deleteUpdate.Operation == OperationDelete {
			// Find all resources that depend on this resource being deleted
			dependentResources, err := findResourcesThatDependOn(deleteUpdate.DesiredState, allResources)
			if err != nil {
				slog.Warn("Failed to find dependent resources",
					"resource", deleteUpdate.DesiredState.Label,
					"error", err)
				continue
			}

			// Create delete operations for dependent resources
			for _, dependentRes := range dependentResources {
				// Check if this dependent resource is not already being deleted
				alreadyBeingDeleted := false
				for _, existingDelete := range allDeleteUpdates {
					if existingDelete.DesiredState.Label == dependentRes.Label &&
						existingDelete.DesiredState.Stack == dependentRes.Stack &&
						existingDelete.DesiredState.Type == dependentRes.Type {
						alreadyBeingDeleted = true
						break
					}
				}

				if !alreadyBeingDeleted {
					// Create a dependency delete operation
					dependencyDelete, err := NewResourceUpdateForDestroy(
						dependentRes,
						*targetMap[dependentRes.Target],
						source,
					)
					if err != nil {
						slog.Error("Failed to create dependency delete for resource",
							"resource", dependentRes.Label,
							"error", err)
						continue
					}
					dependencyDeletes = append(dependencyDeletes, dependencyDelete)
					slog.Debug("Adding dependency delete",
						"dependent", dependentRes.Label,
						"dependsOn", deleteUpdate.DesiredState.Label)
				}
			}
		}
	}

	return dependencyDeletes
}

func convertUpdatesToReplacementsForDependencies(allResourceUpdates []ResourceUpdate, dependencyDeletes []ResourceUpdate, source FormaCommandSource) []ResourceUpdate {
	var finalResourceUpdates []ResourceUpdate

	for _, update := range allResourceUpdates {
		if update.Operation == OperationUpdate {
			// Check if this update resource depends on any resources being deleted
			hasDependencyOnDeletedResource := false

			for _, depDelete := range dependencyDeletes {
				deletedResourceURI := depDelete.DesiredState.URI()
				if update.DesiredState.URI().Stripped() == deletedResourceURI.Stripped() {
					hasDependencyOnDeletedResource = true
					break
				}
			}

			if hasDependencyOnDeletedResource {
				// Convert update to replacement (delete + create) because it depends on a resource being deleted
				replaceUpdates, err := NewResourceUpdateForReplace(
					update.PriorState,
					update.DesiredState,
					update.ExistingTarget,
					update.ResourceTarget,
					source,
				)
				if err != nil {
					slog.Error("Failed to create replacement updates for resource",
						"resource", update.DesiredState.Label,
						"error", err)
					continue
				}

				finalResourceUpdates = append(finalResourceUpdates, replaceUpdates...)
				for i, depDelete := range dependencyDeletes {
					if depDelete.DesiredState.URI().Stripped() == update.DesiredState.URI().Stripped() {
						// Remove this dependency delete as it has been converted to an update
						dependencyDeletes = append(dependencyDeletes[:i], dependencyDeletes[i+1:]...)
						break
					}
				}

			} else {
				// Keep as update
				finalResourceUpdates = append(finalResourceUpdates, update)
			}
		} else {
			// Keep non-update operations as is
			finalResourceUpdates = append(finalResourceUpdates, update)
		}
	}

	//Aftewards it should add the dependency deletes to the final updates that are not part yet based on the forma uri and delete
	for _, depDelete := range dependencyDeletes {
		// Check if this dependency delete is already in the final updates
		found := false
		for _, finalUpdate := range finalResourceUpdates {
			if finalUpdate.DesiredState.URI().Stripped() == depDelete.DesiredState.URI().Stripped() && finalUpdate.Operation == OperationDelete {
				found = true
				break
			}
		}
		if !found {
			finalResourceUpdates = append(finalResourceUpdates, depDelete)
		}
	}
	return finalResourceUpdates
}

func convertDependencyDeletesToReplacements(allResourceUpdates []ResourceUpdate, dependencyDeletes []ResourceUpdate, forma *pkgmodel.Forma, targetMap map[string]*pkgmodel.Target, source FormaCommandSource) []ResourceUpdate {
	var finalResourceUpdates []ResourceUpdate
	var remainingDependencyDeletes []ResourceUpdate

	// Create a map of Forma resources for quick lookup
	formaResourceMap := make(map[string]pkgmodel.Resource)
	for _, resource := range forma.Resources {
		key := fmt.Sprintf("%s|%s|%s", resource.Stack, resource.Label, resource.Type)
		formaResourceMap[key] = resource
	}

	for _, depDelete := range dependencyDeletes {
		key := fmt.Sprintf("%s|%s|%s", depDelete.DesiredState.Stack, depDelete.DesiredState.Label, depDelete.DesiredState.Type)
		// Check if this dependency delete resource is in the Forma
		if formaResource, isInFormaCommand := formaResourceMap[key]; isInFormaCommand {
			// Check if there's already an update or create operation for this resource
			hasUpdateOrCreate := false
			for _, update := range allResourceUpdates {
				if update.DesiredState.URI().Stripped() == depDelete.DesiredState.URI().Stripped() &&
					(update.Operation == OperationUpdate || update.Operation == OperationCreate) {
					hasUpdateOrCreate = true
					break
				}
			}

			if !hasUpdateOrCreate {
				// Convert dependency delete to replacement since the resource is in Forma
				// but has no update/create operation
				replaceUpdates, err := NewResourceUpdateForReplace(
					depDelete.DesiredState, // existing resource
					formaResource,          // new resource from forma
					depDelete.ResourceTarget,
					*targetMap[formaResource.Target],
					source,
				)
				if err != nil {
					slog.Error("Failed to create replacement updates for dependency delete",
						"resource", depDelete.DesiredState.Label,
						"error", err)
					remainingDependencyDeletes = append(remainingDependencyDeletes, depDelete)
					continue
				}

				finalResourceUpdates = append(finalResourceUpdates, replaceUpdates...)
				slog.Debug("Converted dependency delete to replacement",
					"resource", depDelete.DesiredState.Label,
					"reason", "resource is in Forma but has no update/create operation")
			} else {
				// Keep as dependency delete since there's already an update/create
				remainingDependencyDeletes = append(remainingDependencyDeletes, depDelete)
			}
		} else {
			// Keep as dependency delete since resource is not in Forma
			remainingDependencyDeletes = append(remainingDependencyDeletes, depDelete)
		}
	}

	return append(finalResourceUpdates, remainingDependencyDeletes...)
}

// assignKSUIDs looks for existing KSUIDs and if not found, generates new KSUIDs
func assignKSUIDs(resources []pkgmodel.Resource, ds ResourceDataLookup) ([]pkgmodel.Resource, map[string]string) {
	var tripletsToLookup []pkgmodel.TripletKey
	var needsLookupIndices []int

	for i, resource := range resources {
		if resource.Ksuid == "" {
			triplet := pkgmodel.TripletKey{
				Stack: resource.Stack,
				Label: resource.Label,
				Type:  resource.Type,
			}
			tripletsToLookup = append(tripletsToLookup, triplet)
			needsLookupIndices = append(needsLookupIndices, i)
		}
	}

	ksuidToLabel := make(map[string]string)
	if len(tripletsToLookup) == 0 {
		for _, resource := range resources {
			if resource.Ksuid != "" {
				ksuidToLabel[resource.Ksuid] = resource.Label
			}
		}
		return resources, ksuidToLabel
	}

	ksuidMap, err := ds.BatchGetKSUIDsByTriplets(tripletsToLookup)

	// If the batch get KSUIDs by triplets fails, generate new KSUIDs + ksuidToLabel mapping
	if err != nil {
		slog.Error("BatchGetKSUIDsByTriplets failed",
			"error", err,
			"triplets", tripletsToLookup)
		for _, idx := range needsLookupIndices {
			resources[idx].Ksuid = util.NewID()
		}
		for _, resource := range resources {
			if resource.Ksuid != "" {
				ksuidToLabel[resource.Ksuid] = resource.Label
			}
		}
		return resources, ksuidToLabel
	}

	for i, idx := range needsLookupIndices {
		triplet := tripletsToLookup[i]
		if existingKSUID, ok := ksuidMap[triplet]; ok {
			// Found by triplet in the target stack
			resources[idx].Ksuid = existingKSUID
		} else {
			// Not found in target stack - check if it exists in $unmanaged
			// This handles the case where we're bringing unmanaged resources under management
			unmanagedKSUID, err := ds.GetKSUIDByTriplet(
				constants.UnmanagedStack,
				triplet.Label,
				triplet.Type,
			)
			if err == nil && unmanagedKSUID != "" {
				// Found in $unmanaged - preserve that KSUID
				slog.Debug("Preserving KSUID from $unmanaged stack",
					"label", triplet.Label,
					"type", triplet.Type,
					"ksuid", unmanagedKSUID)
				resources[idx].Ksuid = unmanagedKSUID
				ksuidToLabel[unmanagedKSUID] = triplet.Label
			} else {
				// Truly doesn't exist! Generate new KSUID
				resources[idx].Ksuid = util.NewID()
			}
		}
	}

	for tripletKey, ksuid := range ksuidMap {
		ksuidToLabel[ksuid] = tripletKey.Label
	}

	for _, resource := range resources {
		if resource.Ksuid != "" {
			ksuidToLabel[resource.Ksuid] = resource.Label
		}
	}

	return resources, ksuidToLabel
}

// translateFormaeReferencesToKsuid translates resolvables values to KSUID refs
func translateFormaeReferencesToKsuid(forma *pkgmodel.Forma, ds ResourceDataLookup) (map[string]string, error) {
	resources, ksuidToLabel := assignKSUIDs(forma.Resources, ds)
	forma.Resources = resources

	tupleToKsuid := make(map[pkgmodel.TripletKey]string)
	for _, resource := range forma.Resources {
		tripletKey := pkgmodel.TripletKey{
			Stack: resource.Stack,
			Label: resource.Label,
			Type:  resource.Type,
		}
		tupleToKsuid[tripletKey] = resource.Ksuid
	}

	for i, resource := range forma.Resources {
		if resource.Properties != nil {
			translatedProperties, externalLabels, err := translatePropertiesJSON(resource.Properties, tupleToKsuid, ds)
			if err != nil {
				return nil, fmt.Errorf("failed to translate properties for resource %s: %w", resource.Label, err)
			}
			forma.Resources[i].Properties = translatedProperties
			maps.Copy(ksuidToLabel, externalLabels)
		}

		if resource.ReadOnlyProperties != nil {
			translatedReadOnlyProperties, externalLabels, err := translatePropertiesJSON(resource.ReadOnlyProperties, tupleToKsuid, ds)
			if err != nil {
				return nil, fmt.Errorf("failed to translate read-only properties for resource %s: %w", resource.Label, err)
			}
			forma.Resources[i].ReadOnlyProperties = translatedReadOnlyProperties
			maps.Copy(ksuidToLabel, externalLabels)
		}
	}

	return ksuidToLabel, nil
}

// translatePropertiesJSON translates all resolvable objects to KSUID URIs
func translatePropertiesJSON(properties json.RawMessage, tripletToKsuid map[pkgmodel.TripletKey]string, ds ResourceDataLookup) (json.RawMessage, map[string]string, error) {
	result, externalLabels, resolvables := string(properties), make(map[string]string), pkgmodel.FindResolvablesFromProperties(string(properties))
	var (
		err       error
		formaeURI pkgmodel.FormaeURI
	)

	for _, resolvable := range resolvables {
		ksuid, ok := tripletToKsuid[resolvable.ToTripletKey()]
		if ok {
			formaeURI = resolvable.ToFormaeURI(ksuid)
		} else {
			// Look up the KSUID directly from the datastore
			if resolvable.Label == "" || resolvable.Type == "" || resolvable.Stack == "" {
				slog.Warn("Resolvable object missing required fields",
					"path", resolvable.Path,
					"label", resolvable.Label,
					"type", resolvable.Type,
					"stack", resolvable.Stack)
				continue
			}

			ksuid, err = ds.GetKSUIDByTriplet(resolvable.Stack, resolvable.Label, resolvable.Type)
			if err != nil || ksuid == "" {
				// Fallback: This handles the case where we're bringing unmanaged resources under management
				// and the resolvable points to the target stack but the resource still exists in $unmanaged
				ksuid, err = ds.GetKSUIDByTriplet(constants.UnmanagedStack, resolvable.Label, resolvable.Type)
				if err != nil {
					slog.Warn("Failed to get KSUID for triplet (including $unmanaged fallback)",
						"path", resolvable.Path,
						"stack", resolvable.Stack,
						"label", resolvable.Label,
						"type", resolvable.Type,
						"error", err)
					continue
				}
				if ksuid == "" {
					slog.Warn("Resource not found for triplet (including $unmanaged fallback)",
						"path", resolvable.Path,
						"stack", resolvable.Stack,
						"label", resolvable.Label,
						"type", resolvable.Type)
					continue
				}
			}
			formaeURI = resolvable.ToFormaeURI(ksuid)
		}
		refObject := map[string]string{
			"$ref": string(formaeURI),
		}

		result, err = sjson.Set(result, resolvable.Path, refObject)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to replace resolvable object at path %s: %w", resolvable.Path, err)
		}

		if resolvable.Label != "" {
			externalLabels[formaeURI.KSUID()] = resolvable.Label
		}
	}

	return json.RawMessage(result), externalLabels, nil
}
