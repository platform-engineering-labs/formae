// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResourceDataLookup defines the minimal interface needed by resource_update package
// to avoid circular dependencies with the datastore package.
// The datastore.Datastore interface satisfies this interface.
type ResourceDataLookup interface {
	LoadResourcesByStack(stackLabel string) ([]*pkgmodel.Resource, error)
	LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error)
	BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error)
	GetKSUIDByTriplet(stack, label, resourceType string) (string, error)
	LatestLabelForResource(label string) (string, error)
	FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error)
	FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error)
	// GetGeneratorIdentity returns the identity of the live generator with
	// this label on this stack. A zero GeneratorIdentity and a nil error
	// mean no such generator exists — mirrors datastore.Datastore's method
	// of the same name (its GeneratorIdentity is an alias of
	// pkgmodel.GeneratorIdentity for exactly this reason).
	GetGeneratorIdentity(label, stackLabel string) (pkgmodel.GeneratorIdentity, error)
}
