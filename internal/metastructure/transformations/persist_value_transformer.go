// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type PersistValueTransformer struct{}

// Ensure PersistValueTransformer implements ResourceTransformer
var _ ResourceTransformer = (*PersistValueTransformer)(nil)

func NewPersistValueTransformer() *PersistValueTransformer {
	return &PersistValueTransformer{}
}

// ApplyToResource applies the transformation to hash all secret values in the resource
func (pv *PersistValueTransformer) ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, error) {
	if resource == nil {
		return nil, fmt.Errorf("resource cannot be nil")
	}

	// Create a map to track original -> hashed value substitutions
	valueMap := make(map[string]string)

	transformedResource := &pkgmodel.Resource{
		Label:    resource.Label,
		Type:     resource.Type,
		Stack:    resource.Stack,
		Target:   resource.Target,
		Schema:   resource.Schema,
		NativeID: resource.NativeID,
		Managed:  resource.Managed,
		Ksuid:    resource.Ksuid,
	}

	if resource.Properties != nil {
		transformedProps, err := pv.transformRawProps(resource.Properties, valueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to transform properties: %w", err)
		}
		transformedResource.Properties = transformedProps
	}

	if resource.ReadOnlyProperties != nil {
		transformedReadOnly, err := pv.transformRawProps(resource.ReadOnlyProperties, valueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to transform read-only properties: %w", err)
		}
		transformedResource.ReadOnlyProperties = transformedReadOnly
	}

	// Transform PatchDocument using valueMap substitutions
	if resource.PatchDocument != nil {
		transformedPatchDoc, err := pv.transformPatchDocument(resource.PatchDocument, valueMap)
		if err != nil {
			return nil, fmt.Errorf("failed to transform patch document: %w", err)
		}
		transformedResource.PatchDocument = transformedPatchDoc
	}

	return transformedResource, nil
}

func (pv *PersistValueTransformer) transformRawProps(properties json.RawMessage, valueMap map[string]string) (json.RawMessage, error) {
	if len(properties) == 0 {
		return json.RawMessage("{}"), nil
	}

	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return nil, fmt.Errorf("failed to unmarshal properties: %w", err)
	}

	err := pv.processProps(props, valueMap)
	if err != nil {
		return nil, fmt.Errorf("failed to process properties: %w", err)
	}

	result, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
	}

	return result, nil
}

func (pv *PersistValueTransformer) processProps(m map[string]any, valueMap map[string]string) error {
	for _, v := range m {
		switch val := v.(type) {
		case map[string]any:
			if visibility, ok := val["$visibility"].(string); ok && visibility == "Opaque" {
				originalValue := val["$value"]

				// Skip if already hashed
				if valueStr, ok := originalValue.(string); ok && isAlreadyHashed(valueStr) {
					continue
				}

				value := &pkgmodel.Value{
					Value:      originalValue,
					Visibility: visibility,
				}
				if strategy, ok := val["$strategy"].(string); ok {
					value.Strategy = strategy
				}

				hashedValue := value.Hash()
				val["$value"] = hashedValue.Value

				// Store the mapping: original -> hashed
				if originalStr := fmt.Sprintf("%v", originalValue); originalStr != "" {
					valueMap[originalStr] = fmt.Sprintf("%v", hashedValue.Value)
				}
			} else {
				if err := pv.processProps(val, valueMap); err != nil {
					return err
				}
			}
		case []any:
			for _, elem := range val {
				if elemMap, ok := elem.(map[string]any); ok {
					if err := pv.processProps(elemMap, valueMap); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// transformPatchDocument substitutes any original opaque values with their hashed equivalents
func (pv *PersistValueTransformer) transformPatchDocument(patchDoc json.RawMessage, valueMap map[string]string) (json.RawMessage, error) {
	if len(patchDoc) == 0 {
		return patchDoc, nil
	}

	var patchOps []map[string]any
	if err := json.Unmarshal(patchDoc, &patchOps); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patch document: %w", err)
	}

	for i, op := range patchOps {
		if value, hasValue := op["value"]; hasValue {
			valueStr := fmt.Sprintf("%v", value)
			if hashedValue, found := valueMap[valueStr]; found {
				patchOps[i]["value"] = hashedValue
			}
		}
	}

	transformedPatchDoc, err := json.Marshal(patchOps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed patch document: %w", err)
	}

	return json.RawMessage(transformedPatchDoc), nil
}

// isAlreadyHashed checks if a string looks like a SHA-256 hash (64 hex characters)
func isAlreadyHashed(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
