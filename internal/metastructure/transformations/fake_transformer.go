// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type FakeTransformer struct{}

func NewFakeTransformer() *FakeTransformer {
	return &FakeTransformer{}
}

// ApplyToResource applies the transformation to hash all secret values in the resource
func (pv *FakeTransformer) ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, error) {
	if resource == nil {
		return nil, fmt.Errorf("resource cannot be nil")
	}

	// Create a copy of the resource to avoid modifying the original
	transformedResource := &pkgmodel.Resource{
		Label:              resource.Label,
		Type:               resource.Type,
		Stack:              resource.Stack,
		Target:             resource.Target,
		Properties:         resource.Properties,
		ReadOnlyProperties: resource.ReadOnlyProperties,
		Schema:             resource.Schema,
		PatchDocument:      resource.PatchDocument,
		NativeID:           resource.NativeID,
	}

	return transformedResource, nil
}
