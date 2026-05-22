// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"github.com/tidwall/gjson"

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
//   - OperationCreate: not yet implemented (Task 2.4). The create branch needs
//     to chase Resolvable references through baseResources to compare URIs.
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
// Post-apply the resolver leaves cross-resource references in the Resolvable
// shape `{"$ref": ..., "$value": ...}` rather than collapsing them to a
// literal scalar. We must unwrap `$value` before comparing — otherwise a child
// whose ChildProperty is Resolvable and a producer whose ParentProperty is a
// literal would never compare equal even when they identify the same instance.
func destroySideMatches(k, producer *resource_update.ResourceUpdate, m pkgmodel.ParentMapping) bool {
	kVal, ok := getEffectivePropertyValue(k.DesiredState.Properties, m.ChildProperty)
	if !ok {
		return false
	}

	// Producer's value is keyed by ParentProperty (which equals Schema.Identifier
	// when the relationship pins on the parent's primary identifier).
	pVal, ok := getEffectivePropertyValue(producer.DesiredState.Properties, m.ParentProperty)
	if !ok {
		return false
	}
	return kVal == pVal
}

// getEffectivePropertyValue returns the unwrapped scalar value of a property.
//
// If the property is a Resolvable object (`{"$ref": ..., "$value": ...}`),
// returns `$value` — this is the shape the resolver leaves on persisted
// properties post-apply. If the property is a literal scalar, returns it
// directly.
//
// Returns false when the property doesn't exist, isn't a scalar, or is a
// non-Resolvable object — none of those are valid for parent-mapping equality.
func getEffectivePropertyValue(props []byte, propName string) (string, bool) {
	result := gjson.GetBytes(props, propName)
	if !result.Exists() {
		return "", false
	}
	if result.IsObject() {
		// Resolvable shape: {"$ref": ..., "$value": ...} — unwrap to $value.
		if result.Get("$ref").Exists() {
			v := result.Get("$value")
			if !v.Exists() {
				return "", false
			}
			return v.String(), true
		}
		// Non-Resolvable object — not a valid scalar for parent-mapping comparison.
		return "", false
	}
	return result.String(), true
}

// createSideMatches is the create-phase counterpart to destroySideMatches.
//
// At create time the child's parent-reference property does not yet hold a
// literal (the producer has not been created), but a Resolvable carrying the
// producer's KSUID-based FormaeURI. We chase that URI through baseResources
// (the changeset's URI-indexed view of every Create/Replace/Update operation)
// to compare identities rather than concrete property values.
//
// Two reference shapes are possible and they are discriminated by Type:
//
//   - **Parent reference** — the K-side URI resolves to a resource whose Type
//     matches `k.DesiredState.Schema.Parent`. In this case the mapping is
//     simply asserting "K points at its parent"; the match holds iff the
//     URI equals `producer`'s URI.
//   - **Sibling reference** — the K-side URI resolves to some other resource
//     (typically a peer that K and producer both reference, e.g., an ECS
//     Cluster shared by TaskSet and Service). In this case the producer must
//     also carry a Resolvable at `ParentProperty` pointing at the same third
//     resource; the match holds iff K's and producer's URIs are equal after
//     stripping property paths.
//
// `baseResources` is consulted purely for the type discriminator. A missing
// entry — e.g., the K-side URI points at a resource outside the current
// changeset — yields no match: without knowing the referenced resource's
// type we cannot tell whether this is a parent or sibling reference and must
// conservatively decline.
func createSideMatches(
	k, producer *resource_update.ResourceUpdate,
	m pkgmodel.ParentMapping,
	baseResources map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate,
) bool {
	kRef, ok := extractResolvableURI(k.DesiredState.Properties, m.ChildProperty)
	if !ok {
		return false
	}

	kIndexed, found := baseResources[kRef.Stripped()]
	if !found {
		return false
	}

	if kIndexed.DesiredState.Type == k.DesiredState.Schema.Parent {
		// Parent reference: K's URI must equal producer's URI.
		return kRef.Stripped() == producer.URI().Stripped()
	}

	// Sibling reference: K and producer must point at the same third resource.
	// Read producer's value at m.ParentProperty — it should also be a Resolvable.
	pRef, ok := extractResolvableURI(producer.DesiredState.Properties, m.ParentProperty)
	if !ok {
		return false
	}
	return kRef.Stripped() == pRef.Stripped()
}

// extractResolvableURI reads `propName` out of `props` (raw JSON) and returns
// the underlying FormaeURI when the value is a Resolvable object of the form
// `{"$ref": "<uri>", "$value": ...}`. Returns false for literal values,
// non-object values, or objects without a `$ref` key — none of which can be
// chased to a base resource for type discrimination.
//
// Uses gjson against the raw Properties JSON (Properties is json.RawMessage,
// not a parsed map) to match the convention used by the surrounding
// dependency-extraction code in resolver and resource_update.
func extractResolvableURI(props []byte, propName string) (pkgmodel.FormaeURI, bool) {
	field := gjson.GetBytes(props, propName)
	if !field.IsObject() {
		return "", false
	}
	ref := field.Get("$ref")
	if !ref.Exists() {
		return "", false
	}
	return pkgmodel.FormaeURI(ref.String()), true
}
