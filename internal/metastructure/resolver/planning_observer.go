// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package resolver

import pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"

type ResourceObserver func(string, *pkgmodel.Resource)

func PlanningResourceObserver(lookup any) ResourceObserver {
	if o, ok := lookup.(interface {
		ObservePlanningResource(string, *pkgmodel.Resource)
	}); ok {
		return o.ObservePlanningResource
	}
	return nil
}
func ObservePlanningResource(observers []ResourceObserver, id string, r *pkgmodel.Resource) {
	for _, o := range observers {
		if o != nil {
			o(id, r)
		}
	}
}
func ObservePlanningStack(lookup any, label string) {
	if o, ok := lookup.(interface{ ObservePlanningStack(string) }); ok {
		o.ObservePlanningStack(label)
	}
}
func ObservePlanningTargetInventory(lookup any, label string) {
	if o, ok := lookup.(interface{ ObservePlanningTargetInventory(string) }); ok {
		o.ObservePlanningTargetInventory(label)
	}
}
