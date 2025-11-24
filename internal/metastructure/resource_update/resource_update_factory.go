// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func NewResourceUpdateForExisting(
	resolvableProperties resolver.ResolvableProperties,
	existingResource pkgmodel.Resource,
	newResource pkgmodel.Resource,
	existingTarget pkgmodel.Target,
	newTarget pkgmodel.Target,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
) ([]ResourceUpdate, error) {

	if reflect.DeepEqual(existingResource, newResource) {
		slog.Debug("No changes detected between existing and new resource, skipping update")
		return nil, nil
	}

	// Check if resources have same label and type
	if existingResource.Label != newResource.Label {
		return nil, fmt.Errorf("resource labels don't match: %s vs %s", existingResource.Label, newResource.Label)
	}

	// Check if we're bringing an unmanaged resource under management
	// This check needs to happen before hasChanges, because for tag-less resources
	// the properties might be identical
	isBringingUnderManagement := existingResource.Stack == constants.UnmanagedStack &&
		newResource.Stack != constants.UnmanagedStack

	hasChanges, filteredProps, err := EnforceSetOnceAndCompareResourceForUpdate(&existingResource, &newResource)
	if err != nil {
		return nil, fmt.Errorf("failed to compare resources: %w", err)
	}
	if !hasChanges && !isBringingUnderManagement {
		return []ResourceUpdate{}, nil
	}

	existingPluginProps, err := resolver.ConvertToPluginFormat(existingResource.Properties)
	if err != nil {
		return nil, fmt.Errorf("failed to convert existing properties to plugin format: %w", err)
	}

	newPluginProps, err := resolver.ConvertToPluginFormat(filteredProps)
	if err != nil {
		return nil, fmt.Errorf("failed to convert new properties to plugin format: %w", err)
	}

	// Generate patch document
	patchDocument, needsReplacement, err := patch.GeneratePatch(
		existingPluginProps,
		newPluginProps,
		resolvableProperties,
		existingResource.Schema.Fields,
		existingResource.Schema.CreateOnly(),
		mode,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch document for resource %s: %w", existingResource.Label, err)
	}

	if patchDocument == nil {
		// Special case: bringing tag-less resources under management
		// For resources that don't support tags (e.g., AWS::EC2::VPCGatewayAttachment),
		// the patch will be empty even though we need to mark the resource as managed
		// and move it to a new stack.
		if isBringingUnderManagement {
			slog.Debug("Bringing unmanaged resource under management with no property changes",
				"resource", existingResource.Label,
				"type", existingResource.Type,
				"oldStack", existingResource.Stack,
				"newStack", newResource.Stack)
			// Continue with the update - we'll create an empty patch document
			// The resource updater will handle this specially to avoid calling the plugin
			patchDocument = json.RawMessage("[]")
		} else {
			// No changes needed
			return []ResourceUpdate{}, nil
		}
	}

	if needsReplacement {
		// Replacement required
		replaceUpdates, err := NewResourceUpdateForReplace(existingResource, newResource, existingTarget, newTarget, source)
		if err != nil {
			return nil, fmt.Errorf("failed to create replacement updates for resource %s: %w", existingResource.Label, err)
		}
		return replaceUpdates, nil
	}

	// Extract resolvables for the new resource
	newRemainingResolvables := resolver.ExtractResolvableURIs(newResource)

	existingMetadata, err := existingResource.GetMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata for existing resource %s: %w", existingResource.Label, err)
	}

	updateResource := ResourceUpdate{
		ExistingResource: existingResource,
		ExistingTarget:   existingTarget,
		Resource: pkgmodel.Resource{
			Label:         newResource.Label,
			Type:          newResource.Type,
			Stack:         newResource.Stack,
			Schema:        newResource.Schema,
			Properties:    filteredProps,
			PatchDocument: patchDocument,
			Target:        newResource.Target,
			NativeID:      existingResource.NativeID, // Ensure existing NativeID is set for updates
			Managed:       newResource.Managed,
			Ksuid:         newResource.Ksuid,
		},
		ResourceTarget:       newTarget,
		Operation:            OperationUpdate,
		MetaData:             existingMetadata,
		State:                ResourceUpdateStateNotStarted,
		Source:               source,
		StackLabel:           newResource.Stack,
		RemainingResolvables: newRemainingResolvables,
		PreviousProperties:   existingResource.Properties,
	}

	return []ResourceUpdate{updateResource}, nil
}

func NewResourceUpdateForReplace(
	existingResource pkgmodel.Resource,
	newResource pkgmodel.Resource,
	existingTarget pkgmodel.Target,
	newTarget pkgmodel.Target,
	source FormaCommandSource,
) ([]ResourceUpdate, error) {
	GroupID := util.NewID()

	// Extract resolvables from new resource for create operation
	newRemainingResolvables := resolver.ExtractResolvableURIs(newResource)

	deleteUpdate, err := NewResourceUpdateForDestroy(
		existingResource,
		existingTarget,
		source,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create delete update (replace) for resource %s: %w", existingResource.Label, err)
	}

	deleteUpdate.GroupID = GroupID

	newMetadata, err := newResource.GetMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata for new resource %s: %w", existingResource.Label, err)
	}

	// Create create operation for new resource
	createUpdate := ResourceUpdate{
		Resource:             newResource,
		ResourceTarget:       newTarget,
		Operation:            OperationCreate,
		MetaData:             newMetadata,
		State:                ResourceUpdateStateNotStarted,
		Source:               source,
		GroupID:              GroupID,
		StackLabel:           newResource.Stack,
		RemainingResolvables: newRemainingResolvables,
	}

	// Return delete first, then create (order matters for replacement)
	return []ResourceUpdate{deleteUpdate, createUpdate}, nil
}

func NewResourceUpdateForDestroy(
	existingResource pkgmodel.Resource,
	target pkgmodel.Target,
	source FormaCommandSource,
) (ResourceUpdate, error) {

	remainingResolvables := resolver.ExtractResolvableURIs(existingResource)

	existingMetadata, err := existingResource.GetMetadata()
	if err != nil {
		return ResourceUpdate{}, fmt.Errorf("failed to get metadata for new existing %s: %w", existingResource.Label, err)
	}
	return ResourceUpdate{
		ExistingResource:     existingResource, // We will always run here on the existing resource for deletion
		Resource:             existingResource,
		ResourceTarget:       target,
		Operation:            OperationDelete,
		MetaData:             existingMetadata,
		State:                ResourceUpdateStateNotStarted,
		Source:               source,
		StackLabel:           existingResource.Stack,
		RemainingResolvables: remainingResolvables,
	}, nil
}

func NewResourceUpdateForCreate(
	newResource pkgmodel.Resource,
	target pkgmodel.Target,
	source FormaCommandSource,
) (ResourceUpdate, error) {

	if err := newResource.ValidateRequiredOnCreateFields(); err != nil {
		missingFields := newResource.GetMissingRequiredOnCreateFields()
		return ResourceUpdate{}, apimodel.RequiredFieldMissingOnCreateError{
			MissingFields: missingFields,
			Stack:         newResource.Stack,
			Label:         newResource.Label,
			Type:          newResource.Type,
		}
	}

	return ResourceUpdate{
		Resource:             newResource,
		ResourceTarget:       target,
		Operation:            OperationCreate,
		State:                ResourceUpdateStateNotStarted,
		Source:               source,
		StackLabel:           newResource.Stack,
		RemainingResolvables: resolver.ExtractResolvableURIs(newResource),
	}, nil
}

func NewResourceUpdateForSync(
	existingResource pkgmodel.Resource,
	target pkgmodel.Target,
	source FormaCommandSource,
) (ResourceUpdate, error) {
	return NewResourceUpdateForSyncWithFilter(existingResource, target, source, nil)
}

func NewResourceUpdateForSyncWithFilter(
	existingResource pkgmodel.Resource,
	target pkgmodel.Target,
	source FormaCommandSource,
	filter plugin.ResourceFilter,
) (ResourceUpdate, error) {

	// need to copy the existing resource to avoid modifying it directly
	existingResourceCopy := existingResource

	resolvedExistingProperties, err := resolver.ConvertToPluginFormat(existingResource.Properties)
	if err != nil {
		return ResourceUpdate{}, fmt.Errorf("failed to resolve dynamic properties for existing resource %s: %w", existingResource.Label, err)
	}

	existingResource.Properties = resolvedExistingProperties
	existingMetadata, err := existingResource.GetMetadata()
	if err != nil {
		return ResourceUpdate{}, fmt.Errorf("failed to get metadata for new existing %s: %w", existingResource.Label, err)
	}

	if filter == nil {
		filter = func(json.RawMessage, pkgmodel.Target) bool {
			return false
		}
	}

	return ResourceUpdate{
		ExistingResource:   existingResource,
		ExistingTarget:     target,
		Resource:           existingResourceCopy,
		ResourceTarget:     target,
		Operation:          OperationRead,
		MetaData:           existingMetadata,
		State:              ResourceUpdateStateNotStarted,
		Source:             source,
		StackLabel:         existingResource.Stack,
		PreviousProperties: existingResource.Properties,
		Filter:             filter,
	}, nil
}
