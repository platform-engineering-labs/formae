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
	"github.com/platform-engineering-labs/formae/internal/constants"
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
	Path      string
	Value     string
	OldValue  string
	Operation string
	HasOld    bool
	IsRef     bool
	IsOpaque  bool
}

// FormatPatchDocument formats JSON Patch operations for cli display
func FormatPatchDocument(node *gtree.Node, patchDoc json.RawMessage, properties json.RawMessage, previousProperties json.RawMessage, refLabels map[string]string, oldStackName string) {
	// Check if we're bringing a resource under management
	// This needs to happen before early returns so it works for tag-less resources with empty patches
	isBringingUnderManagement := oldStackName == constants.UnmanagedStack

	patches := parsePatchOperations(patchDoc, node)
	if patches == nil {
		// Even with no patches, show the management message if applicable
		if isBringingUnderManagement {
			node.Add(display.LightBlue("put resource under management"))
		}
		return
	}

	props := parseProperties(properties, node)
	if props == nil {
		// Even with no properties, show the management message if applicable
		if isBringingUnderManagement {
			node.Add(display.LightBlue("put resource under management"))
		}
		return
	}

	for _, patch := range patches {
		formatSinglePatch(node, patch, props, previousProperties, refLabels)
	}

	// Use OldStackName to detect bringing under management instead of patch analysis
	// This works for both taggable and tag-less resources
	if isBringingUnderManagement {
		node.Add(display.LightBlue("put resource under management"))
	}
}

func parsePatchOperations(patchDoc json.RawMessage, node *gtree.Node) []patchOperation {
	var patches []map[string]any
	if err := json.Unmarshal(patchDoc, &patches); err != nil {
		node.Add(display.Red("Error parsing patch document: " + err.Error()))
		return nil
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
	return operations
}

func parseProperties(properties json.RawMessage, node *gtree.Node) map[string]any {
	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		node.Add(display.Red("Error parsing properties document: " + err.Error()))
		return nil
	}
	return props
}

func formatSinglePatch(node *gtree.Node, patch patchOperation, props map[string]any, previousProperties json.RawMessage, refLabels map[string]string) {
	if strings.Contains(patch.Path, "/Tags/") {
		if change, err := extractTagChange(patch, previousProperties); err != nil {
			node.Add(display.Red(fmt.Sprintf("Error processing tag patch: %v", err)))
		} else {
			node.Add(formatTagChange(change))
		}
		return
	}

	if change, err := extractPropertyChange(patch, props, previousProperties, refLabels); err != nil {
		node.Add(display.Red(fmt.Sprintf("Error processing property patch: %v", err)))
	} else {
		node.Add(formatPropertyChange(change))
	}
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

	// Set IsOpaque after we have the path
	propsBytes, _ := json.Marshal(props)
	change.IsOpaque = isOpaqueProperty(change.Path, previousProperties) || isOpaqueProperty(change.Path, json.RawMessage(propsBytes))

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
	} else {
		change.Value = "\"(empty)\""
	}

	if patch.Op == "replace" && len(previousProperties) > 0 {
		if oldValue := extractPreviousValue(previousProperties, patch.Path); oldValue != "" {
			change.OldValue = oldValue
			change.HasOld = true
		}
	}

	return change, nil
}

// formatPropertyChange formats a PropertyChange for display
func formatPropertyChange(change PropertyChange) string {
	displayPath := stripArrayIndices(change.Path)

	switch change.Operation {
	case "add":
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

	return fmt.Sprintf("%v", value)
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
