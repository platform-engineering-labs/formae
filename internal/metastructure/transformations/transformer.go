// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResourceTransformer defines the interface for transforming resources
type ResourceTransformer interface {
	// ApplyToResource applies the transformation to a resource and returns the transformed resource
	ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, error)
}

// ChainedTransformer allows applying multiple transformers in sequence
type ChainedTransformer struct {
	transformers []ResourceTransformer
}

// NewChainedTransformer creates a new chained transformer with the given transformers
func NewChainedTransformer(transformers ...ResourceTransformer) *ChainedTransformer {
	return &ChainedTransformer{
		transformers: transformers,
	}
}

// ApplyToResource applies all transformers in sequence
func (ct *ChainedTransformer) ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, error) {
	if resource == nil {
		return nil, fmt.Errorf("resource cannot be nil")
	}

	result := resource
	var err error

	for i, transformer := range ct.transformers {
		result, err = transformer.ApplyToResource(result)
		if err != nil {
			return nil, fmt.Errorf("transformer %d failed: %w", i, err)
		}
	}

	return result, nil
}

// Add appends a transformer to the chain
func (ct *ChainedTransformer) Add(transformer ResourceTransformer) {
	ct.transformers = append(ct.transformers, transformer)
}
