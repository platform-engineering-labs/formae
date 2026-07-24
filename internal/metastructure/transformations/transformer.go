// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResourceTransformer transforms a resource's persisted values. Its only
// implementation is PersistValueTransformer (the opaque-secret at-rest hashing
// engine); it stays an interface so callers such as ResourcePersister can hold
// it as a field and swap it in tests.
type ResourceTransformer interface {
	// ApplyToResource returns a copy of resource with opaque secret values hashed.
	ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, error)
}
