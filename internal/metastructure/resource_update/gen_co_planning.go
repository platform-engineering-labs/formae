// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// CoPlanGeneratorDestinations plans the resources named by ksuids even though
// the ordinary planning pass found nothing about them to do.
//
// It is the second half of the draw obligation. Once a generator is going to
// draw, every one of its live destinations that this forma declares has to be
// a node in the changeset, because delivery reaches nodes and nothing else and
// formae keeps only a hash of a drawn value. A destination the command skipped
// would keep the generation it already holds while its siblings moved to the
// new one, and no later apply could level them: the only way to give the
// laggard a value is to draw again, which puts the others behind.
//
// The caller decides who is owed a plan; this decides nothing. It is handed
// resource KSUIDs and plans exactly those, through the same
// NewResourceUpdateForExisting the ordinary pass uses, so provenance
// classification, the stable-binding carry-forward and the freeze at the
// provider boundary all still apply to them. The only difference is the
// CoPlannedForDraw marker, which stops the "nothing moved" suppressions
// dropping the update on the floor.
//
// THE FEEDBACK LOOP THIS MUST NOT CLOSE. The set of drawing generators is
// derived from the planned updates (GeneratorsNeedingDraw), and this adds
// updates. If the caller re-derived that set over the widened plan, a
// co-planned resource's OTHER bindings — to generators that were stable and
// had no reason to draw — could be read as needing a draw, and every apply
// would rotate one more credential than the last. The caller computes the
// drawing set ONCE, from the ordinary pass, and never recomputes it over what
// this returns. Nothing here reads the drawing set, so this file cannot close
// that loop on its own; metastructure.FormaCommandFromForma is where the
// ordering is kept.
//
// A KSUID with no live row, or none the forma declares, is skipped: this plans
// against a declaration and an existing row, and without both there is nothing
// to pair. Reaching such a destination is not this pass's problem — it is
// exactly what the unreachable-destination refusal is for.
//
// mode is passed through rather than assumed: reconcile and patch build the
// same three views of the forma and hand NewResourceUpdateForExisting the same
// arguments, so co-planning is the same work under either. Its one caller is
// the apply path, which is the only command that draws — a destroy writes no
// property, and sync and discovery carry no authored forma to plan from.
func CoPlanGeneratorDestinations(
	forma *pkgmodel.Forma,
	ksuids map[string]bool,
	mode pkgmodel.FormaApplyMode,
	source FormaCommandSource,
	existingTargets []*pkgmodel.Target,
	ds ResourceDataLookup,
	force bool,
) ([]ResourceUpdate, error) {
	if len(ksuids) == 0 {
		return nil, nil
	}

	allResourcesByStack, err := ds.LoadAllResourcesByStack()
	if err != nil {
		return nil, fmt.Errorf("failed to load existing stacks: %w", err)
	}
	existingByKsuid := make(map[string]*pkgmodel.Resource)
	for _, resources := range allResourcesByStack {
		for _, existing := range resources {
			if ksuids[existing.Ksuid] {
				existingByKsuid[existing.Ksuid] = existing
			}
		}
	}
	if len(existingByKsuid) == 0 {
		return nil, nil
	}

	// The same three views of the forma the reconcile pass builds, so a
	// co-planned resource's desired document is derived exactly as it would
	// have been had something about it moved.
	resolvableLookup := resourcesForResolvables(forma, allResourcesByStack)
	effectiveDesired, err := ComputeEffectiveDesired(forma, allResourcesByStack)
	if err != nil {
		return nil, fmt.Errorf("failed to compute effective desired state: %w", err)
	}
	generatorGenerationLookup, err := generatorGenerations(forma, ds)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve generator generations: %w", err)
	}
	existingTargetMap, desiredTargetMap := buildTargetMaps(forma, existingTargets)

	var coPlanned []ResourceUpdate
	for _, newResource := range forma.Resources {
		existing, ok := existingByKsuid[newResource.Ksuid]
		if !ok {
			continue
		}
		existingTarget, ok := existingTargetMap[existing.Target]
		if !ok {
			return nil, fmt.Errorf("failed to co-plan resource %s: its target %s is unknown",
				existing.Label, existing.Target)
		}
		desiredTarget, ok := desiredTargetMap[newResource.Target]
		if !ok {
			return nil, fmt.Errorf("failed to co-plan resource %s: its target %s is unknown",
				newResource.Label, newResource.Target)
		}

		readOnlyProperties, err := resolver.LoadResolvablePropertiesFromStacks(
			newResource, resolvableLookup, effectiveDesired, generatorGenerationLookup)
		if err != nil {
			return nil, fmt.Errorf("failed to load resolvable properties for %s: %w", newResource.Label, err)
		}

		updates, err := NewResourceUpdateForExisting(
			readOnlyProperties,
			effectiveDesired[existing.Ksuid],
			*existing,
			newResource,
			*existingTarget,
			*desiredTarget,
			mode,
			source,
			force,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to co-plan resource %s: %w", newResource.Label, err)
		}
		coPlanned = append(coPlanned, updates...)
	}

	return coPlanned, nil
}
