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
	"github.com/tidwall/gjson"
)

func NewResourceUpdateForExisting(
	resolvableProperties resolver.ResolvableProperties,
	effectiveDesired json.RawMessage,
	existingResource pkgmodel.Resource,
	newResource pkgmodel.Resource,
	existingTarget pkgmodel.Target,
	newTarget pkgmodel.Target,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
	force bool,
	coPlanned CoPlannedForDraw,
) ([]ResourceUpdate, error) {

	// filteredProps must exist before the identical-resources check below, so
	// that check can compute the ownership claim from the effective desired
	// properties rather than from newResource.Properties directly (they
	// legitimately differ under setOnce).
	var err error
	filteredProps := effectiveDesired
	if filteredProps == nil {
		filteredProps, err = filterSetOnceProps(existingResource.Properties, newResource.Properties, newResource.Label)
		if err != nil {
			return nil, fmt.Errorf("failed to compute effective desired properties: %w", err)
		}
	}

	// claimedMembers/ownershipDelta run BEFORE either early-return below: a
	// co-owned collection's live membership can shift under this forma even
	// when nothing else about the resource moved (another writer added or
	// removed a member this forma also declares), and that is exactly the
	// legacy-bootstrap case (no record yet, existing==desired byte-for-byte)
	// the identical-resources return would otherwise swallow.
	claim := claimedMembers(filteredProps, existingResource.Properties, newResource.Schema)
	ownershipDelta := !pkgmodel.OwnedMembersEqual(
		pkgmodel.NormalizeOwnedMembers(claim, newResource.Schema.Hints),
		pkgmodel.NormalizeOwnedMembers(existingResource.OwnedMembers, newResource.Schema.Hints))

	// The three suppressions below all say the same thing: nothing about this
	// resource moved, so the command has nothing to do for it. coPlanned is
	// one case where that is true and the update is still owed — a
	// generator this resource binds to draws in this command, and only a
	// resource that is a node in the changeset receives the drawn value (see
	// CoPlannedForDraw). ownershipDelta is the other: the record itself needs
	// writing even though nothing else changed.
	if reflect.DeepEqual(existingResource, newResource) && !bool(coPlanned) && !ownershipDelta {
		slog.Debug("No changes detected between existing and new resource, skipping update")
		return nil, nil
	}

	// Check if resources have same label and type. Labels may
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

	hasChanges, err := CompareFilteredResourceForUpdate(&existingResource, &newResource, newResource.Schema, filteredProps)
	if err != nil {
		return nil, fmt.Errorf("failed to compare resources: %w", err)
	}
	if !hasChanges && !stackChanged && !labelChanged && !bool(coPlanned) && !ownershipDelta {
		return []ResourceUpdate{}, nil
	}

	// A record-only update: no property, stack, label, or draw change — the
	// sole reason this update exists is that the ownership record itself
	// needs writing. Built through the same construction path as any other
	// update below, then overridden just before it is returned. Also set
	// further down when hasChanges is true but the patch it produced
	// suppresses to nothing — see the comment there.
	recordOnly := ownershipDelta && !hasChanges && !stackChanged && !labelChanged && !bool(coPlanned)

	var patchDocument json.RawMessage
	var createOnlyPatch json.RawMessage
	var onlyForceResent bool

	// Provenance classification runs BEFORE any opaque suppression or patch
	// generation: it decides per reference occurrence whether the source
	// provably moved, marks stable occurrences for suppression at the
	// flattening seam, and produces the immutable records regeneration reads
	// back after recovery.
	provenanceRecords := buildProvenanceRecords(filteredProps, existingResource.Properties, resolvableProperties, newResource.Schema, force)

	if hasChanges {
		// Drop opaque values that are unchanged from what is stored so a sibling
		// edit does not churn (re-emit cleartext) or corrupt (re-emit the stored
		// hash, for setOnce) the opaque field. Genuinely changed opaque values
		// stay, so rotation still produces a patch op. filteredProps is left
		// untouched for DesiredState.Properties below — only the patch inputs are
		// stripped.
		existingForPatch, desiredForPatch, err := SuppressUnchangedOpaqueValues(existingResource.Properties, filteredProps, newResource.Schema, newResource.Type)
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

		patchDocument, createOnlyPatch, onlyForceResent, err = patch.GeneratePatch(
			existingPluginProps,
			newPluginProps,
			existingForPatch,
			desiredForPatch,
			resolvableProperties,
			newResource.Schema,
			existingResource.OwnedMembers,
			mode,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create patch document for resource %s: %w", existingResource.Label, err)
		}

		// A patch that is empty, or whose only ops are requiredOnUpdate fields
		// force-resent to guarantee the provider sees them (no op reflecting an
		// actual change), carries nothing for the plugin to do. Planning is the
		// one place that decision is allowed to drop the update outright —
		// regeneratePatchDocument at execution time must never do the same, or
		// an in-flight update would lose the force-resent field from its payload.
		if (patchDocument == nil || onlyForceResent) && len(createOnlyPatch) == 0 && !stackChanged && !labelChanged && !bool(coPlanned) {
			if !ownershipDelta {
				return []ResourceUpdate{}, nil
			}
			// The patch suppressed to nothing (or purely force-resent), so the
			// plugin has nothing to do — but a co-owned collection's membership
			// still shifted under this forma. Route to a record-only update
			// instead of dropping it: the override block below (recordOnly)
			// turns this into the same empty-patch, claim-only update the two
			// simpler suppressions above produce.
			recordOnly = true
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

	// Runs after the patch is derived, so what the provider is asked to do is
	// unaffected: a stable occurrence is already suppressed from the diff.
	// This is only about what the row keeps, which is written from the desired
	// document below.
	filteredProps, generatorDigests, err := CarryStableGeneratorBindingForward(
		filteredProps, existingResource.Properties, provenanceRecords)
	if err != nil {
		return nil, fmt.Errorf("failed to carry stable generator bindings forward for resource %s: %w", existingResource.Label, err)
	}

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
			// Carried forward on every update; a real update's echo recompute
			// (updateResourceUpdateFromProgress) replaces this once the
			// provider's write lands, so this is a placeholder for anything
			// that reads DesiredState before then. A record-only update
			// overrides it below with the freshly computed claim.
			OwnedMembers: existingResource.OwnedMembers,
			// Preserve the existing row's KSUID across the update. The caller
			// has already paired `existingResource` (current managed row) with
			// `newResource` (desired declaration); the existing KSUID is the
			// authoritative identity. Using `newResource.Ksuid` here would let
			// a stale or freshly-minted KSUID from the upstream `assignKSUIDs`
			// pass through, which causes a rename to write a second
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
		ProvenanceRecords:    provenanceRecords,
		ResolvedRootDigests:  generatorDigests,
	}

	if recordOnly {
		updateResource.DesiredState.PatchDocument = json.RawMessage(`[]`)
		updateResource.DesiredState.Properties = existingResource.Properties
		updateResource.DesiredState.OwnedMembers = claim
		updateResource.RecordOnly = true
		updateResource.RemainingResolvables = nil
	}

	return []ResourceUpdate{updateResource}, nil
}

// claimedMembers computes, per CoOwned-hinted field, the ownership record
// this forma may claim right now: declared member identities (from declared)
// intersected with what the provider actually shows (from live). A member
// this forma declares but the provider does not show — or the reverse — is
// not claimable yet, so only the intersection earns a record entry. Identity
// extraction is envelope-flattened (see MemberIdentities): an unresolved
// $ref/$res/$gen reference contributes nothing to either side. A field whose
// intersection is empty gets no entry at all.
func claimedMembers(declared, live json.RawMessage, schema pkgmodel.Schema) pkgmodel.OwnedMembers {
	var claim pkgmodel.OwnedMembers
	for path, hint := range schema.Hints {
		if hint.CoOwned == nil {
			continue
		}
		// Identities are names; an opaque field's "members" are its secret
		// values. Recording them here would persist a secret unhashed,
		// bypassing the opaque-hashing transformer entirely. The PKL schema
		// validation rejects this combination at authoring time (see
		// formae.pkl's FieldHint.validated()), but this is the defensive
		// backstop for a schema that reaches here some other way — no claim
		// is recorded for such a field until a hashing design exists.
		if hint.Opaque {
			continue
		}

		declaredMembers := pkgmodel.MemberIdentities(gjson.GetBytes(declared, path), hint)
		liveMembers := pkgmodel.MemberIdentities(gjson.GetBytes(live, path), hint)
		claimed := intersectIdentities(declaredMembers, liveMembers)
		if len(claimed) == 0 {
			continue
		}

		if claim == nil {
			claim = pkgmodel.OwnedMembers{}
		}
		claim[path] = pkgmodel.OwnedPathRecord{Rule: pkgmodel.IdentityRule(hint), Members: claimed}
	}
	return claim
}

// intersectIdentities returns the members of a that also appear in b. Both
// inputs are the sorted, deduplicated output of MemberIdentities, so the
// result is sorted and deduplicated too.
func intersectIdentities(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	inB := make(map[string]struct{}, len(b))
	for _, m := range b {
		inB[m] = struct{}{}
	}
	var out []string
	for _, m := range a {
		if _, ok := inB[m]; ok {
			out = append(out, m)
		}
	}
	return out
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
	// field already hashed at rest (the steady state once a secret is hashed) must not be rejected
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
