// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ddddddO/gtree"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

type patchOperation struct {
	Op    string
	Path  string
	Value any
}

type TagChange struct {
	Key       string
	Value     string
	OldValue  string
	Operation string
	HasOld    bool
}

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
	Tags       []TagChange
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

	var cs ChangeSet
	for _, patch := range patches {
		if strings.Contains(patch.Path, "/Tags/") {
			change, err := extractTagChange(patch, previousProperties)
			if err != nil {
				return ChangeSet{}, fmt.Errorf("error processing tag patch: %w", err)
			}
			cs.Tags = append(cs.Tags, change)
			continue
		}
		change, err := extractPropertyChange(patch, props, previousProperties, refLabels)
		if err != nil {
			return ChangeSet{}, fmt.Errorf("error processing property patch: %w", err)
		}
		cs.Properties = append(cs.Properties, change)
	}
	return cs, nil
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

	var cs ChangeSet
	for _, patch := range patches {
		if immutablePaths[patch.Path] {
			continue
		}
		if strings.Contains(patch.Path, "/Tags/") {
			change, err := extractTagChange(patch, previousProperties)
			if err != nil {
				return ChangeSet{}, fmt.Errorf("error processing tag patch: %w", err)
			}
			cs.Tags = append(cs.Tags, change)
			continue
		}
		change, err := extractPropertyChange(patch, props, previousProperties, refLabels)
		if err != nil {
			return ChangeSet{}, fmt.Errorf("error processing property patch: %w", err)
		}
		cs.Properties = append(cs.Properties, change)
	}
	return cs, nil
}

// FormatPatchDocument formats JSON Patch operations for cli display.
//
// RFC-0041: the "put resource under management" sub-line was removed.
// formatSimulatedResourceUpdate now emits dedicated `label: <old> -> <new>`
// and `from unmanaged to <stack>` sub-lines on the parent update entry, so
// re-stating "put resource under management" inside `by doing the following:`
// is redundant noise. With an empty patch this function emits nothing — the
// parent's sub-lines convey the transition.
func FormatPatchDocument(node *gtree.Node, patchDoc json.RawMessage, properties json.RawMessage, previousProperties json.RawMessage, refLabels map[string]string) {
	cs, err := ExtractChanges(patchDoc, properties, previousProperties, refLabels)
	if err != nil {
		node.Add(display.Red("Error processing patch document: " + err.Error()))
		return
	}

	for _, change := range cs.Tags {
		node.Add(formatTagChange(change))
	}
	for _, change := range cs.Properties {
		if !change.NoOp {
			node.Add(formatPropertyChange(change))
		}
	}
}

// HasVisibleChanges reports whether formatting the patch document would emit at
// least one change line. A patch whose only op is a suppressed NoOp (a
// force-resent requiredOnUpdate field whose value is unchanged) renders
// nothing, and the caller must not open an empty "by doing the following:"
// block for it.
func HasVisibleChanges(patchDoc json.RawMessage, properties json.RawMessage, previousProperties json.RawMessage, refLabels map[string]string) bool {
	cs, err := ExtractChanges(patchDoc, properties, previousProperties, refLabels)
	if err != nil {
		return true // a malformed patch document surfaces as a visible error line
	}
	if len(cs.Tags) > 0 {
		return true // tag changes always render
	}
	for _, change := range cs.Properties {
		if !change.NoOp {
			return true
		}
	}
	return false
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

// extractTagChange extracts tag information from a patch operation
func extractTagChange(patch patchOperation, previousProperties json.RawMessage) (TagChange, error) {
	change := TagChange{
		Operation: patch.Op,
	}

	switch patch.Op {
	case "add", "replace":
		if patch.Value == nil {
			return change, fmt.Errorf("tag patch has nil value for path %s", patch.Path)
		}

		if strings.HasSuffix(patch.Path, "/Value") {
			// Partial tag value update - e.g. replace /Tags/3/Value
			change.Key = findTagKeyFromPath(patch.Path, previousProperties)
			if change.Key == "" {
				return change, fmt.Errorf("could not extract tag key from path %s", patch.Path)
			}
			change.Value = fmt.Sprintf("%v", patch.Value)
		} else {
			// Complete tag object - e.g. add /Tags/3
			if tagMap, ok := patch.Value.(map[string]any); ok {
				if key, ok := tagMap["Key"].(string); ok {
					change.Key = key
				}
				if val, ok := tagMap["Value"].(string); ok {
					change.Value = val
				}
			}
			if change.Key == "" {
				return change, fmt.Errorf("could not extract tag key from patch value")
			}
		}

		// Get old value for replace operations
		if patch.Op == "replace" && len(previousProperties) > 0 {
			if oldValue := findPreviousTagValue(previousProperties, change.Key); oldValue != "" {
				change.OldValue = oldValue
				change.HasOld = true
			}
		}
	case "remove":
		change.Key = findTagKeyFromPath(patch.Path, previousProperties)
	}

	return change, nil
}

// formatTagChange formats a TagChange for display
func formatTagChange(change TagChange) string {
	switch change.Operation {
	case "add":
		return display.Green(fmt.Sprintf(`add new Tag "%s" with the value "%s"`, change.Key, change.Value))
	case "remove":
		return display.Red(fmt.Sprintf(`remove Tag "%s"`, change.Key))
	case "replace":
		if change.HasOld {
			return display.Gold(fmt.Sprintf(`change Tag "%s" from "%s" to "%s"`, change.Key, change.OldValue, change.Value))
		} else {
			return display.Gold(fmt.Sprintf(`change Tag "%s" to "%s"`, change.Key, change.Value))
		}
	default:
		return display.Grey(fmt.Sprintf(`%s Tag "%s" with the value"%s"`, change.Operation, change.Key, change.Value))
	}
}

// extractPropertyChange extracts property information from a patch operation
func extractPropertyChange(patch patchOperation, props map[string]any, previousProperties json.RawMessage, refLabels map[string]string) (PropertyChange, error) {
	change := PropertyChange{
		Path:      cleanPatchPath(patch.Path),
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

// formatPropertyChange formats a PropertyChange for display
func formatPropertyChange(change PropertyChange) string {
	displayPath := stripArrayIndices(change.Path)

	if change.IsCascadeResolvable {
		// Cascade-update where the new value is provider-assigned (e.g.
		// an AWS ARN). Render the friendly form rather than trying to
		// show `from X to Y` with a missing Y.
		if change.CascadeCurrentValue != "" {
			return display.Gold(fmt.Sprintf(`change property "%s" to point at the new %s (current: "%s")`,
				displayPath, change.CascadeSourceLabel, change.CascadeCurrentValue))
		}
		return display.Gold(fmt.Sprintf(`change property "%s" to point at the new %s`,
			displayPath, change.CascadeSourceLabel))
	}

	switch change.Operation {
	case "add":
		if change.IsOpaque {
			if change.ExistsInPrevious {
				return display.Gold(fmt.Sprintf(`change property "%s" (opaque value changed)`, displayPath))
			}
			if isArrayProperty(change.Path) {
				return display.Green(fmt.Sprintf(`add new entry to "%s" (opaque value)`, displayPath))
			}
			return display.Green(fmt.Sprintf(`add new property "%s" (opaque value)`, displayPath))
		}
		if change.ExistsInPrevious {
			if isArrayProperty(change.Path) {
				return display.Gold(fmt.Sprintf(`change entry "%s" in "%s"`, change.Value, displayPath))
			}
			// Force-resent (writeOnly/requiredOnUpdate) field: it surfaces as an
			// "add" because it was stripped before the diff. Show only the new
			// value — the previous value is a secret we must not leak, and it
			// exists here solely for the no-op suppression test.
			return display.Gold(fmt.Sprintf(`change property "%s" to "%s"`, displayPath, change.Value))
		}
		if isArrayProperty(change.Path) {
			return display.Green(fmt.Sprintf(`add new entry "%s" to "%s"`, change.Value, displayPath))
		} else {
			return display.Green(fmt.Sprintf(`add new property "%s" with the value "%s"`, displayPath, change.Value))
		}
	case "remove":
		if isArrayProperty(change.Path) {
			return display.Red(fmt.Sprintf(`remove entry "%s" from "%s"`, change.Value, displayPath))
		} else {
			return display.Red(fmt.Sprintf(`remove property "%s" from "%s"`, change.Value, displayPath))
		}
	case "replace":
		if change.HasOld {
			if change.IsOpaque {
				return display.Gold(fmt.Sprintf(`change property "%s" (opaque value changed)`, displayPath))
			}
			return display.Gold(fmt.Sprintf(`change property "%s" from "%s" to "%s"`, displayPath, change.OldValue, change.Value))
		} else {
			return display.Gold(fmt.Sprintf(`change property "%s" to "%s"`, displayPath, change.Value))
		}
	default:
		return display.Grey(fmt.Sprintf(`%s property "%s"`, change.Operation, displayPath))
	}
}

// findTagKeyFromPath extracts tag key from path using array index in old properties
func findTagKeyFromPath(path string, previousProps json.RawMessage) string {
	// Extract index
	parts := strings.Split(path, "/")
	if len(parts) > 2 && parts[1] != "Tags" {
		return ""
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil {
		return ""
	}

	var oldProps map[string]any
	if err := json.Unmarshal(previousProps, &oldProps); err != nil {
		return ""
	}
	tags, ok := oldProps["Tags"].([]any)
	if !ok || index >= len(tags) {
		return ""
	}

	if tagMap, ok := tags[index].(map[string]any); ok {
		if key, ok := tagMap["Key"].(string); ok {
			return key
		}
	}

	return ""
}

// findPreviousTagValue extracts the value of a tag with the given key from old properties
func findPreviousTagValue(oldProps json.RawMessage, tagKey string) string {
	var props map[string]any
	if err := json.Unmarshal(oldProps, &props); err != nil {
		return ""
	}

	tags, ok := props["Tags"].([]any)
	if !ok {
		return ""
	}

	for _, tag := range tags {
		if tagMap, ok := tag.(map[string]any); ok {
			if key, ok := tagMap["Key"].(string); ok && key == tagKey {
				if value, ok := tagMap["Value"].(string); ok {
					return value
				}
			}
		}
	}

	return ""
}

// cleanPatchPath converts JSON Pointer paths to more readable format
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

// inferValueFromRef tries to infer what dependency is being added by looking at existing array elements
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

// formatPatchValue formats a patch value for display
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
		// For complex values, serialize to JSON
		if bytes, err := json.Marshal(v); err == nil {
			return string(bytes)
		}
		return formatValueForDisplay(v)
	default:
		return formatValueForDisplay(v)
	}
}

// formatReferenceValue formats a $ref value using provided label mappings
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

// isArrayProperty checks if a path contains array indices
func isArrayProperty(path string) bool {
	return strings.Contains(path, "[") && strings.Contains(path, "]")
}

// stripArrayIndices removes array indices from a path for cleaner display
func stripArrayIndices(path string) string {
	// Remove [index] patterns like "Statement[1]" -> "Statement"
	parts := strings.Split(path, "[")
	if len(parts) == 1 {
		return path // No brackets found
	}

	result := parts[0]
	for i := 1; i < len(parts); i++ {
		// Find closing bracket and append everything after it
		if bracketEnd := strings.Index(parts[i], "]"); bracketEnd != -1 {
			result += parts[i][bracketEnd+1:]
		}
	}
	return result
}

// formatValueForDisplay handles both regular values and Formae Value structures
func formatValueForDisplay(value any) string {
	if valueMap, ok := value.(map[string]any); ok {
		if visibility, ok := valueMap["$visibility"].(string); ok && visibility == "Opaque" {
			return "(opaque value)"
		}

		if val, ok := valueMap["$value"]; ok {
			return fmt.Sprintf("%v", val)
		}
	}

	// Composite values (objects and arrays) must render as JSON to match
	// the add-side formatter (formatPatchValue). Without this, remove ops
	// use Go's default `map[k:v]` syntax while add ops use JSON, making
	// array-set diffs visually uncomparable.
	// Note: json.Marshal sorts map keys alphabetically, so both sides are
	// key-aligned regardless of source order — that alignment is what makes
	// the diff readable.
	switch v := value.(type) {
	case map[string]any, []any:
		if bytes, err := json.Marshal(v); err == nil {
			return string(bytes)
		}
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

// isOpaqueProperty checks if a property path points to an opaque value
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
