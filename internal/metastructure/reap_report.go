// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// DanglingReapedReference describes a still-reachable resource or target
// whose config/properties $ref a resource that has since been reaped.
//
// Nested and cross-target reaping is independent — each target reaps on its
// own accrued unreachability, with no cascade traversal to dependents (see
// target_reaper.go). That means a dependent on a different, still-reachable
// target can outlive the resource it points at. This function surfaces such
// references so a human can decide what to do; it never modifies or deletes
// anything itself (dangling-dependent report, not dangling-dependent
// cleanup).
type DanglingReapedReference struct {
	// ReapedKsuid is the KSUID of the reaped resource still being referenced.
	ReapedResource pkgmodel.FormaeURI
	// DependentResource is the referencing resource. Exactly one of
	// DependentResource / DependentTarget is set per finding.
	DependentResource *pkgmodel.Resource
	// DependentTarget is the referencing target config.
	DependentTarget *pkgmodel.Target
}

// FindDanglingReapedReferences reports every live resource or target whose
// config still $refs a resource that has since been reaped. Read-only: it
// performs no writes and fixes nothing, by design (see DanglingReapedReference).
func FindDanglingReapedReferences(ds datastore.Datastore) ([]DanglingReapedReference, error) {
	reaped, err := ds.LoadReapedResources()
	if err != nil {
		return nil, fmt.Errorf("failed to load reaped resources: %w", err)
	}
	if len(reaped) == 0 {
		return nil, nil
	}

	ksuids := make([]string, 0, len(reaped))
	uriByKsuid := make(map[string]pkgmodel.FormaeURI, len(reaped))
	for _, r := range reaped {
		ksuids = append(ksuids, r.Ksuid)
		uriByKsuid[r.Ksuid] = r.URI()
	}

	dependentResources, err := ds.FindResourcesDependingOnMany(ksuids)
	if err != nil {
		return nil, fmt.Errorf("failed to find resources depending on reaped resources: %w", err)
	}
	dependentTargets, err := ds.FindTargetsDependingOnMany(ksuids)
	if err != nil {
		return nil, fmt.Errorf("failed to find targets depending on reaped resources: %w", err)
	}

	var findings []DanglingReapedReference
	for _, ksuid := range ksuids {
		reapedURI := uriByKsuid[ksuid]
		for _, dep := range dependentResources[ksuid] {
			findings = append(findings, DanglingReapedReference{
				ReapedResource:    reapedURI,
				DependentResource: dep,
			})
		}
		for _, dep := range dependentTargets[ksuid] {
			findings = append(findings, DanglingReapedReference{
				ReapedResource:  reapedURI,
				DependentTarget: dep,
			})
		}
	}

	return findings, nil
}
