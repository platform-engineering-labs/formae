// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// SynthesizeResolveTargetUpdates builds synthetic Resolve target ops for targets
// that a command references but that are NOT already covered by a real
// Create/Update/Replace/Delete target update in this command.
//
// An unchanged target may still hold opaque $ref config (e.g. a secret). Without a
// real target update carrying resolved desired config, that config would be
// dispatched to the plugin unresolved. For each such distinct referenced target
// label, the PERSISTED target row is loaded (a target with a real update already
// carries its desired config, so it is never synthesized for), its config is
// scanned for resolvable $refs using the same extractor that populates a target
// update's RemainingResolvables, and — if any exist — a NewResolveTargetUpdate is
// appended.
//
// It then rejects a credential-from-credential chain: a target whose config
// opaquely $refs a secret whose OWN target also carries opaque $ref config cannot
// be resolved, because bootstrapping its credential would first require
// bootstrapping another.
//
// referencedLabels are the distinct target labels a resource op referenced (see
// resource_update.ReferencedTargetLabels); sourceTargetByKsuid maps an in-command
// resource KSUID to its target label (see resource_update.SourceTargetByKsuid), so
// a secret source being created in this same command resolves to its target
// without a persisted row. Passing these plain inputs keeps this package free of a
// resource_update import.
//
// A nil datastore (unit tests that never reference persisted targets) or a target
// that is not found is treated as "nothing to synthesize" and skipped.
func SynthesizeResolveTargetUpdates(
	referencedLabels []string,
	sourceTargetByKsuid map[string]string,
	existing []TargetUpdate,
	ds TargetDatastore,
) ([]TargetUpdate, error) {
	// A target is covered (needs no synthetic Resolve) only when an update already
	// carries re-resolved config:
	//   - Create/Update/Replace/Resolve carry freshly re-resolved config, so they cover.
	//   - A plain Delete does NOT carry resolved config, yet resource ops tearing down
	//     on that target still need the current secret value, so it stays uncovered.
	//   - A cascade Delete tears down its config's $ref source in the same command. For
	//     a plain cross-resource $ref that is fine (the delete works off the stored
	//     snapshot / NativeID), so it stays covered. But when the cascade target carries
	//     an OPAQUE $ref (a secret the plugin needs to authenticate teardown) it is
	//     un-covered: the changeset orders the secret delete AFTER the target delete, so
	//     the secret is still present at Resolve time, and only its opaque refs resolve.
	covered := make(map[string]bool)
	cascadeOpaqueUncovered := make(map[string]bool)
	for i := range existing {
		switch existing[i].Operation {
		case TargetOperationCreate,
			TargetOperationUpdate,
			TargetOperationReplace,
			TargetOperationResolve:
			covered[existing[i].Target.Label] = true
		case TargetOperationDelete:
			if existing[i].IsCascade {
				var hasOpaque bool
				if existing[i].ExistingTarget != nil && ds != nil {
					opaqueURIs, err := resolver.ExtractSourceOpaqueResolvableURIsFromJSON(existing[i].ExistingTarget.Config, ds.LoadResourceById)
					if err != nil {
						return nil, fmt.Errorf("detect opaque refs for cascade target %q: %w", existing[i].Target.Label, err)
					}
					hasOpaque = len(opaqueURIs) > 0
				}
				if !hasOpaque {
					covered[existing[i].Target.Label] = true
				} else {
					cascadeOpaqueUncovered[existing[i].Target.Label] = true
				}
			}
		}
	}

	var candidates []string
	for _, label := range referencedLabels {
		if label == "" || covered[label] {
			continue
		}
		candidates = append(candidates, label)
	}

	if len(candidates) == 0 || ds == nil {
		return nil, nil
	}

	var synthetic []TargetUpdate
	for _, label := range candidates {
		persisted, err := ds.LoadTarget(label)
		if err != nil {
			return nil, fmt.Errorf("synthesize resolve target %q: %w", label, err)
		}
		if persisted == nil {
			continue
		}
		// For a cascade-opaque uncovered target, resolve ONLY its opaque $refs: its
		// non-opaque cross-resource $refs point at sources being deleted in this same
		// command, so reading a vanishing source would fail. On the delete path those
		// non-opaque refs carry their stored value via NativeID and need no resolution.
		var resolvables []pkgmodel.FormaeURI
		opaqueOnly := cascadeOpaqueUncovered[label]
		if opaqueOnly {
			resolvables, err = resolver.ExtractSourceOpaqueResolvableURIsFromJSON(persisted.Config, ds.LoadResourceById)
			if err != nil {
				return nil, fmt.Errorf("extract opaque resolvables for cascade target %q: %w", label, err)
			}
		} else {
			resolvables = resolver.ExtractResolvableURIsFromJSON(persisted.Config)
		}
		if len(resolvables) == 0 {
			continue
		}
		resolveTU := NewResolveTargetUpdate(*persisted, resolvables)
		// Preserve the opaque-only selection so execute-time revalidation keeps
		// excluding vanishing non-opaque refs if the config advances under us.
		resolveTU.OpaqueOnly = opaqueOnly
		synthetic = append(synthetic, resolveTU)
	}

	if err := rejectTransitivelyOpaqueTargets(append(existing, synthetic...), sourceTargetByKsuid, ds); err != nil {
		return nil, err
	}

	return synthetic, nil
}

// rejectTransitivelyOpaqueTargets rejects a target whose config opaquely $refs a
// secret whose source resource lives on a target that ITSELF carries opaque $ref
// config. Resolving such a target would require first bootstrapping a credential
// from another credential that itself needs resolving — a chain that has no clear
// starting point.
//
// For every target update in this command (real Create/Update ops AND synthetic
// Resolve ops), each opaque $ref in the target config is followed to its source
// resource (by KSUID). The source resource is looked up first among this command's
// resource ops (a source being CREATED here is not yet persisted, via
// sourceTargetByKsuid), then in the datastore. The source's target config is then
// scanned: if it carries any opaque $ref, the chain is rejected with an error
// naming both target labels and no secret material.
//
// One hop is sufficient. A deeper chain (the source's target opaquely refs yet
// another opaque target) is still rejected here, because the immediate hop this
// guard inspects is already opaque. A nil datastore is treated as "nothing to
// traverse" — such changesets never reference persisted secret sources.
func rejectTransitivelyOpaqueTargets(
	targetUpdates []TargetUpdate,
	sourceTargetByKsuid map[string]string,
	ds TargetDatastore,
) error {
	if ds == nil {
		return nil
	}

	// sourceTargetLabel resolves a secret source KSUID to its target label, checking
	// this command's resource ops before falling back to the persisted resource.
	sourceTargetLabel := func(ksuid string) (string, error) {
		if label, ok := sourceTargetByKsuid[ksuid]; ok {
			return label, nil
		}
		res, err := ds.LoadResourceById(ksuid)
		if err != nil {
			return "", fmt.Errorf("load secret source resource %q: %w", ksuid, err)
		}
		if res == nil {
			return "", nil
		}
		return res.Target, nil
	}

	for i := range targetUpdates {
		referencing := targetUpdates[i].Target.Label
		opaqueRefs := resolver.ExtractOpaqueResolvableURIsFromJSON(targetUpdates[i].Target.Config)
		for _, ref := range opaqueRefs {
			sourceLabel, err := sourceTargetLabel(ref.KSUID())
			if err != nil {
				return err
			}
			if sourceLabel == "" || sourceLabel == referencing {
				continue
			}
			sourceTarget, err := ds.LoadTarget(sourceLabel)
			if err != nil {
				return fmt.Errorf("load secret source target %q: %w", sourceLabel, err)
			}
			if sourceTarget == nil {
				continue
			}
			if len(resolver.ExtractOpaqueResolvableURIsFromJSON(sourceTarget.Config)) > 0 {
				return fmt.Errorf(
					"cannot bootstrap a credential from a credential that itself requires resolution: "+
						"target %q references a secret whose source lives on target %q, which itself carries opaque references",
					referencing, sourceLabel)
			}
		}
	}

	return nil
}
