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
// literal property values from both sides' DesiredState. The destroy planner
// works off the snapshot of the resource being torn down — which is stored in
// DesiredState — so we read child and producer values from there.
//
// ParentProperty names the key on the producer side, ChildProperty names the
// key on the child side. They can diverge — e.g., TaskSet→Service maps
// "ServiceName"→"Service" — so we look each up separately.
func destroySideMatches(k, producer *resource_update.ResourceUpdate, m pkgmodel.ParentMapping) bool {
	kVal, ok := k.DesiredState.GetProperty(m.ChildProperty)
	if !ok {
		return false
	}

	// Producer's value is keyed by ParentProperty (which equals Schema.Identifier
	// when the relationship pins on the parent's primary identifier).
	producerKey := m.ParentProperty

	pVal, ok := producer.DesiredState.GetProperty(producerKey)
	if !ok {
		return false
	}
	return kVal == pVal
}

// createSideMatches is the create-phase counterpart to destroySideMatches.
// Implemented in Task 2.4. Until then, panics if reached so an out-of-order
// task land does not silently degrade runtimeDependency create-side edges
// into default edges.
func createSideMatches(
	k, producer *resource_update.ResourceUpdate,
	m pkgmodel.ParentMapping,
	baseResources map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate,
) bool {
	_ = k
	_ = producer
	_ = m
	_ = baseResources
	panic("createSideMatches not yet implemented (RFC-0043 Task 2.4)")
}
