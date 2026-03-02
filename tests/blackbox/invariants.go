// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"fmt"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// ViolationKind classifies the type of invariant violation.
type ViolationKind int

const (
	ViolationPhantomResource    ViolationKind = iota // in inventory but not in cloud
	ViolationOrphanedResource                        // in cloud but not in inventory
	ViolationPropertyMismatch                        // inventory and cloud properties differ
	ViolationCommandNotTerminal                      // command not in terminal state
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
// ignorePrefixes allows filtering out cloud state entries whose native IDs
// start with any of the given prefixes (e.g. "cloud-" for out-of-band entries).
//
// Invariants checked:
//  1. No phantom resources: every resource in inventory exists in cloud state
//  2. No orphaned cloud resources: every cloud resource is tracked in inventory
//  3. Property consistency: inventory properties match cloud state properties
func CheckInvariants(inventory []pkgmodel.Resource, cloudState map[string]testcontrol.CloudStateEntry, ignorePrefixes ...string) []Violation {
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
		if _, ok := cloudState[res.NativeID]; !ok {
			violations = append(violations, Violation{
				Kind:    ViolationPhantomResource,
				Message: fmt.Sprintf("phantom resource: %s (%s) in inventory but not in cloud state", res.Label, res.NativeID),
			})
		}
	}

	// Invariant 2: No orphaned cloud resources
	// Every cloud resource should be tracked in inventory
	// (skip entries matching ignorePrefixes — these are out-of-band test artifacts)
	for nativeID, entry := range cloudState {
		if _, ok := inventoryByNativeID[nativeID]; !ok {
			if hasAnyPrefix(nativeID, ignorePrefixes) {
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
		invRes, ok := inventoryByNativeID[nativeID]
		if !ok {
			continue // already reported as orphaned
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

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
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
// whitespace, and empty arrays). Empty arrays are treated as equivalent to
// missing fields since the agent may strip them during property processing.
func jsonEqual(a, b string) bool {
	var aVal, bVal map[string]any
	if err := json.Unmarshal([]byte(a), &aVal); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bVal); err != nil {
		return false
	}
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
