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
	LoadStack(stackLabel string) (*pkgmodel.Forma, error)
	LoadAllStacks() ([]*pkgmodel.Forma, error)
	BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error)
	GetKSUIDByTriplet(stack, label, resourceType string) (string, error)
	LatestLabelForResource(label string) (string, error)
}
