// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Type aliases for backward compatibility within this package
type (
	FormaCommandSource  = types.FormaCommandSource
	OperationType       = types.OperationType
	ResourceUpdateState = types.ResourceUpdateState
)

// Re-export constants for backward compatibility
const (
	FormaCommandSourceUser        = types.FormaCommandSourceUser
	FormaCommandSourceSynchronize = types.FormaCommandSourceSynchronize
	FormaCommandSourceDiscovery   = types.FormaCommandSourceDiscovery

	OperationCreate  = types.OperationCreate
	OperationUpdate  = types.OperationUpdate
	OperationDelete  = types.OperationDelete
	OperationRead    = types.OperationRead
	OperationReplace = types.OperationReplace

	ResourceUpdateStateUnknown    = types.ResourceUpdateStateUnknown
	ResourceUpdateStateNotStarted = types.ResourceUpdateStateNotStarted
	ResourceUpdateStatePending    = types.ResourceUpdateStatePending
	ResourceUpdateStateInProgress = types.ResourceUpdateStateInProgress
	ResourceUpdateStateFailed     = types.ResourceUpdateStateFailed
	ResourceUpdateStateSuccess    = types.ResourceUpdateStateSuccess
	ResourceUpdateStateCanceled   = types.ResourceUpdateStateCanceled
	ResourceUpdateStateRejected   = types.ResourceUpdateStateRejected
)

// ResourceUpdate represents an update to a resource in the system. A ResourceUpdate is a logical operation
// that may involve multiple plugin operations. For example a replace operation will involve two plugin
// operations: a delete and a create.
type ResourceUpdate struct {
	Resource                 pkgmodel.Resource         `json:"Resource"`
	ResourceTarget           pkgmodel.Target           `json:"ResourceTarget"`
	ExistingResource         pkgmodel.Resource         `json:"ExistingResource"`
	ExistingTarget           pkgmodel.Target           `json:"ExistingTarget"`
	Operation                OperationType             `json:"Operation"`
	MetaData                 json.RawMessage           `json:"MetaData"`
	State                    ResourceUpdateState       `json:"State"`
	StartTs                  time.Time                 `json:"StartTs"`
	ModifiedTs               time.Time                 `json:"ModifiedTs"`
	Retries                  uint16                    `json:"Retries"`
	Remaining                int16                     `json:"Remaining"`
	Version                  string                    `json:"Version"`
	MostRecentProgressResult resource.ProgressResult   `json:"MostRecentProgressResult"`
	ProgressResult           []resource.ProgressResult `json:"ProgressResult"`
	Source                   FormaCommandSource        `json:"Source,omitempty"`
	RemainingResolvables     []pkgmodel.FormaeURI      `json:"RemainingResolvables,omitempty"`
	StackLabel               string                    `json:"StackLabel,omitempty"`
	GroupID                  string                    `json:"GroupId,omitempty"`
	ReferenceLabels          map[string]string         `json:"ReferenceLabels,omitempty"`
	PreviousProperties       json.RawMessage           `json:"PreviousProperties,omitempty"`
	MatchFilters             []plugin.MatchFilter      `json:"matchFilters,omitempty"` // Declarative filters (any match = exclude)
}

func (ru *ResourceUpdate) URI() pkgmodel.FormaeURI {
	return ru.Resource.URI()
}

func (ru *ResourceUpdate) HasResolvables() bool {
	return len(ru.RemainingResolvables) > 0
}

func (ru *ResourceUpdate) ListResolvables() []pkgmodel.FormaeURI {
	return ru.RemainingResolvables
}

func (ru *ResourceUpdate) ResolveValue(formaeUri pkgmodel.FormaeURI, value string) error {
	properties, err := resolver.ResolvePropertyReferences(formaeUri, ru.Resource.Properties, value)
	if err != nil {
		slog.Error("Failed to resolve dynamic properties", "error", err)
		return fmt.Errorf("failed to resolve dynamic properties: %w", err)
	}
	ru.Resource.Properties = properties
	return nil
}

func (ru *ResourceUpdate) RequiresDelete() bool {
	return ru.Operation == OperationDelete || ru.Operation == OperationReplace
}

func (ru *ResourceUpdate) IsCreate() bool {
	return ru.Operation == OperationCreate || ru.Operation == OperationReplace
}

func (ru *ResourceUpdate) IsUpdate() bool {
	return ru.Operation == OperationUpdate
}

func (ru *ResourceUpdate) IsSync() bool {
	return ru.Operation == OperationRead
}

func (ru *ResourceUpdate) IsDelete() bool {
	return ru.Operation == OperationDelete || ru.Operation == OperationReplace
}

func (ru *ResourceUpdate) HasProgress() bool {
	// The progress is considered if it's not a read
	for _, progress := range ru.ProgressResult {
		if progress.Operation != resource.OperationRead {
			return true
		}
	}

	return false
}

func (ru *ResourceUpdate) FindProgress(operation resource.Operation) (bool, *resource.ProgressResult) {
	for _, progress := range ru.ProgressResult {
		if progress.Operation == operation {
			return true, &progress
		}
	}
	return false, nil
}

func (ru *ResourceUpdate) RecordProgress(progress *resource.ProgressResult) error {
	found := false
	for i, existingProgress := range ru.ProgressResult {
		if existingProgress.Operation == progress.Operation {
			ru.ProgressResult[i] = *progress
			found = true
		}
	}
	if !found {
		ru.ProgressResult = append(ru.ProgressResult, *progress)
		ru.MostRecentProgressResult = *progress // Update the MostRecentProgressResult to the latest progress
	}

	return ru.updateResourceUpdateFromProgress(progress)
}

func (ru *ResourceUpdate) updateResourceUpdateFromProgress(progress *resource.ProgressResult) error {
	ru.UpdateState()
	slog.Debug("Updating resource state for " + string(ru.URI()) + " to " + string(ru.State))

	slog.Debug("Setting NativeID from progress", "nativeID", progress.NativeID, "uri", ru.URI())
	ru.Resource.NativeID = progress.NativeID
	if ru.StartTs.IsZero() {
		ru.StartTs = progress.StartTs
	}
	ru.ModifiedTs = progress.ModifiedTs

	// Only update properties when the operation has finished successfully.
	// Intermediate progress updates may have empty/partial properties which would
	// wipe out $ref structures needed for dependency tracking.
	if !progress.FinishedSuccessfully() {
		return nil
	}

	// For Update operations with a Read sub-operation, update ExistingResource properties
	// instead of Resource properties to preserve user-provided values for metadata/legacy.
	if ru.Operation == OperationUpdate && progress.Operation == resource.OperationRead {
		err := ru.updateExistingResourceProperties(string(progress.ResourceProperties))
		if err != nil {
			slog.Error("Failed to update resource properties", "error", err)
			return err
		}
	} else {
		err := ru.updateResourceProperties(string(progress.ResourceProperties))
		if err != nil {
			slog.Error("Failed to update resource properties", "error", err)
			return err
		}
	}

	return nil
}

func (ru *ResourceUpdate) Reject() {
	ru.State = ResourceUpdateStateRejected
	ru.ModifiedTs = util.TimeNow()
}

func (ru *ResourceUpdate) MarkAsSuccess() {
	ru.State = ResourceUpdateStateSuccess
	ru.ModifiedTs = util.TimeNow()
}

func (ru *ResourceUpdate) MarkAsFailed() {
	ru.State = ResourceUpdateStateFailed
	ru.ModifiedTs = util.TimeNow()
}

func (ru *ResourceUpdate) MostRecentFailureMessage() string {
	// Account for non-recoverable errors or max attempts reached
	if msg := ru.FilterProgressMessage(func(p resource.ProgressResult) bool {
		return p.Failed() && p.StatusMessage != ""
	}); msg != "" {
		return msg
	}

	// Account for recoverable errors
	return ru.FilterProgressMessage(func(p resource.ProgressResult) bool {
		return p.OperationStatus == resource.OperationStatusFailure && p.StatusMessage != ""
	})
}

func (ru *ResourceUpdate) MostRecentStatusMessage() string {
	// Find most recent non-empty status message
	for i := len(ru.ProgressResult) - 1; i >= 0; i-- {
		if ru.ProgressResult[i].StatusMessage != "" {
			return ru.ProgressResult[i].StatusMessage
		}
	}
	return ""
}

func (ru *ResourceUpdate) FilterProgressMessage(filter func(resource.ProgressResult) bool) string {
	for _, progress := range ru.ProgressResult {
		if filter(progress) {
			return progress.StatusMessage
		}
	}
	return ""
}

// UpdateState derives the ResourceUpdate state from its ProgressResult.
// This is used during restart recovery to restore the correct state based on
// the progress that was persisted before the restart.
func (ru *ResourceUpdate) UpdateState() {
	if len(ru.ProgressResult) == 0 {
		ru.State = ResourceUpdateStateNotStarted
		return
	}
	ops := ru.requiredOperations()
	if len(ru.ProgressResult) < len(ops) {
		ru.State = ResourceUpdateStateInProgress
		return
	}
	finalState := ResourceUpdateStateSuccess
	for _, progress := range ru.ProgressResult {
		if progress.Failed() {
			finalState = ResourceUpdateStateFailed
			break
		} else if progress.OperationStatus != resource.OperationStatusSuccess {
			finalState = ResourceUpdateStateInProgress
		}
	}
	ru.State = finalState
}

func (ru *ResourceUpdate) requiredOperations() []resource.Operation {
	switch ru.Operation {
	case OperationRead:
		return []resource.Operation{resource.OperationRead}
	case OperationCreate:
		return []resource.Operation{resource.OperationCreate}
	case OperationDelete:
		return []resource.Operation{resource.OperationRead, resource.OperationDelete}
	case OperationUpdate:
		return []resource.Operation{resource.OperationRead, resource.OperationUpdate}
	case OperationReplace:
		return []resource.Operation{resource.OperationRead, resource.OperationDelete, resource.OperationCreate}
	default:
		slog.Error("Unknown operation type", "operation", ru.Operation)
		return nil
	}
}

func (ru *ResourceUpdate) updateResourceProperties(incomingProperties string) error {
	return ru.updateProperties(incomingProperties, &ru.Resource.Properties, &ru.Resource.ReadOnlyProperties)
}

func (ru *ResourceUpdate) updateExistingResourceProperties(incomingProperties string) error {
	return ru.updateProperties(incomingProperties, &ru.ExistingResource.Properties, &ru.ExistingResource.ReadOnlyProperties)
}

// updateProperties splits the properties from the plugin read result into regular and read-only,
// based on the resource schema fields, and merges $ref structures from the existing target properties.
// This preserves $ref structures needed for destroy dependency tracking and PKL extraction.
func (ru *ResourceUpdate) updateProperties(incomingProperties string, targetProperties, targetReadOnlyProperties *json.RawMessage) error {
	if incomingProperties == "" {
		slog.Debug("No properties to split for resource", "uri", ru.URI())
		incomingProperties = "{}"
	}
	var allProperties map[string]any
	if err := json.Unmarshal([]byte(incomingProperties), &allProperties); err != nil {
		slog.Error("Failed to unmarshal resource properties", "error", err)
		return err
	}

	// Build a set of schema fields for quick lookup
	fieldsSet := make(map[string]struct{})
	for _, field := range ru.Resource.Schema.Fields {
		fieldsSet[field] = struct{}{}
	}

	// Split properties into regular and read-only
	properties := make(map[string]any)
	readOnlyProperties := make(map[string]any)
	for k, v := range allProperties {
		if _, ok := fieldsSet[k]; ok {
			properties[k] = v
		} else {
			readOnlyProperties[k] = v
		}
	}

	// Marshal back to JSON
	propertiesJson, err := json.Marshal(properties)
	if err != nil {
		slog.Error("Failed to marshal regular properties", "error", err)
		return err
	}

	// Merge refs from user-provided properties to preserve $ref structures
	mergedProps, mergeErr := mergeRefsPreservingUserRefs(*targetProperties, propertiesJson, ru.Resource.Schema)
	if mergeErr != nil {
		slog.Error("Failed to merge refs into properties", "error", mergeErr)
		return mergeErr
	}
	*targetProperties = mergedProps

	if len(readOnlyProperties) > 0 {
		if readOnlyPropertiesJson, err := json.Marshal(readOnlyProperties); err == nil {
			*targetReadOnlyProperties = readOnlyPropertiesJson
		} else {
			slog.Error("Failed to marshal read-only properties", "error", err)
			return err
		}
	} else {
		*targetReadOnlyProperties = nil
	}

	return nil
}

// mergeRefsPreservingUserRefs merges user-provided properties with plugin-returned properties.
// This function handles special "$ref" objects (resolvable references) by preserving the user's
// $ref structure while updating the $value from the plugin. The plugin properties serve as the
// base, with selective preservation of user values.
//
// The schema parameter provides field hints that control how arrays are matched:
// - Default/Set: elements are matched by value (JSON equality after flattening $refs)
// - Array: elements are matched by index position
// - EntitySet: elements are matched by a key field (e.g., Tags by "Key")
func mergeRefsPreservingUserRefs(userProperties, pluginProperties json.RawMessage, schema pkgmodel.Schema) (json.RawMessage, error) {
	if userProperties == nil {
		userProperties = []byte("{}")
	}
	if pluginProperties == nil {
		pluginProperties = []byte("{}")
	}

	userParsed := gjson.ParseBytes(userProperties)
	pluginParsed := gjson.ParseBytes(pluginProperties)

	// Start with plugin properties as base and merge in user values where appropriate
	result := string(pluginProperties)

	merger := &propertyMerger{
		userRoot:   userParsed,
		pluginRoot: pluginParsed,
		result:     &result,
		schema:     schema,
	}

	merger.mergeValue("", userParsed, pluginParsed)

	return json.RawMessage(result), nil
}

// propertyMerger handles the recursive merging of JSON properties
type propertyMerger struct {
	userRoot   gjson.Result
	pluginRoot gjson.Result
	result     *string
	schema     pkgmodel.Schema
}

// mergeValue recursively merges a value at the given path
func (m *propertyMerger) mergeValue(path string, userVal, pluginVal gjson.Result) {
	if userVal.IsObject() {
		m.mergeObject(path, userVal, pluginVal)
	} else if userVal.IsArray() {
		m.mergeArray(path, userVal, pluginVal)
	} else {
		m.mergePrimitive(path, userVal)
	}
}

// mergeObject handles merging of object values
func (m *propertyMerger) mergeObject(path string, userVal, pluginVal gjson.Result) {
	// Check if this is a $ref object (resolvable reference)
	if userRef := userVal.Get("$ref"); userRef.Exists() {
		m.mergeRefObject(path, userVal, pluginVal)
		return
	}

	// Not a $ref object - recursively merge each field
	userVal.ForEach(func(key, val gjson.Result) bool {
		childPath := m.buildChildPath(path, key.String())
		pluginChildVal := pluginVal.Get(key.String())
		m.mergeValue(childPath, val, pluginChildVal)
		return true
	})
}

// mergeRefObject handles merging of $ref objects (resolvable references)
func (m *propertyMerger) mergeRefObject(path string, userVal, pluginVal gjson.Result) {
	cleanPath := m.cleanPath(path)

	// Determine which $value to use
	userValue := userVal.Get("$value")
	valueToSet := m.selectRefValue(userValue, pluginVal)

	// Preserve user's $ref structure and update the $value
	updatedRef, _ := sjson.Set(userVal.Raw, "$value", valueToSet)
	*m.result, _ = sjson.SetRaw(*m.result, cleanPath, updatedRef)
}

// selectRefValue determines which value to use for a $ref object's $value field
func (m *propertyMerger) selectRefValue(userValue gjson.Result, pluginVal gjson.Result) any {
	// If plugin value is also a $ref object, use its $value
	if pluginVal.IsObject() && pluginVal.Get("$ref").Exists() {
		pluginValue := pluginVal.Get("$value")
		return m.preferNonNullValue(userValue, pluginValue)
	}

	// Plugin value is a simple value - use it as the $value
	return m.preferNonNullValue(userValue, pluginVal)
}

// preferNonNullValue returns the user value if it exists and is non-null/non-empty,
// otherwise returns the plugin value
func (m *propertyMerger) preferNonNullValue(userValue, pluginValue gjson.Result) any {
	userHasValue := userValue.Exists() && userValue.Value() != nil
	pluginIsNullOrEmpty := pluginValue.Value() == nil || pluginValue.String() == ""

	if userHasValue && pluginIsNullOrEmpty {
		return userValue.Value()
	}
	return pluginValue.Value()
}

// mergeArray handles merging of array values
// Elements are matched based on the updateMethod hint for the field:
// - Default/Set: match by value (JSON equality after flattening $refs)
// - Array: match by index position
// - EntitySet: match by key field (e.g., Tags by "Key")
func (m *propertyMerger) mergeArray(path string, userVal, pluginVal gjson.Result) {
	userArray := userVal.Array()
	pluginArray := pluginVal.Array()

	// Get the field name from the path (e.g., "networks" from "networks" or "networks.0.uuid")
	fieldName := m.getFieldNameFromPath(path)
	hint := m.schema.Hints[fieldName]

	// Track which user elements have been matched (to avoid double-matching)
	matchedUserIndices := make(map[int]bool)

	// Phase 1: Match plugin elements with user elements that have concrete values
	type pendingMatch struct {
		pluginIdx  int
		pluginElem gjson.Result
	}
	var unmatchedPluginElements []pendingMatch

	for i, pluginElem := range pluginArray {
		childPath := fmt.Sprintf("%s.%d", path, i)

		// Find matching user element based on update method
		matchedUserElem, matchedIdx := m.findMatchingUserElementWithIndex(userArray, pluginElem, hint, matchedUserIndices)

		if matchedIdx >= 0 {
			matchedUserIndices[matchedIdx] = true
			m.mergeValue(childPath, matchedUserElem, pluginElem)
		} else {
			// No match found - save for phase 2
			unmatchedPluginElements = append(unmatchedPluginElements, pendingMatch{i, pluginElem})
		}
	}

	// Phase 2: For unmatched plugin elements, try to pair with user elements that have $ref-without-$value
	// These couldn't be matched in phase 1 because they don't have a concrete value yet
	for _, pending := range unmatchedPluginElements {
		childPath := fmt.Sprintf("%s.%d", path, pending.pluginIdx)

		// Look for unmatched user element with $ref but no $value
		matchedUserElem := m.findUserElementWithUnresolvedRef(userArray, matchedUserIndices)
		if matchedUserElem.matchedIdx >= 0 {
			matchedUserIndices[matchedUserElem.matchedIdx] = true
		}

		m.mergeValue(childPath, matchedUserElem.elem, pending.pluginElem)
	}
}

// getFieldNameFromPath extracts the top-level field name from a JSON path
// e.g., "networks" -> "networks", "networks.0.uuid" -> "networks"
func (m *propertyMerger) getFieldNameFromPath(path string) string {
	// Remove leading dot if present
	if path != "" && path[0] == '.' {
		path = path[1:]
	}

	// Find the first dot or bracket
	for i, c := range path {
		if c == '.' || c == '[' {
			return path[:i]
		}
	}
	return path
}

// findMatchingUserElementWithIndex finds a user array element that matches the plugin element,
// returning both the element and its index. It skips elements already matched (in excludeIndices).
// Returns index -1 if no match found.
func (m *propertyMerger) findMatchingUserElementWithIndex(userArray []gjson.Result, pluginElem gjson.Result, hint pkgmodel.FieldHint, excludeIndices map[int]bool) (gjson.Result, int) {
	if len(userArray) == 0 {
		return gjson.Result{Type: gjson.Null}, -1
	}

	switch hint.UpdateMethod {
	case pkgmodel.FieldUpdateMethodArray:
		// Array: This should match by index, but we don't have the index here
		return gjson.Result{Type: gjson.Null}, -1

	case pkgmodel.FieldUpdateMethodEntitySet:
		// EntitySet: match by key field
		if hint.IndexField == "" {
			return gjson.Result{Type: gjson.Null}, -1
		}
		pluginKeyValue := pluginElem.Get(hint.IndexField).String()
		for i, userElem := range userArray {
			if excludeIndices[i] {
				continue
			}
			userKeyValue := m.flattenRefValue(userElem.Get(hint.IndexField))
			if userKeyValue == pluginKeyValue {
				return userElem, i
			}
		}
		return gjson.Result{Type: gjson.Null}, -1

	default:
		// Default/Set: match by value (JSON equality after flattening $refs)
		// Only match elements that have concrete values (not $ref without $value)
		pluginFlattened := m.flattenElement(pluginElem)
		for i, userElem := range userArray {
			if excludeIndices[i] {
				continue
			}
			// Skip elements that contain $ref without $value - these will be handled in phase 2
			if m.hasUnresolvedRef(userElem) {
				continue
			}
			userFlattened := m.flattenElement(userElem)
			if userFlattened == pluginFlattened {
				return userElem, i
			}
		}
		return gjson.Result{Type: gjson.Null}, -1
	}
}

// unresolvedRefMatch holds the result of finding a user element with unresolved $ref
type unresolvedRefMatch struct {
	elem       gjson.Result
	matchedIdx int
}

// findUserElementWithUnresolvedRef finds the first unmatched user element that contains
// a $ref without a $value (unresolved reference). These elements couldn't be matched
// by value comparison because their value isn't known yet.
func (m *propertyMerger) findUserElementWithUnresolvedRef(userArray []gjson.Result, excludeIndices map[int]bool) unresolvedRefMatch {
	for i, userElem := range userArray {
		if excludeIndices[i] {
			continue
		}
		if m.hasUnresolvedRef(userElem) {
			return unresolvedRefMatch{elem: userElem, matchedIdx: i}
		}
	}
	return unresolvedRefMatch{elem: gjson.Result{Type: gjson.Null}, matchedIdx: -1}
}

// hasUnresolvedRef checks if an element contains any $ref object without a $value.
// These are references where the value isn't known yet (e.g., from PKL translation).
func (m *propertyMerger) hasUnresolvedRef(elem gjson.Result) bool {
	if !elem.IsObject() {
		return false
	}

	hasUnresolved := false
	elem.ForEach(func(key, val gjson.Result) bool {
		if val.IsObject() && val.Get("$ref").Exists() && !val.Get("$value").Exists() {
			hasUnresolved = true
			return false // stop iteration
		}
		return true
	})
	return hasUnresolved
}

// flattenRefValue extracts the actual value from a potential $ref object
// If the value is a $ref object, returns the $value; otherwise returns the string value
func (m *propertyMerger) flattenRefValue(val gjson.Result) string {
	if val.IsObject() && val.Get("$ref").Exists() {
		return val.Get("$value").String()
	}
	return val.String()
}

// flattenElement creates a comparable string representation of an element
// by flattening all $ref objects to their $value
func (m *propertyMerger) flattenElement(elem gjson.Result) string {
	if !elem.IsObject() {
		return elem.Raw
	}

	// For objects, we need to flatten recursively
	result := make(map[string]any)
	elem.ForEach(func(key, val gjson.Result) bool {
		if val.IsObject() && val.Get("$ref").Exists() {
			// This is a $ref object - use the $value
			result[key.String()] = val.Get("$value").Value()
		} else {
			result[key.String()] = val.Value()
		}
		return true
	})

	// Marshal back to JSON for comparison
	jsonBytes, _ := json.Marshal(result)
	return string(jsonBytes)
}

// mergePrimitive handles merging of primitive values
// Primitive values from user are kept only if they don't exist in the plugin response
func (m *propertyMerger) mergePrimitive(path string, userVal gjson.Result) {
	cleanPath := m.cleanPath(path)
	pluginValue := m.pluginRoot.Get(cleanPath)

	// Only set user value if plugin doesn't have this field
	if !pluginValue.Exists() {
		*m.result, _ = sjson.SetRaw(*m.result, cleanPath, userVal.Raw)
	}
}

// buildChildPath constructs a JSON path for a child field
func (m *propertyMerger) buildChildPath(parentPath, fieldName string) string {
	if parentPath == "" {
		return fieldName
	}
	return parentPath + "." + fieldName
}

// cleanPath removes leading dot from a path if present
func (m *propertyMerger) cleanPath(path string) string {
	if path != "" && path[0] == '.' {
		return path[1:]
	}
	return path
}
