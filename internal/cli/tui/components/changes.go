// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// patchOperation is a decoded JSON Patch operation.
type patchOperation struct {
	Op    string
	Path  string
	Value any
}

// PropertyChange holds the structured representation of a property patch
// operation. All fields are copied verbatim from renderer/patches.go so that
// callers in this package (changelines.go) and in simview/driftview can use
// the same type without importing renderer.
type PropertyChange struct {
	Path             string
	Value            string
	OldValue         string
	Operation        string
	HasOld           bool
	IsRef            bool
	IsOpaque         bool
	ExistsInPrevious bool

	// NoOp marks a change the renderer should suppress entirely: a field that
	// is force-resent on every update (requiredOnUpdate) but whose value did
	// not change. The plugin still receives it; the user shouldn't see it as a
	// "change" on an otherwise-clean reconcile.
	NoOp bool

	// IsCascadeResolvable signals a cascade-update synthetic op where the
	// new value isn't knowable at plan time (e.g. provider-assigned
	// identifiers like AWS ARNs). The renderer prints a friendly
	// "to point at the new <source-label> (current: <value>)" line
	// instead of trying to format a missing value.
	IsCascadeResolvable bool
	CascadeSourceLabel  string
	CascadeCurrentValue string
}

// ChangeSet is the structured form of a patch document's visible changes.
type ChangeSet struct {
	Properties []PropertyChange
}

// extractFromOperations is the single semantics home for the per-operation
// loop: every op goes through extractPropertyChange. Tags are not special-cased
// — they render as generic objects like any other collection. Both
// ExtractChanges and MutableChangesForReplace delegate here so the logic lives
// in exactly one place.
func extractFromOperations(ops []patchOperation, props map[string]any, previousProperties json.RawMessage, refLabels map[string]string) (ChangeSet, error) {
	var cs ChangeSet
	for _, patch := range ops {
		change, err := extractPropertyChange(patch, props, previousProperties, refLabels)
		if err != nil {
			return ChangeSet{}, fmt.Errorf("error processing property patch: %w", err)
		}
		cs.Properties = append(cs.Properties, change)
	}
	return cs, nil
}

// ExtractChanges parses a JSON Patch document into structured changes without
// rendering. Same semantics as FormatPatchDocument: NoOp suppression markers,
// cascade-resolvable synthesis, references resolved to labels.
func ExtractChanges(patchDoc, properties, previousProperties json.RawMessage, refLabels map[string]string) (ChangeSet, error) {
	patches, err := decodePatchOperations(patchDoc)
	if err != nil {
		return ChangeSet{}, err
	}

	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return ChangeSet{}, fmt.Errorf("error parsing properties document: %w", err)
	}

	return extractFromOperations(patches, props, previousProperties, refLabels)
}

// MutableChangesForReplace returns the changes of patchDoc minus the paths in
// createOnlyPatch — the carried mutable changes shown alongside a replace cause.
func MutableChangesForReplace(patchDoc, createOnlyPatch, properties, previousProperties json.RawMessage, refLabels map[string]string) (ChangeSet, error) {
	immutablePaths := make(map[string]bool)
	if len(createOnlyPatch) > 0 {
		var ops []struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(createOnlyPatch, &ops); err == nil {
			for _, op := range ops {
				immutablePaths[op.Path] = true
			}
		}
	}

	patches, err := decodePatchOperations(patchDoc)
	if err != nil {
		return ChangeSet{}, err
	}

	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return ChangeSet{}, fmt.Errorf("error parsing properties document: %w", err)
	}

	var filtered []patchOperation
	for _, patch := range patches {
		if !immutablePaths[patch.Path] {
			filtered = append(filtered, patch)
		}
	}

	return extractFromOperations(filtered, props, previousProperties, refLabels)
}

// decodePatchOperations parses a JSON Patch document into patchOperation
// structs without any gtree/rendering side-effects.
func decodePatchOperations(patchDoc json.RawMessage) ([]patchOperation, error) {
	var patches []map[string]any
	if err := json.Unmarshal(patchDoc, &patches); err != nil {
		return nil, fmt.Errorf("error parsing patch document: %w", err)
	}

	var operations []patchOperation
	for _, patch := range patches {
		op, okOp := patch["op"].(string)
		path, okPath := patch["path"].(string)
		if okOp && okPath {
			operations = append(operations, patchOperation{
				Op:    op,
				Path:  path,
				Value: patch["value"],
			})
		}
	}
	return operations, nil
}

// extractPropertyChange extracts property information from a patch operation.
func extractPropertyChange(patch patchOperation, props map[string]any, previousProperties json.RawMessage, refLabels map[string]string) (PropertyChange, error) {
	change := PropertyChange{
		Path:      resolveEntitySetPath(patch.Path, patch.Op, props, previousProperties),
		Operation: patch.Op,
	}

	// Cascade-resolvable marker: the value is an object carrying
	// `$cascade-resolvable: true` and metadata about the source. The new
	// concrete value isn't known at plan time (provider-assigned), so we
	// short-circuit normal value formatting and capture the source info
	// for friendly rendering.
	if valueMap, ok := patch.Value.(map[string]any); ok {
		if isCascade, _ := valueMap["$cascade-resolvable"].(bool); isCascade {
			change.IsCascadeResolvable = true
			change.CascadeSourceLabel, _ = valueMap["$source-label"].(string)
			change.CascadeCurrentValue, _ = valueMap["$current-value"].(string)
			return change, nil
		}
	}

	// Set IsOpaque after we have the path
	propsBytes, _ := json.Marshal(props)
	change.IsOpaque = isOpaqueProperty(change.Path, previousProperties) || isOpaqueProperty(change.Path, json.RawMessage(propsBytes))

	// For "add" operations, check if the property already exists in previous state.
	// This happens for WriteOnly fields that are stripped before patch comparison —
	// jsonpatch sees them as missing and generates "add", but they're really updates.
	if patch.Op == "add" && len(previousProperties) > 0 {
		change.ExistsInPrevious = propertyExistsInPrevious(change.Path, previousProperties)
	}

	// Try to get a reference value first (for add operations)
	if patch.Op == "add" {
		if inferredValue := inferValueFromRef(props, patch.Path, refLabels); inferredValue != "" {
			change.Value = inferredValue
			change.IsRef = true
			return change, nil
		}
	}

	// Format the actual value
	if patch.Value != nil && patch.Value != "" {
		change.Value = formatPatchValue(patch.Value, refLabels)
	} else if patch.Op == "remove" && len(previousProperties) > 0 {
		// Remove operations don't carry a value per RFC 6902, so look it up from previous state
		if oldValue := extractPreviousValue(previousProperties, patch.Path); oldValue != "" {
			change.Value = oldValue
		} else {
			change.Value = "\"(empty)\""
		}
	} else {
		change.Value = "\"(empty)\""
	}

	if patch.Op == "replace" && len(previousProperties) > 0 {
		if oldValue := extractPreviousValue(previousProperties, patch.Path); oldValue != "" {
			change.OldValue = oldValue
			change.HasOld = true
		}
	}

	// A force-resent field surfaces as an "add" whose path already exists in
	// the previous state (a requiredOnUpdate field stripped before the diff).
	// Carry its previous value so it renders as a change, and mark it a no-op
	// when the value is unchanged so the caller can suppress it — the plugin
	// still receives the re-send, but the user shouldn't see a "change" that
	// isn't one.
	//
	// The no-op test compares JSON-canonical values, not rendered strings: a
	// rendered compare would collapse distinct JSON types (the string "1" vs
	// the number 1) into the same text and wrongly suppress a real change, and
	// would never match an opaque re-send (whose rendered form is the
	// "(opaque value)" placeholder) against its unchanged underlying secret.
	if patch.Op == "add" && change.ExistsInPrevious {
		if prev, ok := previousRawValue(previousProperties, patch.Path); ok {
			change.OldValue = formatValueForDisplay(prev.Value())
			change.HasOld = true
			change.NoOp = canonicalValue(patch.Value) == canonicalValue(prev.Value())
		}
	}

	return change, nil
}

// previousRawValue looks up the raw (un-rendered) previous value at a JSON
// Pointer path, returning whether the path exists.
func previousRawValue(oldProps json.RawMessage, path string) (gjson.Result, bool) {
	if len(oldProps) == 0 {
		return gjson.Result{}, false
	}
	jsonPath := strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", ".")
	result := gjson.GetBytes(oldProps, jsonPath)
	return result, result.Exists()
}

// canonicalValue normalizes a value for change detection. It unwraps Formae's
// Value wrapper so an opaque re-send compares against its underlying secret
// ($value), then serializes to JSON so that distinct JSON types never collapse
// to the same text. Falls back to a best-effort string on marshal failure.
func canonicalValue(v any) string {
	v = unwrapFormaeValue(v)
	if bytes, err := json.Marshal(v); err == nil {
		return string(bytes)
	}
	return fmt.Sprintf("%v", v)
}

// unwrapFormaeValue returns the inner $value of a Formae Value wrapper, or the
// value unchanged when it isn't a wrapper.
func unwrapFormaeValue(v any) any {
	if valueMap, ok := v.(map[string]any); ok {
		if inner, ok := valueMap["$value"]; ok {
			return inner
		}
	}
	return v
}

// cleanPatchPath converts JSON Pointer paths to more readable format.
func cleanPatchPath(path string) string {
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}

	// Convert array indices
	// e.g. "Tags/3/Value" -> "Tags[3].Value"
	segments := strings.Split(path, "/")
	var parts []string
	for _, part := range segments {
		if len(parts) > 0 {
			if _, err := strconv.Atoi(part); err == nil {
				parts[len(parts)-1] = parts[len(parts)-1] + "[" + part + "]"
				continue
			}
		}
		parts = append(parts, part)
	}

	return strings.Join(parts, ".")
}

// resolveEntitySetPath rewrites a positional array index in a change path into
// the indexed element's entity-set key, so a change to one element of a keyed
// collection identifies WHICH element — e.g. "Tags[env].Value" instead of a
// bare "Tags.Value", and "Tags[temporary]" for a removed element. It handles
// single-level collection paths (/Coll/N or /Coll/N/Field) for remove/replace
// ops; adds keep their positional index (stripped downstream) because the added
// object already shows its own identity. Falls back to cleanPatchPath(rawPath)
// whenever a key can't be resolved from the data.
func resolveEntitySetPath(rawPath, op string, props map[string]any, previousProperties json.RawMessage) string {
	clean := cleanPatchPath(rawPath)
	if op != "remove" && op != "replace" {
		return clean
	}

	segs := strings.Split(strings.TrimPrefix(rawPath, "/"), "/")
	if len(segs) < 2 || len(segs) > 3 {
		return clean
	}
	coll := segs[0]
	idx, err := strconv.Atoi(segs[1])
	if err != nil {
		return clean
	}
	changedField := ""
	if len(segs) == 3 {
		changedField = segs[2]
	}

	// Removes reference an element that's gone from the current properties, so
	// look it up in the previous state; replaces keep the (unchanged) key in
	// current properties.
	source := props
	if op == "remove" {
		if len(previousProperties) == 0 {
			return clean
		}
		var prev map[string]any
		if err := json.Unmarshal(previousProperties, &prev); err != nil {
			return clean
		}
		source = prev
	}

	list, ok := source[coll].([]any)
	if !ok || idx < 0 || idx >= len(list) {
		return clean
	}
	elem, ok := list[idx].(map[string]any)
	if !ok {
		return clean
	}
	keyField := pickIndexField(list, changedField)
	if keyField == "" {
		return clean
	}
	keyVal, ok := elem[keyField]
	if !ok {
		return clean
	}

	display := coll + "[" + fmt.Sprintf("%v", keyVal) + "]"
	if changedField != "" {
		display += "." + changedField
	}
	return display
}

// pickIndexField returns the field that best identifies elements of a keyed
// collection: the alphabetically-first field whose scalar value is unique
// across all elements, excluding the field currently being changed. When no
// field is unique it falls back to the first candidate for a stable label, and
// returns "" when there are no usable candidates.
func pickIndexField(list []any, exclude string) string {
	first, ok := list[0].(map[string]any)
	if !ok {
		return ""
	}
	cands := make([]string, 0, len(first))
	for k := range first {
		if k == exclude {
			continue
		}
		cands = append(cands, k)
	}
	sort.Strings(cands)
	for _, f := range cands {
		if isUniqueScalarField(list, f) {
			return f
		}
	}
	if len(cands) > 0 {
		return cands[0]
	}
	return ""
}

// isUniqueScalarField reports whether field has a present, scalar, and unique
// value across every element of list.
func isUniqueScalarField(list []any, field string) bool {
	seen := make(map[string]bool, len(list))
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return false
		}
		v, ok := m[field]
		if !ok {
			return false
		}
		switch v.(type) {
		case map[string]any, []any:
			return false
		}
		key := fmt.Sprintf("%v", v)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func extractPreviousValue(oldProps json.RawMessage, path string) string {
	if len(oldProps) == 0 {
		return ""
	}

	jsonPath := strings.TrimPrefix(path, "/")

	// gjson wants dot notation
	jsonPath = strings.ReplaceAll(jsonPath, "/", ".")

	result := gjson.GetBytes(oldProps, jsonPath)
	if !result.Exists() {
		return ""
	}

	return formatValueForDisplay(result.Value())
}

// inferValueFromRef tries to infer what dependency is being added by looking at existing array elements.
func inferValueFromRef(props map[string]any, path string, refLabels map[string]string) string {
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(pathParts) == 0 {
		return ""
	}

	// Handle array case - e.g. /SubnetIds/2
	if len(pathParts) >= 2 {
		arrayName := pathParts[0]
		if array, ok := props[arrayName].([]any); ok && len(array) > 0 {
			for _, elem := range array {
				if refMap, ok := elem.(map[string]any); ok {
					if ref, hasRef := refMap["$ref"].(string); hasRef {
						return formatReferenceValue(ref, refLabels)
					}
				}
			}
		}
	}

	// Handle single property case - e.g. /VpcId
	if len(pathParts) == 1 {
		propName := pathParts[0]
		if propValue, ok := props[propName]; ok {
			if refMap, ok := propValue.(map[string]any); ok {
				if ref, hasRef := refMap["$ref"].(string); hasRef {
					return formatReferenceValue(ref, refLabels)
				}
			}
		}
	}

	return ""
}

// formatPatchValue formats a patch value for display.
func formatPatchValue(value any, refLabels map[string]string) string {
	// Check if this is a reference value
	if refMap, ok := value.(map[string]any); ok {
		if ref, hasRef := refMap["$ref"].(string); hasRef {
			// This is a reference - format it nicely
			return formatReferenceValue(ref, refLabels)
		}
	}

	switch v := value.(type) {
	case string:
		return v
	case nil:
		return "null"
	case map[string]any, []any:
		return formatCompositeValue(v, refLabels)
	default:
		return formatValueForDisplay(v)
	}
}

// formatCompositeValue renders an object or array value in a compact, readable
// form — no raw JSON. Objects render as "{k: v, k: v}" with keys sorted; arrays
// as "[v, v]". Forma special-value wrappers ($ref/$value/$visibility) are
// unwrapped. Used by both the add-side (formatPatchValue) and remove/old-side
// (formatValueForDisplay) so a diff's two halves stay byte-aligned.
//
// A top-level scalar is returned unquoted so QuoteCardValue can decide quoting
// at render time (unchanged from the old behavior); scalars nested inside an
// object/array are quoted inline, since QuoteCardValue treats the whole "{…}"
// as an already-composite string and won't recurse.
func formatCompositeValue(value any, refLabels map[string]string) string {
	return formatValueRec(value, refLabels, true)
}

func formatValueRec(value any, refLabels map[string]string, top bool) string {
	switch v := value.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			return formatReferenceValue(ref, refLabels)
		}
		if vis, ok := v["$visibility"].(string); ok && vis == "Opaque" {
			return "(opaque value)"
		}
		if inner, ok := v["$value"]; ok {
			return formatValueRec(inner, refLabels, top)
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+": "+formatValueRec(v[k], refLabels, false))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			parts = append(parts, formatValueRec(e, refLabels, false))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		if top {
			return v
		}
		return fmt.Sprintf("%q", v)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatReferenceValue formats a $ref value using provided label mappings.
func formatReferenceValue(ref string, refLabels map[string]string) string {
	// Parse: formae://KSUID#/property
	parts := strings.SplitN(ref, "://", 2)
	if len(parts) != 2 {
		return fmt.Sprintf("$ref:%s", ref)
	}

	pathParts := strings.SplitN(parts[1], "#", 2)
	if len(pathParts) != 2 {
		return fmt.Sprintf("$ref:%s", ref)
	}

	ksuid := pathParts[0]
	property := strings.TrimPrefix(pathParts[1], "/")

	if label, exists := refLabels[ksuid]; exists {
		return fmt.Sprintf("%s.%s", label, property)
	}

	// If KSUID not found in mapping, return the KSUID itself
	return fmt.Sprintf("%s.%s", ksuid, property)
}

// formatValueForDisplay handles both regular values and Formae Value structures.
// Composite values (objects and arrays) render through formatCompositeValue so
// the remove/old side stays byte-aligned with the add side.
func formatValueForDisplay(value any) string {
	switch value.(type) {
	case map[string]any, []any:
		return formatCompositeValue(value, nil)
	}
	return fmt.Sprintf("%v", value)
}

// propertyExistsInPrevious checks if a property path exists in previous properties.
// Used to detect WriteOnly fields that were stripped before patch comparison.
func propertyExistsInPrevious(path string, previousProperties json.RawMessage) bool {
	if len(previousProperties) == 0 {
		return false
	}

	jsonPath := strings.TrimPrefix(path, "/")
	jsonPath = strings.ReplaceAll(jsonPath, "/", ".")

	return gjson.GetBytes(previousProperties, jsonPath).Exists()
}

// isOpaqueProperty checks if a property path points to an opaque value.
func isOpaqueProperty(path string, properties json.RawMessage) bool {
	if len(properties) == 0 {
		return false
	}

	jsonPath := strings.TrimPrefix(path, "/")
	jsonPath = strings.ReplaceAll(jsonPath, "/", ".")

	result := gjson.GetBytes(properties, jsonPath)
	if !result.Exists() {
		return false
	}

	if result.IsObject() {
		visibility := result.Get("$visibility")
		return visibility.Exists() && visibility.String() == "Opaque"
	}

	return false
}
