// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// ViolationKind classifies the type of invariant violation.
type ViolationKind int

const (
	ViolationPhantomResource        ViolationKind = iota // in inventory but not in cloud
	ViolationOrphanedResource                            // in cloud but not in inventory
	ViolationPropertyMismatch                            // inventory and cloud properties differ
	ViolationCommandNotTerminal                          // command not in terminal state
	ViolationResolvableNotResolved                       // resolvable $ref not properly resolved
	ViolationModelInventoryMismatch                      // model expected state doesn't match inventory
)

// Violation describes a single invariant violation.
type Violation struct {
	Kind    ViolationKind
	Message string
}

// CommandState is a simplified view of a command's status for invariant checking.
type CommandState struct {
	ID    string
	State string
}

// CheckInvariants checks the core correctness invariants against actual state.
// It returns any violations found.
//
// ignoreNativeIDs contains native IDs of cloud resources that are expected to
// exist in the cloud but NOT in inventory yet (e.g. out-of-band created
// unmanaged resources that have not been discovered).
//
// Invariants checked:
//  1. No phantom resources: every resource in inventory exists in cloud state
//  2. No orphaned cloud resources: every cloud resource is tracked in inventory
//  3. Property consistency: inventory properties match cloud state properties
func CheckInvariants(inventory []pkgmodel.Resource, cloudState map[string]testcontrol.CloudStateEntry, ignoreNativeIDs map[string]bool, ignoreManagedDriftNativeIDs map[string]bool) []Violation {
	var violations []Violation

	// Build lookup of inventory resources by NativeID
	inventoryByNativeID := make(map[string]pkgmodel.Resource, len(inventory))
	for _, res := range inventory {
		if res.NativeID != "" {
			inventoryByNativeID[res.NativeID] = res
		}
	}

	// Invariant 1: No phantom resources
	// Every resource in inventory should exist in cloud state
	for _, res := range inventory {
		if res.NativeID == "" {
			continue
		}
		if ignoreManagedDriftNativeIDs[res.NativeID] {
			continue
		}
		if _, ok := cloudState[res.NativeID]; !ok {
			violations = append(violations, Violation{
				Kind:    ViolationPhantomResource,
				Message: fmt.Sprintf("phantom resource: %s (%s) in inventory but not in cloud state", res.Label, res.NativeID),
			})
		}
	}

	// Invariant 2: No orphaned cloud resources
	// Every cloud resource should be tracked in inventory (or be a known
	// unmanaged resource from the model's tracking set).
	for nativeID, entry := range cloudState {
		if _, ok := inventoryByNativeID[nativeID]; !ok {
			if ignoreNativeIDs[nativeID] {
				continue
			}
			violations = append(violations, Violation{
				Kind:    ViolationOrphanedResource,
				Message: fmt.Sprintf("orphaned resource: %s (%s) in cloud state but not in inventory", nativeID, entry.ResourceType),
			})
		}
	}

	// Invariant 3: Property consistency
	// For resources present in both, properties should match
	for nativeID, cloudEntry := range cloudState {
		if ignoreManagedDriftNativeIDs[nativeID] {
			continue
		}
		invRes, ok := inventoryByNativeID[nativeID]
		if !ok {
			continue // already reported as orphaned or ignored
		}
		invProps := string(invRes.Properties)
		if invProps != "" && cloudEntry.Properties != "" && !jsonEqual(invProps, cloudEntry.Properties) {
			violations = append(violations, Violation{
				Kind: ViolationPropertyMismatch,
				Message: fmt.Sprintf("property mismatch for %s: inventory=%s cloud=%s",
					nativeID, invProps, cloudEntry.Properties),
			})
		}
	}

	return violations
}

// CheckReconcileProperties verifies that after a successful reconcile, the
// inventory properties for the given resources exactly match the desired
// properties from the forma (after JSON normalization).
func CheckReconcileProperties(inventory []pkgmodel.Resource, resourceLabels []string, desiredProps map[string]string) []Violation {
	var violations []Violation

	invByLabel := make(map[string]pkgmodel.Resource)
	for _, res := range inventory {
		invByLabel[res.Label] = res
	}

	for _, label := range resourceLabels {
		res, ok := invByLabel[label]
		if !ok {
			// Resource might not exist yet (e.g. first create). Skip.
			continue
		}

		desired, hasDes := desiredProps[label]
		if !hasDes {
			continue
		}

		if !jsonEqual(string(res.Properties), desired) {
			violations = append(violations, Violation{
				Kind: ViolationPropertyMismatch,
				Message: fmt.Sprintf("reconcile property mismatch for %s: inventory=%s desired=%s",
					label, string(res.Properties), desired),
			})
		}
	}
	return violations
}

// CheckPatchProperties verifies that after a successful patch, the inventory
// properties contain all elements from the submitted forma (additive check).
func CheckPatchProperties(inventory []pkgmodel.Resource, resourceLabels []string, submittedProps map[string]string) []Violation {
	var violations []Violation

	invByLabel := make(map[string]pkgmodel.Resource)
	for _, res := range inventory {
		invByLabel[res.Label] = res
	}

	for _, label := range resourceLabels {
		res, ok := invByLabel[label]
		if !ok {
			continue
		}

		submitted, hasSub := submittedProps[label]
		if !hasSub {
			continue
		}

		if errs := checkSuperset(string(res.Properties), submitted); len(errs) > 0 {
			for _, e := range errs {
				violations = append(violations, Violation{
					Kind:    ViolationPropertyMismatch,
					Message: fmt.Sprintf("patch property mismatch for %s: %s", label, e),
				})
			}
		}
	}
	return violations
}

// jsonEqual compares two JSON strings for semantic equality (ignoring key order,
// whitespace, empty arrays, and resolvable reference wrappers). Empty arrays are
// treated as equivalent to missing fields since the agent may strip them during
// property processing. Resolvable references {"$ref":"...","$value":"..."} are
// flattened to just their $value before comparison.
func jsonEqual(a, b string) bool {
	var aVal, bVal map[string]any
	if err := json.Unmarshal([]byte(a), &aVal); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bVal); err != nil {
		return false
	}
	flattenResolvables(aVal)
	flattenResolvables(bVal)
	stripEmptyArrays(aVal)
	stripEmptyArrays(bVal)
	aBytes, _ := json.Marshal(aVal)
	bBytes, _ := json.Marshal(bVal)
	return string(aBytes) == string(bBytes)
}

// stripEmptyArrays removes keys whose values are empty arrays from a JSON map.
func stripEmptyArrays(m map[string]any) {
	for k, v := range m {
		if arr, ok := v.([]any); ok && len(arr) == 0 {
			delete(m, k)
		}
	}
}

// flattenResolvables recursively converts resolvable wrapper objects to just
// their $value. Two wrapper formats exist:
//   - {"$ref":"formae://...","$value":"..."} — legacy/output format
//   - {"$res":true,"$label":"...","$type":"...","$stack":"...","$property":"...","$value":"..."} — input format
//
// This normalizes inventory properties (which retain the wrapper structure)
// for comparison with expected plain values.
func flattenResolvables(m map[string]any) {
	for k, v := range m {
		switch vv := v.(type) {
		case map[string]any:
			if isResolvableWrapper(vv) {
				if val, hasVal := vv["$value"]; hasVal {
					m[k] = val
					continue
				}
				m[k] = ""
				continue
			}
			flattenResolvables(vv)
		case []any:
			for i, elem := range vv {
				if elemMap, ok := elem.(map[string]any); ok {
					if isResolvableWrapper(elemMap) {
						if val, hasVal := elemMap["$value"]; hasVal {
							vv[i] = val
							continue
						}
						vv[i] = ""
						continue
					}
					flattenResolvables(elemMap)
				}
			}
		}
	}
}

// isResolvableWrapper returns true if the map is a resolvable reference wrapper
// (either {"$ref":...} or {"$res":true,...}).
func isResolvableWrapper(m map[string]any) bool {
	if _, hasRef := m["$ref"]; hasRef {
		return true
	}
	if res, hasRes := m["$res"]; hasRes {
		if b, ok := res.(bool); ok && b {
			return true
		}
	}
	return false
}

// checkSuperset verifies that actualJSON is a superset of submittedJSON:
// - Scalar fields in submitted must match actual
// - Array fields: every element in submitted must appear in actual
// Returns a list of mismatch descriptions, or nil if the check passes.
func checkSuperset(actualJSON, submittedJSON string) []string {
	var actual, submitted map[string]any
	if err := json.Unmarshal([]byte(actualJSON), &actual); err != nil {
		return []string{fmt.Sprintf("failed to parse actual: %v", err)}
	}
	if err := json.Unmarshal([]byte(submittedJSON), &submitted); err != nil {
		return []string{fmt.Sprintf("failed to parse submitted: %v", err)}
	}
	flattenResolvables(actual)
	flattenResolvables(submitted)

	var errs []string
	for key, subVal := range submitted {
		actVal, ok := actual[key]
		if !ok {
			// A missing field is fine if the submitted value is an empty array,
			// since the agent strips empty arrays during property processing.
			if sv, isArr := subVal.([]any); isArr && len(sv) == 0 {
				continue
			}
			errs = append(errs, fmt.Sprintf("field %s missing from actual", key))
			continue
		}

		switch sv := subVal.(type) {
		case []any:
			av, ok := actVal.([]any)
			if !ok {
				errs = append(errs, fmt.Sprintf("field %s: expected array in actual", key))
				continue
			}
			for _, elem := range sv {
				if !containsJSON(av, elem) {
					errs = append(errs, fmt.Sprintf("field %s: element %v not found in actual %v", key, elem, av))
				}
			}
		default:
			// Scalar comparison
			subBytes, _ := json.Marshal(subVal)
			actBytes, _ := json.Marshal(actVal)
			if string(subBytes) != string(actBytes) {
				errs = append(errs, fmt.Sprintf("field %s: submitted=%s actual=%s", key, subBytes, actBytes))
			}
		}
	}
	return errs
}

// containsJSON checks if the given array contains an element that is JSON-equal
// to the target.
func containsJSON(arr []any, target any) bool {
	targetBytes, _ := json.Marshal(target)
	for _, elem := range arr {
		elemBytes, _ := json.Marshal(elem)
		if string(elemBytes) == string(targetBytes) {
			return true
		}
	}
	return false
}

// CheckCommandCompleteness verifies that all commands are in terminal states.
func CheckCommandCompleteness(commands []CommandState) []Violation {
	var violations []Violation
	for _, cmd := range commands {
		if cmd.State != "Success" && cmd.State != "Failed" && cmd.State != "Canceled" {
			violations = append(violations, Violation{
				Kind:    ViolationCommandNotTerminal,
				Message: fmt.Sprintf("command %s in non-terminal state: %s", cmd.ID, cmd.State),
			})
		}
	}
	return violations
}

// findByLabel returns a pointer to the first resource in inventory with the
// given label, or nil if not found.
func findByLabel(inventory []pkgmodel.Resource, label string) *pkgmodel.Resource {
	for i, res := range inventory {
		if res.Label == label {
			return &inventory[i]
		}
	}
	return nil
}

// checkParentIdValue verifies that res.Properties["ParentId"] resolves to the
// Name of parentRes. Returns a Violation if the check fails, or nil on success.
func checkParentIdValue(res pkgmodel.Resource, parentRes pkgmodel.Resource) *Violation {
	var props map[string]any
	if err := json.Unmarshal(res.Properties, &props); err != nil {
		v := Violation{
			Kind:    ViolationResolvableNotResolved,
			Message: fmt.Sprintf("failed to parse properties for %s: %v", res.Label, err),
		}
		return &v
	}

	parentID, ok := props["ParentId"]
	if !ok {
		v := Violation{
			Kind:    ViolationResolvableNotResolved,
			Message: fmt.Sprintf("resource %s missing ParentId field", res.Label),
		}
		return &v
	}

	resolvedValue := extractResolvedValue(parentID)
	if resolvedValue != parentRes.Label {
		v := Violation{
			Kind: ViolationResolvableNotResolved,
			Message: fmt.Sprintf("resource %s: ParentId resolved to %q, expected %q",
				res.Label, resolvedValue, parentRes.Label),
		}
		return &v
	}
	return nil
}

// CheckResolvableProperties verifies that child/grandchild resources in
// inventory have their ParentId correctly resolved to the parent's Name.
//
// For within-stack children/grandchildren the parent is looked up in inventory.
// For cross-stack children (pool.IsCrossStack(idx) == true) the parent is
// looked up in providerInventory using the provider stack label. If
// providerInventory is nil, cross-stack checks are skipped.
func CheckResolvableProperties(inventory []pkgmodel.Resource, pool *ResourcePool,
	stackLabel string, appliedIDs []int,
	providerInventory []pkgmodel.Resource, providerStackLabel string) []Violation {
	var violations []Violation

	// Build inventory lookup by label
	invByLabel := make(map[string]pkgmodel.Resource)
	for _, res := range inventory {
		invByLabel[res.Label] = res
	}

	for _, idx := range appliedIDs {
		// Only check child/grandchild resources (those with a parent)
		if pool.IsParent(idx) {
			continue
		}

		if pool.IsCrossStack(idx) {
			// Cross-stack: parent lives in the provider stack's inventory.
			if providerInventory == nil {
				continue
			}
			label := pool.LabelForStack(stackLabel, idx)
			res, ok := invByLabel[label]
			if !ok {
				continue // Resource not in inventory yet, skip
			}
			parentLabel := pool.LabelForStack(providerStackLabel, pool.Slots[idx].CrossStackParentSlot)
			parentRes := findByLabel(providerInventory, parentLabel)
			if parentRes == nil {
				continue // Parent not in provider inventory (may have failed), skip
			}
			if v := checkParentIdValue(res, *parentRes); v != nil {
				violations = append(violations, *v)
			}
			continue
		}

		// Within-stack: parent lives in the same inventory.
		label := pool.LabelForStack(stackLabel, idx)
		res, ok := invByLabel[label]
		if !ok {
			continue // Resource not in inventory yet, skip
		}

		parentLabel := pool.LabelForStack(stackLabel, pool.Slots[idx].ParentIndex)
		parentRes, ok := invByLabel[parentLabel]
		if !ok {
			continue // Parent not in inventory yet, skip
		}

		if v := checkParentIdValue(res, parentRes); v != nil {
			violations = append(violations, *v)
		}
	}
	return violations
}

// extractResolvedValue extracts the resolved value from a property value.
// If it's a resolvable wrapper object (with $value key), returns the $value as string.
// If it's a plain string, returns it as-is.
func extractResolvedValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		if resolved, ok := val["$value"]; ok {
			if s, ok := resolved.(string); ok {
				return s
			}
		}
	}
	return ""
}

// CheckModelVsInventory verifies that the model's expected resource states
// match what the agent actually has in inventory. For each resource tracked
// in the model, it checks whether its existence in inventory is consistent
// with the model's State.
func CheckModelVsInventory(model *StateModel, inventory []pkgmodel.Resource) []Violation {
	var violations []Violation
	expectedExistingKeys := make(map[string]bool)

	// Build a lookup of inventory resources by (stack, label).
	inventoryByKey := make(map[string]pkgmodel.Resource, len(inventory))
	for _, res := range inventory {
		key := res.Stack + "/" + res.Label
		inventoryByKey[key] = res
	}

	for s := range model.Stacks {
		stack := &model.Stacks[s]
		for idx, res := range stack.Resources {
			label := model.LabelForResource(s, idx)
			resourceType := model.TypeForResource(idx)
			if model.HasPendingManagedDriftAffectingSlot(s, idx) {
				continue
			}

			key := stack.Label + "/" + label
			invRes, existsInInventory := inventoryByKey[key]
			if res.State == StateExists {
				expectedExistingKeys[key] = true
			}

			var actual ResourceState
			if existsInInventory {
				actual = StateExists
			} else {
				actual = StateNotExist
			}

			if res.State != actual {
				stateStr := "NotExist"
				if actual == StateExists {
					stateStr = "Exists"
				}
				expectedStr := "NotExist"
				if res.State == StateExists {
					expectedStr = "Exists"
				}
				violations = append(violations, Violation{
					Kind: ViolationModelInventoryMismatch,
					Message: fmt.Sprintf("stack %s resource %s (slot %d): inventory=%s but model expects %s",
						stack.Label, label, idx, stateStr, expectedStr),
				})
				continue
			}

			if res.State != StateExists {
				continue
			}

			if invRes.Type != resourceType {
				violations = append(violations, Violation{
					Kind: ViolationModelInventoryMismatch,
					Message: fmt.Sprintf("stack %s resource %s (slot %d): inventory type=%s but model expects %s",
						stack.Label, label, idx, invRes.Type, resourceType),
				})
			}

			expectedProperties := expectedPropertiesForComparison(model, s, idx, res.Properties, inventoryByKey)
			actualProperties := string(invRes.Properties)
			if model.Pool != nil && !model.Pool.IsParent(idx) {
				actualProperties = stripPropertyKey(actualProperties, "ParentId")
				expectedProperties = stripPropertyKey(expectedProperties, "ParentId")
			}
			if !jsonEqual(actualProperties, expectedProperties) {
				violations = append(violations, Violation{
					Kind: ViolationModelInventoryMismatch,
					Message: fmt.Sprintf("stack %s resource %s (slot %d): inventory properties=%s but model expects %s",
						stack.Label, label, idx, string(invRes.Properties), expectedProperties),
				})
			}
		}
	}

	for _, res := range inventory {
		key := res.Stack + "/" + res.Label
		if expectedExistingKeys[key] {
			continue
		}
		stackIdx, slotIdx, ok := model.findResourceSlot(res.Stack, res.Label)
		if ok && model.HasPendingManagedDriftAffectingSlot(stackIdx, slotIdx) {
			continue
		}
		violations = append(violations, Violation{
			Kind: ViolationModelInventoryMismatch,
			Message: fmt.Sprintf("inventory contains unexpected managed resource: stack %s resource %s type=%s",
				res.Stack, res.Label, res.Type),
		})
	}

	return violations
}

func expectedPropertiesForComparison(model *StateModel, stackIdx, slotIdx int, properties string, inventoryByKey map[string]pkgmodel.Resource) string {
	if properties == "" || model.Pool == nil || model.Pool.IsParent(slotIdx) {
		return properties
	}
	var props map[string]any
	if err := json.Unmarshal([]byte(properties), &props); err != nil {
		return properties
	}
	parentStackLabel, parentLabel, ok := expectedParentResourceKey(model, stackIdx, slotIdx)
	if !ok {
		return properties
	}
	if invParent, ok := inventoryByKey[parentStackLabel+"/"+parentLabel]; ok {
		var parentProps map[string]any
		if err := json.Unmarshal(invParent.Properties, &parentProps); err == nil {
			if name, ok := parentProps["Name"].(string); ok && name != "" {
				props["ParentId"] = name
				bytes, err := json.Marshal(props)
				if err == nil {
					return string(bytes)
				}
			}
		}
	}
	return properties
}

func expectedParentResourceKey(model *StateModel, stackIdx, slotIdx int) (string, string, bool) {
	if model.Pool == nil {
		return "", "", false
	}
	if model.Pool.IsCrossStack(slotIdx) {
		parentSlot := model.Pool.Slots[slotIdx].CrossStackParentSlot
		if parentSlot < 0 {
			return "", "", false
		}
		return model.ProviderStackLabel, model.LabelForResource(0, parentSlot), true
	}
	parentSlot := model.Pool.Slots[slotIdx].ParentIndex
	if parentSlot < 0 {
		return "", "", false
	}
	return model.Stack(stackIdx).Label, model.LabelForResource(stackIdx, parentSlot), true
}

func stripPropertyKey(properties, key string) string {
	if properties == "" {
		return properties
	}
	var props map[string]any
	if err := json.Unmarshal([]byte(properties), &props); err != nil {
		return properties
	}
	delete(props, key)
	bytes, err := json.Marshal(props)
	if err != nil {
		return properties
	}
	return string(bytes)
}

// CheckUnmanagedModelVsInventory verifies that unmanaged resources expected in
// inventory match the actual unmanaged inventory, and that unexpected unmanaged
// resources are not present.
func CheckUnmanagedModelVsInventory(model *StateModel, inventory []pkgmodel.Resource) []Violation {
	var violations []Violation
	expected := make(map[string]*ExpectedUnmanagedResource)
	actual := make(map[string]pkgmodel.Resource)

	for _, res := range inventory {
		if res.NativeID == "" {
			continue
		}
		actual[res.NativeID] = res
	}

	for nativeID, res := range model.UnmanagedResources {
		if !res.PresentInInventory {
			continue
		}
		expected[nativeID] = res
		actualRes, ok := actual[nativeID]
		if !ok {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("unmanaged resource %s expected in inventory but missing", nativeID),
			})
			continue
		}
		if actualRes.Managed {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("unmanaged resource %s unexpectedly marked managed in inventory", nativeID),
			})
		}
		if actualRes.Stack != "$unmanaged" {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("unmanaged resource %s has stack=%s, expected $unmanaged", nativeID, actualRes.Stack),
			})
		}
		if res.ResourceType != "" && actualRes.Type != res.ResourceType {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("unmanaged resource %s type=%s, expected %s", nativeID, actualRes.Type, res.ResourceType),
			})
		}
		if !jsonEqual(string(actualRes.Properties), res.InventoryProperties) {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("unmanaged resource %s inventory properties=%s but model expects %s", nativeID, string(actualRes.Properties), res.InventoryProperties),
			})
		}
	}

	for nativeID, res := range actual {
		if expected[nativeID] != nil {
			continue
		}
		violations = append(violations, Violation{
			Kind:    ViolationModelInventoryMismatch,
			Message: fmt.Sprintf("inventory contains unexpected unmanaged resource: %s (%s)", nativeID, res.Type),
		})
	}

	return violations
}

func CheckManagedDriftVsInventory(model *StateModel, inventory []pkgmodel.Resource) []Violation {
	var violations []Violation
	actualByNativeID := make(map[string]pkgmodel.Resource, len(inventory))
	for _, res := range inventory {
		if res.NativeID == "" {
			continue
		}
		actualByNativeID[res.NativeID] = res
	}

	for nativeID, expected := range model.ManagedDriftedResources {
		if expected.PendingSync {
			continue
		}
		actual, ok := actualByNativeID[nativeID]
		if !ok {
			if expected.PresentInInventory {
				violations = append(violations, Violation{
					Kind:    ViolationModelInventoryMismatch,
					Message: fmt.Sprintf("managed drift resource %s expected in inventory but missing", nativeID),
				})
			}
			continue
		}
		if !actual.Managed {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("managed drift resource %s unexpectedly marked unmanaged in inventory", nativeID),
			})
		}
		if expected.StackLabel != "" && actual.Stack != expected.StackLabel {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("managed drift resource %s stack=%s, expected %s", nativeID, actual.Stack, expected.StackLabel),
			})
		}
		if expected.ResourceLabel != "" && actual.Label != expected.ResourceLabel {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("managed drift resource %s label=%s, expected %s", nativeID, actual.Label, expected.ResourceLabel),
			})
		}
		if expected.ResourceType != "" && actual.Type != expected.ResourceType {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("managed drift resource %s type=%s, expected %s", nativeID, actual.Type, expected.ResourceType),
			})
		}
		if expected.InventoryProperties != "" && !jsonEqual(string(actual.Properties), expected.InventoryProperties) {
			violations = append(violations, Violation{
				Kind:    ViolationModelInventoryMismatch,
				Message: fmt.Sprintf("managed drift resource %s inventory properties=%s but model expects %s", nativeID, string(actual.Properties), expected.InventoryProperties),
			})
		}
	}

	return violations
}
