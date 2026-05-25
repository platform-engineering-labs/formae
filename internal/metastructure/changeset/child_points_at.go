// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// childPointsAt returns true iff `k`'s parent-reference properties identify
// `producer` as its parent instance.
//
// Returns false when `k.DesiredState.Schema.ParentMappings` is empty: without
// mappings, instance membership cannot be proven — the rule conservatively
// does not fire and the engine falls back to default edge behavior.
//
// The mappings are AND-combined: every mapping must match for the function
// to return true. This is what makes the rule "instance-safe" for composite
// parent relationships (e.g., ECS TaskSet whose parent Service is keyed by
// ServiceName + Cluster rather than a single ARN).
//
// Phases:
//   - OperationDelete: compares actual property values on both sides — the
//     destroy planner already operates on the resource snapshot captured in
//     DesiredState, so we read literal values from there.
//   - OperationCreate: chases Resolvable references through baseResources to
//     compare URIs. The child's parent-reference field holds a Resolvable
//     carrying the producer's KSUID-based FormaeURI rather than a literal at
//     create time, so identity matching is done on URIs rather than values.
//
// `baseResources` is the changeset's index from stripped FormaeURI to the
// base (Create/Replace/Update) ResourceUpdate that produces that URI. It is
// only consulted in the create branch — destroy passes nil.
func childPointsAt(
	k, producer *resource_update.ResourceUpdate,
	phase resource_update.OperationType,
	baseResources map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate,
) bool {
	mappings := k.DesiredState.Schema.ParentMappings
	if len(mappings) == 0 {
		return false
	}
	for _, m := range mappings {
		switch phase {
		case resource_update.OperationDelete:
			if !destroySideMatches(k, producer, m) {
				return false
			}
		case resource_update.OperationCreate:
			if !createSideMatches(k, producer, m, baseResources) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// destroySideMatches checks one parent mapping on the destroy path by reading
// effective property values from both sides' DesiredState. The destroy planner
// works off the snapshot of the resource being torn down — which is stored in
// DesiredState — so we read child and producer values from there.
//
// ParentProperty names the key on the producer side, ChildProperty names the
// key on the child side. They can diverge — e.g., TaskSet→Service maps
// "ServiceName"→"Service" — so we look each up separately.
//
// Post-apply the resolver leaves cross-resource references in the resolved-
// reference shape `{"$ref": ..., "$value": ...}` rather than collapsing them
// to a literal scalar. Resource.GetEffectivePropertyValue unwraps that for us
// — otherwise a child whose ChildProperty is a resolved reference and a
// producer whose ParentProperty is a literal would never compare equal even
// when they identify the same instance.
func destroySideMatches(k, producer *resource_update.ResourceUpdate, m pkgmodel.ParentMapping) bool {
	kVal, ok := k.DesiredState.GetEffectivePropertyValue(m.ChildProperty)
	if !ok {
		return false
	}

	// Producer's value is keyed by ParentProperty (which equals Schema.Identifier
	// when the relationship pins on the parent's primary identifier).
	pVal, ok := producer.DesiredState.GetEffectivePropertyValue(m.ParentProperty)
	if !ok {
		return false
	}
	return kVal == pVal
}

// createSideMatches is the create-phase counterpart to destroySideMatches.
//
// At create time the child's parent-reference property does not yet hold a
// literal (the producer has not been created), but a Resolvable carrying the
// producer's KSUID-based FormaeURI. We chase that URI through baseResources
// (the changeset's URI-indexed view of every Create/Replace/Update operation)
// to compare identities rather than concrete property values.
//
// Two reference shapes are possible:
//
//   - **Parent reference** — the K-side URI resolves to a resource (in
//     baseResources) whose Type matches `k.DesiredState.Schema.Parent`. In
//     this case the mapping is simply asserting "K points at its parent"; the
//     match holds iff the URI equals `producer`'s URI.
//   - **Sibling reference** — the K-side URI does NOT resolve to a parent-
//     typed resource. This covers two sub-cases: (a) the referenced resource
//     is in baseResources but has a different type (a peer K and producer
//     share, e.g., an ECS Cluster), or (b) the referenced resource is outside
//     the current changeset (unchanged sibling whose KSUID still appears as a
//     Resolvable on both K and producer). In either sub-case the producer
//     must also carry a Resolvable at `ParentProperty` pointing at the same
//     resource; the match holds iff K's and producer's URIs are equal after
//     stripping property paths.
//
// `baseResources` is consulted only as a type discriminator for the parent-
// reference shortcut. If the referenced resource is absent we fall through to
// the sibling comparison rather than declining — otherwise composite-parent
// matches with an external shared resource (Service+TaskSet both pointing at
// a Cluster outside the changeset) would never fire.
func createSideMatches(
	k, producer *resource_update.ResourceUpdate,
	m pkgmodel.ParentMapping,
	baseResources map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate,
) bool {
	kRef, ok := k.DesiredState.GetPropertyReference(m.ChildProperty)
	if !ok {
		return false
	}

	if kIndexed, found := baseResources[kRef.Stripped()]; found &&
		kIndexed.DesiredState.Type == k.DesiredState.Schema.Parent {
		// Parent reference: K's URI must equal producer's URI.
		return kRef.Stripped() == producer.URI().Stripped()
	}

	// Sibling reference (referenced resource has a different type, or is not
	// in the current changeset at all). K and producer must point at the same
	// third resource. Read producer's value at m.ParentProperty — it should
	// also be a resolved reference.
	pRef, ok := producer.DesiredState.GetPropertyReference(m.ParentProperty)
	if !ok {
		return false
	}
	return kRef.Stripped() == pRef.Stripped()
}
