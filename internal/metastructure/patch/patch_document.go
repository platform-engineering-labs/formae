// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"
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
		Atomics:    []jsonpatch.Path{},
	}

	for field, hint := range hints {
		path := jsonpatch.Path(fmt.Sprintf("$.%s", field))
		switch hint.UpdateMethod {
		case pkgmodel.FieldUpdateMethodEntitySet:
			collections.EntitySets[path] = jsonpatch.Key(hint.IndexField)
		case pkgmodel.FieldUpdateMethodArray:
			collections.Arrays = append(collections.Arrays, path)
		case pkgmodel.FieldUpdateMethodAtomic:
			collections.Atomics = append(collections.Atomics, path)
		}
	}

	return collections
}

func entitySetProviderDefaultsFromHints(hints map[string]pkgmodel.FieldHint) map[string]string {
	result := map[string]string{}
	for field, hint := range hints {
		if hint.HasProviderDefault && hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "" {
			result[field] = hint.IndexField
		}
	}
	return result
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

	patchOps, err := createPatchDocument(flattenedDocument, flattenedPatch, schema.Fields, schema.WriteOnly(), schema.HasProviderDefault(), entitySetProviderDefaultsFromHints(schema.Hints), collectionSemanticsFromFieldHints(schema.Hints), defaultIgnoredFields, strategy)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create patch document: %w", err)
	}

	// Remove spurious patch operations that add empty arrays or maps.
	// The PKL schema renders unset nullable Listing/Mapping fields as
	// []/{}. An "add" of an empty collection to a field absent in the actual
	// state is always PKL rendering noise — a user clearing a field would
	// produce a "replace" (field exists in actual), not an "add".
	patchOps = filterSpuriousEmptyAdds(patchOps)

	// Strip empty collections from inside all patch operation values. This
	// cleans up phantom []/{}  values inside nested objects (e.g., empty
	// ResponseParameters inside an IntegrationResponse). Without this,
	// EntitySet array elements may not match their actual counterparts and
	// produce "array items are not unique" errors.
	patchOps = stripEmptyCollectionsFromOps(patchOps)

	if len(patchOps) == 0 {
		return nil, false, nil
	}

	// Separate createOnly operations from mutable operations. CreateOnly
	// fields cannot be updated in-place via the cloud API — if they changed,
	// the resource needs a full replacement (destroy + create). We detect
	// this and strip createOnly ops from the patch sent to the plugin.
	createOnlyFields := schema.CreateOnly()
	needsReplacement, _ := containsCreateOnlyFields(patchOps, createOnlyFields)
	patchOps = filterCreateOnlyFields(patchOps, createOnlyFields)

	if len(patchOps) == 0 && !needsReplacement {
		return nil, false, nil
	}

	patchJson, err := json.Marshal(patchOps)
	if err != nil {
		return nil, false, fmt.Errorf("failed to serialize patch document: %w", err)
	}

	return json.RawMessage(patchJson), needsReplacement, nil
}

func createPatchDocument(document []byte, patch []byte, schemaFields []string, writeOnlyFields []string, hasProviderDefaultFields []string, entitySetProviderDefaults map[string]string, collections jsonpatch.Collections, ignoredFields []jsonpatch.Path, strategy jsonpatch.PatchStrategy) ([]jsonpatch.JsonPatchOperation, error) {
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

	// For EntitySet fields with provider defaults, filter elements from the document (actual state)
	// whose key doesn't appear in the desired state. Cloud providers like AWS populate EntitySet
	// collections with many default elements (e.g., ~22 LoadBalancer attributes). Including all of
	// them in the patch can exceed API limits. By stripping unmatched elements before comparison,
	// only user-specified elements are included in the patch.
	documentFiltered, err := removeProviderDefaultEntitySetElements(documentWithoutProviderDefaults, patchWithSchemaFieldsOnly, entitySetProviderDefaults)
	if err != nil {
		return nil, err
	}

	// Strip empty arrays and maps from inside nested objects in both the
	// desired state and actual state. The PKL schema renders unset
	// nullable Listing/Mapping fields as []/{}. Without stripping, EntitySet
	// element matching fails because elements have different shapes (one has
	// phantom empty fields, the other doesn't), causing duplicate entries.
	cleanedDesired, err := StripNestedEmptyCollections(patchWithSchemaFieldsOnly)
	if err != nil {
		return nil, err
	}
	cleanedDocument, err := StripNestedEmptyCollections(documentFiltered)
	if err != nil {
		return nil, err
	}

	// Create the actual patch document
	patchDoc, err := jsonpatch.CreatePatch(cleanedDocument, cleanedDesired, collections, ignoredFields, strategy)
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
// Handles array traversal: if a path segment resolves to an array, the remaining
// path is applied to every map element in that array.
func removeNestedField(obj map[string]any, path []string) {
	if len(path) == 0 {
		return
	}

	if len(path) == 1 {
		delete(obj, path[0])
		return
	}

	val, exists := obj[path[0]]
	if !exists {
		return
	}

	// Navigate to the nested object
	if nested, ok := val.(map[string]any); ok {
		removeNestedField(nested, path[1:])
		return
	}

	// Handle arrays: apply remaining path to each map element
	if arr, ok := val.([]any); ok {
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]any); ok {
				removeNestedField(elemMap, path[1:])
			}
		}
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

// removeProviderDefaultEntitySetElements filters EntitySet arrays in the document (actual state)
// to only retain elements whose key (by indexField) matches a key in the patch (desired state).
// This handles cloud providers that populate EntitySet collections with many default elements
// (e.g., AWS LoadBalancer returns ~22 default attributes). Without filtering, all defaults would
// be included in the patch, potentially exceeding API limits.
func removeProviderDefaultEntitySetElements(document []byte, patch []byte, entitySetProviderDefaults map[string]string) ([]byte, error) {
	if len(entitySetProviderDefaults) == 0 {
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

	for field, indexKey := range entitySetProviderDefaults {
		docArr, ok := docMap[field].([]any)
		if !ok {
			continue
		}

		patchArr, ok := patchMap[field].([]any)
		if !ok {
			// Desired state doesn't have this field at all — remove entire array from document
			delete(docMap, field)
			continue
		}

		// Build a set of keys present in desired state
		desiredKeys := map[string]struct{}{}
		for _, elem := range patchArr {
			if elemMap, ok := elem.(map[string]any); ok {
				if keyVal, ok := elemMap[indexKey]; ok {
					desiredKeys[fmt.Sprintf("%v", keyVal)] = struct{}{}
				}
			}
		}

		// Filter document array to only keep elements with matching keys
		filtered := make([]any, 0, len(patchArr))
		for _, elem := range docArr {
			if elemMap, ok := elem.(map[string]any); ok {
				if keyVal, ok := elemMap[indexKey]; ok {
					if _, exists := desiredKeys[fmt.Sprintf("%v", keyVal)]; exists {
						filtered = append(filtered, elem)
					}
					continue
				}
			}
			// Keep elements we can't match on (no key field)
			filtered = append(filtered, elem)
		}
		docMap[field] = filtered
	}

	serialized, err := json.Marshal(docMap)
	if err != nil {
		return nil, err
	}

	return serialized, nil
}

// fieldExistsInMap checks if a field at the given path exists in a nested map structure.
// For example, path ["BucketEncryption", "Rules"] checks if obj["BucketEncryption"]["Rules"] exists.
// Handles array traversal: if a path segment resolves to an array, checks whether
// the remaining path exists in any map element of that array.
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

	// Handle arrays: check if remaining path exists in any map element
	if arr, ok := val.([]any); ok {
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]any); ok {
				if fieldExistsInMap(elemMap, path[1:]) {
					return true
				}
			}
		}
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

// StripNestedEmptyCollections recursively removes empty arrays and maps from
// inside nested objects in a JSON document. Top-level empty collections are
// preserved (they may represent intentional "clear" operations).
// This is used both in the patch pipeline (before diff comparison) and in the
// resource updater (before sending Properties to plugins for Create/Update)
// to clean PKL rendering artifacts (null → []/{}  from nullable Listing/Mapping fields).
func StripNestedEmptyCollections(data []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("StripNestedEmptyCollections: invalid JSON: %w", err)
	}

	for k, v := range doc {
		doc[k] = stripEmptyCollectionsFromValue(v)
	}

	return json.Marshal(doc)
}

// filterSpuriousEmptyAdds removes "add" operations with empty array or map
// values. The PKL schema renders unset nullable Listing/Mapping fields
// as []/{}. An "add" means the field is absent in the actual state, so adding
// an empty collection is never user intent — it's PKL rendering noise. A user
// clearing an existing field produces a "replace" (field exists), not "add".
func filterSpuriousEmptyAdds(patchOps []jsonpatch.JsonPatchOperation) []jsonpatch.JsonPatchOperation {
	filtered := make([]jsonpatch.JsonPatchOperation, 0, len(patchOps))
	for _, op := range patchOps {
		if op.Operation == "add" && isEmptyCollection(op.Value) {
			continue
		}
		filtered = append(filtered, op)
	}
	return filtered
}

// stripEmptyCollectionsFromOps recursively removes empty arrays and maps from
// inside all patch operation values. This ensures that EntitySet element
// matching works correctly when elements contain phantom []/{}  values.
func stripEmptyCollectionsFromOps(patchOps []jsonpatch.JsonPatchOperation) []jsonpatch.JsonPatchOperation {
	for i := range patchOps {
		patchOps[i].Value = stripEmptyCollectionsFromValue(patchOps[i].Value)
	}
	return patchOps
}

func stripEmptyCollectionsFromValue(val any) any {
	switch v := val.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for k, elem := range v {
			if isEmptyCollection(elem) {
				continue
			}
			cleaned[k] = stripEmptyCollectionsFromValue(elem)
		}
		return cleaned
	case []any:
		cleaned := make([]any, 0, len(v))
		for _, elem := range v {
			cleaned = append(cleaned, stripEmptyCollectionsFromValue(elem))
		}
		return cleaned
	default:
		return val
	}
}

func isEmptyCollection(val any) bool {
	switch v := val.(type) {
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

func containsCreateOnlyFields(patchOps []jsonpatch.JsonPatchOperation, createOnlyFields []string) (bool, error) {
	for _, patch := range patchOps {
		path := cleanPath(patch.Path)
		if isCreateOnlyPath(path, createOnlyFields) {
			return true, nil
		}
	}

	return false, nil
}

// filterCreateOnlyFields removes patch operations that target createOnly fields.
// These operations cannot be sent to the cloud API — createOnly fields are
// immutable after creation. If they changed, the caller uses needsReplacement
// to trigger a full destroy+create cycle instead.
func filterCreateOnlyFields(patchOps []jsonpatch.JsonPatchOperation, createOnlyFields []string) []jsonpatch.JsonPatchOperation {
	if len(createOnlyFields) == 0 {
		return patchOps
	}
	filtered := make([]jsonpatch.JsonPatchOperation, 0, len(patchOps))
	for _, op := range patchOps {
		path := cleanPath(op.Path)
		if !isCreateOnlyPath(path, createOnlyFields) {
			filtered = append(filtered, op)
		}
	}
	return filtered
}

// isCreateOnlyPath checks if a patch path targets a createOnly field.
// Matches both the field itself ("/DomainName") and nested paths within
// it ("/ContainerDefinitions/0/Name").
func isCreateOnlyPath(path string, createOnlyFields []string) bool {
	for _, field := range createOnlyFields {
		if path == field || strings.HasPrefix(path, field+"/") {
			return true
		}
	}
	return false
}

func hasValue(val any) bool {
	v, ok := val.(string)
	return !ok || len(v) > 0
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
				if found {
					modVal["$value"] = val
				}
				// If not found, keep the $ref as-is for late-binding resolution
				// at execution time (forward references to new resources).
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
