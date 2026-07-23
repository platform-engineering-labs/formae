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
		transformedProps, err := pv.transformRawProps(resource.Properties, resource.Schema)
		if err != nil {
			return nil, fmt.Errorf("failed to transform properties: %w", err)
		}
		transformedResource.Properties = transformedProps
	}

	if resource.ReadOnlyProperties != nil {
		transformedReadOnly, err := pv.transformRawProps(resource.ReadOnlyProperties, resource.Schema)
		if err != nil {
			return nil, fmt.Errorf("failed to transform read-only properties: %w", err)
		}
		transformedResource.ReadOnlyProperties = transformedReadOnly
	}

	if resource.PatchDocument != nil {
		transformedPatchDoc, err := pv.transformPatchDocument(resource.PatchDocument, resource.Schema)
		if err != nil {
			return nil, fmt.Errorf("failed to transform patch document: %w", err)
		}
		transformedResource.PatchDocument = transformedPatchDoc
	}

	return transformedResource, nil
}

func (pv *PersistValueTransformer) transformRawProps(properties json.RawMessage, schema pkgmodel.Schema) (json.RawMessage, error) {
	if len(properties) == 0 {
		return json.RawMessage("{}"), nil
	}
	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return nil, fmt.Errorf("failed to unmarshal properties: %w", err)
	}

	opaqueFields := make(map[string]bool)
	for _, f := range schema.Opaque() {
		opaqueFields[f] = true
	}

	if err := pv.processProps(props, opaqueFields); err != nil {
		return nil, fmt.Errorf("failed to process properties: %w", err)
	}
	result, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
	}
	return result, nil
}

// processProps hashes (a) any top-level property named in opaqueFields (schema-keyed,
// first cut = top-level scalars) and (b) any nested map carrying a $visibility=="Opaque"
// envelope. Idempotent: values already marked $hashed are skipped.
func (pv *PersistValueTransformer) processProps(m map[string]any, opaqueFields map[string]bool) error {
	for key, v := range m {
		if opaqueFields[key] {
			hashed, ok := pv.hashOpaqueField(v)
			if ok {
				m[key] = hashed
				continue
			}
		}
		switch val := v.(type) {
		case map[string]any:
			if visibility, ok := val["$visibility"].(string); ok && visibility == "Opaque" {
				if hashed, done := pv.hashEnvelope(val); done {
					m[key] = hashed
				}
			} else {
				if err := pv.processProps(val, nil); err != nil {
					return err
				}
			}
		case []any:
			for _, elem := range val {
				if elemMap, ok := elem.(map[string]any); ok {
					if err := pv.processProps(elemMap, nil); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// hashOpaqueField hashes a schema-opaque property value. It accepts a bare scalar
// (wrapping it into a hashed opaque envelope) or an existing envelope map.
func (pv *PersistValueTransformer) hashOpaqueField(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return pv.hashEnvelope(m)
	}
	// Bare scalar: wrap + hash.
	value := &pkgmodel.Value{Value: v, Visibility: pkgmodel.VisibilityOpaque}
	hashed := value.Hash()
	return map[string]any{"$value": hashed.Value, "$visibility": pkgmodel.VisibilityOpaque, "$hashed": true}, true
}

// hashEnvelope hashes an existing {$value,$visibility,...} map in place, unless already $hashed.
func (pv *PersistValueTransformer) hashEnvelope(val map[string]any) (map[string]any, bool) {
	if h, ok := val["$hashed"].(bool); ok && h {
		return val, false
	}
	original := val["$value"]
	value := &pkgmodel.Value{Value: original, Visibility: pkgmodel.VisibilityOpaque}
	if strategy, ok := val["$strategy"].(string); ok {
		value.Strategy = strategy
	}
	hashed := value.Hash()
	val["$value"] = hashed.Value
	val["$hashed"] = true
	return val, true
}

// transformPatchDocument hashes patch-op values purely structurally:
//   - if the op's path names a schema-opaque field, hash a bare scalar value (or leave an
//     already-$hashed envelope alone);
//   - if the op's value is itself an opaque envelope ({"$visibility":"Opaque",...}), hash it
//     in place unless it already carries $hashed:true.
//
// We deliberately do NOT substitute values by content match against other hashed properties:
// that both corrupted non-secret fields that happened to collide with a secret's plaintext and
// produced a bare (unmarked) digest, which hashOpaqueField treats as plaintext and re-hashes on
// the next boot backfill (hash-of-hash).
func (pv *PersistValueTransformer) transformPatchDocument(patchDoc json.RawMessage, schema pkgmodel.Schema) (json.RawMessage, error) {
	if len(patchDoc) == 0 {
		return patchDoc, nil
	}

	var patchOps []map[string]any
	if err := json.Unmarshal(patchDoc, &patchOps); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patch document: %w", err)
	}

	opaqueFields := make(map[string]bool)
	for _, f := range schema.Opaque() {
		opaqueFields["/"+f] = true
	}
	for i, op := range patchOps {
		value, hasValue := op["value"]
		if !hasValue {
			continue
		}
		if path, _ := op["path"].(string); opaqueFields[path] {
			if m, ok := value.(map[string]any); ok {
				if h, ok := m["$hashed"].(bool); ok && h {
					// Already a hashed envelope — idempotent, leave as-is.
					continue
				}
			}
			if s, ok := value.(string); ok {
				hashed := (&pkgmodel.Value{Value: s, Visibility: pkgmodel.VisibilityOpaque}).Hash()
				patchOps[i]["value"] = map[string]any{
					"$value":      hashed.Value,
					"$visibility": pkgmodel.VisibilityOpaque,
					"$hashed":     true,
				}
				continue
			}
		}
		// Not a schema-opaque path, but the value itself may be an explicit opaque
		// envelope (e.g. a patch op targeting a non-top-level opaque field).
		if m, ok := value.(map[string]any); ok {
			if visibility, ok := m["$visibility"].(string); ok && visibility == "Opaque" {
				if hashed, done := pv.hashEnvelope(m); done {
					patchOps[i]["value"] = hashed
				}
			}
		}
	}

	transformedPatchDoc, err := json.Marshal(patchOps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed patch document: %w", err)
	}

	return json.RawMessage(transformedPatchDoc), nil
}
