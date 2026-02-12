// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
)

var defaultIgnoredFields = []jsonpatch.Path{}

func GeneratePatch(document []byte, patch []byte, properties resolver.ResolvableProperties, schema pkgmodel.Schema, mode pkgmodel.FormaApplyMode) (json.RawMessage, bool, error) {
	return generatePatch(document, patch, properties, schema, mode)
}

func collectionSemanticsFromFieldHints(hints map[string]pkgmodel.FieldHint) jsonpatch.Collections {
	collections := jsonpatch.Collections{
		EntitySets: jsonpatch.EntitySets{},
		Arrays:     []jsonpatch.Path{},
	}

	for field, hint := range hints {
		path := jsonpatch.Path(fmt.Sprintf("$.%s", field))
		switch hint.UpdateMethod {
		case pkgmodel.FieldUpdateMethodEntitySet:
			collections.EntitySets[path] = jsonpatch.Key(hint.IndexField)
		case pkgmodel.FieldUpdateMethodArray:
			collections.Arrays = append(collections.Arrays, path)
		}
	}

	return collections
}

func generatePatch(document []byte, patch []byte, properties resolver.ResolvableProperties, schema pkgmodel.Schema, mode pkgmodel.FormaApplyMode) (json.RawMessage, bool, error) {
	flattenedDocument, flattenedPatch, err := flattenAndResolveRefs(document, patch, properties)
	if err != nil {
		return nil, false, fmt.Errorf("failed to flatten and resolve refs: %w", err)
	}

	var strategy jsonpatch.PatchStrategy
	switch mode {
	case pkgmodel.FormaApplyModeReconcile:
		strategy = jsonpatch.PatchStrategyExactMatch
	case pkgmodel.FormaApplyModePatch:
		strategy = jsonpatch.PatchStrategyEnsureExists
	default:
		return nil, false, fmt.Errorf("unable to generate patch document for apply mode: %s", mode)
	}

	patchOps, err := createPatchDocument(flattenedDocument, flattenedPatch, schema.Fields, schema.WriteOnly(), schema.HasProviderDefault(), collectionSemanticsFromFieldHints(schema.Hints), defaultIgnoredFields, strategy)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create patch document: %w", err)
	}

	if len(patchOps) == 0 {
		return nil, false, nil
	}

	needsReplacement, _ := containsCreateOnlyFields(patchOps, schema.CreateOnly())
	patchJson, err := json.Marshal(patchOps)
	if err != nil {
		return nil, false, fmt.Errorf("failed to serialize patch document: %w", err)
	}

	return json.RawMessage(patchJson), needsReplacement, nil
}

func createPatchDocument(document []byte, patch []byte, schemaFields []string, writeOnlyFields []string, hasProviderDefaultFields []string, collections jsonpatch.Collections, ignoredFields []jsonpatch.Path, strategy jsonpatch.PatchStrategy) ([]jsonpatch.JsonPatchOperation, error) {
	patchWithSchemaFieldsOnly, err := removeNonSchemaFields(patch, schemaFields)
	if err != nil {
		return nil, err
	}

	// Remove writeOnly fields from the document (existing state).
	// WriteOnly fields (like passwords) are never returned by the cloud provider's Read operation,
	// but Formae stores them. By removing them from the document before comparison,
	// jsonpatch will generate an "add" operation for these fields, ensuring they're always
	// included in the patch sent to the cloud provider.
	documentWithoutWriteOnly, err := removeWriteOnlyFields(document, writeOnlyFields)
	if err != nil {
		return nil, err
	}

	// Remove provider default fields from the document (existing state) if they are not in the desired state (patch).
	// Provider default fields are optional fields that cloud providers assign default values to.
	// If the user didn't specify the field in their PKL, we don't want to generate a "remove" operation
	// that would delete the provider-assigned default. By removing these fields from the document
	// before comparison (only when they're not in the desired state), we prevent oscillation.
	documentWithoutProviderDefaults, err := removeProviderDefaultFields(documentWithoutWriteOnly, patchWithSchemaFieldsOnly, hasProviderDefaultFields)
	if err != nil {
		return nil, err
	}

	// Create the actual patch document
	patchDoc, err := jsonpatch.CreatePatch(documentWithoutProviderDefaults, patchWithSchemaFieldsOnly, collections, ignoredFields, strategy)
	if err != nil {
		return nil, fmt.Errorf("failed to create JSON patch: %w", err)
	}

	return patchDoc, nil
}

// removeWriteOnlyFields removes writeOnly fields from the document.
// WriteOnly field paths can be nested (e.g., "LoginProfile.Password").
func removeWriteOnlyFields(document []byte, writeOnlyFields []string) ([]byte, error) {
	if len(writeOnlyFields) == 0 {
		return document, nil
	}

	var deserialized map[string]any
	if err := json.Unmarshal(document, &deserialized); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	for _, fieldPath := range writeOnlyFields {
		removeNestedField(deserialized, strings.Split(fieldPath, "."))
	}

	serialized, err := json.Marshal(deserialized)
	if err != nil {
		return nil, err
	}

	return serialized, nil
}

// removeNestedField removes a field at the given path from a nested map structure.
// For example, path ["LoginProfile", "Password"] removes the Password key from LoginProfile.
func removeNestedField(obj map[string]any, path []string) {
	if len(path) == 0 {
		return
	}

	if len(path) == 1 {
		delete(obj, path[0])
		return
	}

	// Navigate to the nested object
	if nested, ok := obj[path[0]].(map[string]any); ok {
		removeNestedField(nested, path[1:])
	}
}

// removeProviderDefaultFields removes fields from the document (actual state) that have provider defaults,
// but only if those fields are NOT present in the patch (desired state).
// This prevents "remove" operations for fields where the cloud provider assigns default values.
func removeProviderDefaultFields(document []byte, patch []byte, hasProviderDefaultFields []string) ([]byte, error) {
	if len(hasProviderDefaultFields) == 0 {
		return document, nil
	}

	var docMap map[string]any
	if err := json.Unmarshal(document, &docMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	var patchMap map[string]any
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patch: %w", err)
	}

	for _, fieldPath := range hasProviderDefaultFields {
		pathParts := strings.Split(fieldPath, ".")
		// Only remove from document if the field is NOT in the desired state (patch)
		if !fieldExistsInMap(patchMap, pathParts) {
			removeNestedField(docMap, pathParts)
		}
	}

	serialized, err := json.Marshal(docMap)
	if err != nil {
		return nil, err
	}

	return serialized, nil
}

// fieldExistsInMap checks if a field at the given path exists in a nested map structure.
// For example, path ["BucketEncryption", "Rules"] checks if obj["BucketEncryption"]["Rules"] exists.
func fieldExistsInMap(obj map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}

	val, exists := obj[path[0]]
	if !exists {
		return false
	}

	if len(path) == 1 {
		return true
	}

	// Navigate to the nested object
	if nested, ok := val.(map[string]any); ok {
		return fieldExistsInMap(nested, path[1:])
	}

	return false
}

func removeNonSchemaFields(patch []byte, schemaFields []string) ([]byte, error) {
	var deserialized map[string]any
	if err := json.Unmarshal(patch, &deserialized); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resource properties: %w", err)
	}
	modified := make(map[string]any)
	for _, field := range schemaFields {
		if val, ok := deserialized[field]; ok && hasValue(val) {
			modified[field] = val
		}
	}
	serialized, err := json.Marshal(modified)
	if err != nil {
		return nil, err
	}

	return serialized, err
}

func containsCreateOnlyFields(patchOps []jsonpatch.JsonPatchOperation, createOnlyFields []string) (bool, error) {
	for _, patch := range patchOps {
		path := cleanPath(patch.Path)
		if slices.Contains(createOnlyFields, path) {
			return true, nil
		}
	}

	return false, nil
}

func hasValue(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case string:
		return len(v) > 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func cleanPath(path string) string {
	if len(path) > 0 && path[0] == '/' {
		return path[1:]
	}

	return path
}

// resolveRefs uses properties to resolve references in the patch document
func resolveRefs(current, mod map[string]any, resolvableProperties resolver.ResolvableProperties) error {
	for k, v := range mod {
		switch modVal := v.(type) {
		case map[string]any:
			if ref, hasRef := modVal["$ref"]; hasRef {
				uri := pkgmodel.FormaeURI(ref.(string))
				ksuid := uri.KSUID()
				property := uri.PropertyPath()

				val, found := resolvableProperties.Get(ksuid, property)
				if !found {
					return fmt.Errorf("failed to resolve reference '%s': resource with KSUID '%s' and property '%s' not found", ref, ksuid, property)
				}
				modVal["$value"] = val
			}
			var currNested map[string]any
			if c, ok := current[k].(map[string]any); ok {
				currNested = c
			} else {
				currNested = map[string]any{}
			}
			if err := resolveRefs(currNested, modVal, resolvableProperties); err != nil {
				return err
			}
		case []any:
			var currArr []any
			if c, ok := current[k].([]any); ok {
				currArr = c
			}
			for i, elem := range modVal {
				var currElem any
				if len(currArr) > i {
					currElem = currArr[i]
				}
				if elemMap, ok := elem.(map[string]any); ok {
					var currElemMap map[string]any
					if currElem != nil {
						currElemMap, _ = currElem.(map[string]any)
					}
					// Preserve the key to resolve references
					wrappedElem := map[string]any{k: elemMap}
					if err := resolveRefs(currElemMap, wrappedElem, resolvableProperties); err != nil {
						return err
					}
					if resolvedElem, ok := wrappedElem[k].(map[string]any); ok {
						modVal[i] = resolvedElem
					}
				}
			}
		}
	}
	return nil
}

// flattenRefs recursively flattens $ref / $value pairs
func flattenRefs(m map[string]any) {
	for k, v := range m {
		switch vv := v.(type) {
		case map[string]any:
			if _, hasRef := vv["$ref"]; hasRef {
				if val, hasVal := vv["$value"]; hasVal {
					m[k] = val
					continue
				}
				m[k] = ""
				continue
			}
			flattenRefs(vv)
		case []any:
			for i, elem := range vv {
				if elemMap, ok := elem.(map[string]any); ok {
					// Check if this element has a $ref that needs flattening
					if _, hasRef := elemMap["$ref"]; hasRef {
						if val, hasVal := elemMap["$value"]; hasVal {
							vv[i] = val
							continue
						}
						// If no $value is found, something is wrong. Preserve for debug
						continue
					}
					flattenRefs(elemMap)
				}
			}
		}
	}
}

// normalizeMixedStructures removes nested structure, keeping only flattened (dot notation) keys
func normalizeMixedStructures(m map[string]any) {
	for _, v := range m {
		switch vv := v.(type) {
		case map[string]any:
			if hasMixedStructure(vv) {
				normalizeToFlattenedKeys(vv)
			}
			normalizeMixedStructures(vv)
		case []any:
			for _, elem := range vv {
				if elemMap, ok := elem.(map[string]any); ok {
					normalizeMixedStructures(elemMap)
				}
			}
		}
	}
}

// hasMixedStructure checks if a map has both nested and flattened keys
func hasMixedStructure(m map[string]any) bool {
	hasNested := false
	hasFlattened := false

	for k := range m {
		if strings.Contains(k, ".") {
			hasFlattened = true
		} else if _, ok := m[k].(map[string]any); ok {
			hasNested = true
		}
	}

	return hasNested && hasFlattened
}

// normalizeToFlattenedKeys removes nested structure, keeping only flattened (dot notation) keys
func normalizeToFlattenedKeys(m map[string]any) {
	for k, v := range m {
		if !strings.Contains(k, ".") {
			if _, ok := v.(map[string]any); ok {
				delete(m, k)
			}
		}
	}
}

func flattenAndResolveRefs(document []byte, patch []byte, resolvableProperties resolver.ResolvableProperties) ([]byte, []byte, error) {
	var current, mod map[string]any
	if err := json.Unmarshal(document, &current); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(patch, &mod); err != nil {
		return nil, nil, err
	}
	if err := resolveRefs(current, mod, resolvableProperties); err != nil {
		return nil, nil, err
	}
	flattenRefs(current)
	flattenRefs(mod)

	// handle mixed nested/flattened structures
	normalizeMixedStructures(current)
	normalizeMixedStructures(mod)

	currentRes, err := json.Marshal(current)
	if err != nil {
		return nil, nil, err
	}
	modRes, err := json.Marshal(mod)
	if err != nil {
		return nil, nil, err
	}

	return currentRes, modRes, nil
}
