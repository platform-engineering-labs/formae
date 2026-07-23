// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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

	// Check if resources have same label and type. RFC-0041: labels may
	// legitimately differ when the new resource declares an `alias` that
	// names the existing resource's label — this is the rename path. Any
	// other label mismatch is a generator bug (the caller should not have
	// paired an existing row with an unrelated desired row).
	labelChanged := existingResource.Label != newResource.Label
	if labelChanged && newResource.Alias != existingResource.Label {
		return nil, fmt.Errorf("resource labels don't match: %s vs %s", existingResource.Label, newResource.Label)
	}

	// Check if we're bringing an unmanaged resource under management
	// This check needs to happen before hasChanges, because for tag-less resources
	// the properties might be identical
	stackChanged := existingResource.Stack != newResource.Stack

	hasChanges, filteredProps, err := EnforceSetOnceAndCompareResourceForUpdate(&existingResource, &newResource, newResource.Schema)
	if err != nil {
		return nil, fmt.Errorf("failed to compare resources: %w", err)
	}
	if !hasChanges && !stackChanged && !labelChanged {
		return []ResourceUpdate{}, nil
	}

	var patchDocument json.RawMessage
	var createOnlyPatch json.RawMessage

	if hasChanges {
		// Drop opaque values that are unchanged from what is stored so a sibling
		// edit does not churn (re-emit cleartext) or corrupt (re-emit the stored
		// hash, for setOnce) the opaque field. Genuinely changed opaque values
		// stay, so rotation still produces a patch op. filteredProps is left
		// untouched for DesiredState.Properties below — only the patch inputs are
		// stripped.
		existingForPatch, desiredForPatch, err := SuppressUnchangedOpaqueValues(existingResource.Properties, filteredProps, newResource.Schema)
		if err != nil {
			return nil, fmt.Errorf("failed to suppress unchanged opaque values for resource %s: %w", existingResource.Label, err)
		}

		// Read-safe/comparison conversion: existingForPatch is only used as the "before"
		// side of a local diff (patch.GeneratePatch below), never transmitted to a plugin.
		// A genuinely-rotated opaque field's existing side is a stored hash that can never
		// be un-hashed back to plaintext — that must not block patch generation; the
		// resulting patch document carries the desired (live plaintext) value, not this one.
		existingPluginProps, err := resolver.ConvertExistingStateForComparison(existingForPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to convert existing properties to plugin format: %w", err)
		}

		newPluginProps, err := resolver.ConvertToPluginFormat(desiredForPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to convert new properties to plugin format: %w", err)
		}

		patchDocument, createOnlyPatch, err = patch.GeneratePatch(
			existingPluginProps,
			newPluginProps,
			resolvableProperties,
			newResource.Schema,
			mode,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create patch document for resource %s: %w", existingResource.Label, err)
		}

		if patchDocument == nil && len(createOnlyPatch) == 0 && !stackChanged && !labelChanged {
			return []ResourceUpdate{}, nil
		}
	} else {
		patchDocument = json.RawMessage(`[]`)
		filteredProps = existingResource.Properties
	}

	if len(createOnlyPatch) > 0 {
		// CreateOnly fields changed — plan a destroy+create instead of an update.
		replaceUpdates, err := NewResourceUpdateForReplace(existingResource, newResource, existingTarget, newTarget, source, createOnlyPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to create replacement updates for resource %s: %w", existingResource.Label, err)
		}
		return replaceUpdates, nil
	}

	// Extract resolvables for the new resource
	newRemainingResolvables := resolver.ExtractResolvableURIs(newResource)

	updateResource := ResourceUpdate{
		PriorState:     existingResource,
		ExistingTarget: existingTarget,
		DesiredState: pkgmodel.Resource{
			Label:              newResource.Label,
			Type:               newResource.Type,
			Stack:              newResource.Stack,
			Schema:             newResource.Schema,
			Properties:         filteredProps,
			PatchDocument:      patchDocument,
			Target:             newResource.Target,
			NativeID:           existingResource.NativeID,
			ReadOnlyProperties: existingResource.ReadOnlyProperties,
			Managed:            newResource.Managed,
			// Preserve the existing row's KSUID across the update. The caller
			// has already paired `existingResource` (current managed row) with
			// `newResource` (desired declaration); the existing KSUID is the
			// authoritative identity. Using `newResource.Ksuid` here would let
			// a stale or freshly-minted KSUID from the upstream `assignKSUIDs`
			// pass through, which (RFC-0041) causes a rename to write a second
			// row with the same NativeID under a new KSUID.
			Ksuid: existingResource.Ksuid,
		},
		ResourceTarget:       newTarget,
		Operation:            OperationUpdate,
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
	createOnlyPatch json.RawMessage,
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
	deleteUpdate.CreateOnlyPatch = createOnlyPatch

	// Create create operation for new resource
	createUpdate := ResourceUpdate{
		DesiredState:         newResource,
		ResourceTarget:       newTarget,
		Operation:            OperationCreate,
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

	return ResourceUpdate{
		PriorState:           existingResource, // We will always run here on the existing resource for deletion
		DesiredState:         existingResource,
		ResourceTarget:       target,
		Operation:            OperationDelete,
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
		DesiredState:         newResource,
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
	return NewResourceUpdateForSyncWithFilter(existingResource, target, source)
}

func NewResourceUpdateForSyncWithFilter(
	existingResource pkgmodel.Resource,
	target pkgmodel.Target,
	source FormaCommandSource,
) (ResourceUpdate, error) {

	// need to copy the existing resource to avoid modifying it directly
	existingResourceCopy := existingResource

	// Read-safe conversion: this ResourceUpdate only ever drives a Read (via the
	// ResourceUpdater's synchronize state), never a Create/Update, so a schema-opaque
	// field already hashed at rest (the steady state after PLA-320) must not be rejected
	// here — nothing gets written to the cloud from PriorState/PreviousProperties.
	resolvedExistingProperties, err := resolver.ConvertExistingStateForRead(existingResource.Properties)
	if err != nil {
		return ResourceUpdate{}, fmt.Errorf("failed to resolve dynamic properties for existing resource %s: %w", existingResource.Label, err)
	}

	existingResource.Properties = resolvedExistingProperties

	return ResourceUpdate{
		PriorState:     existingResource,
		ExistingTarget: target,
		DesiredState:   existingResourceCopy,
		ResourceTarget: target,
		Operation:      OperationRead,
		State:          ResourceUpdateStateNotStarted,
		Source:         source,
		StackLabel:     existingResource.Stack,
		// PreviousProperties must keep the ORIGINAL stored copy (existingResourceCopy), not the
		// Read-context-stripped existingResource.Properties: a schema-opaque field hashed at
		// rest carries a {$value,$visibility,$hashed:true} envelope, and
		// ConvertExistingStateForRead strips that down to a bare digest for Read-context use
		// only. If that stripped bare digest were persisted here as PreviousProperties, it would
		// lose its $hashed marker and get treated as plaintext — and re-hashed (hash-of-hash) on
		// the next boot backfill.
		PreviousProperties: existingResourceCopy.Properties,
	}, nil
}
