// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"encoding/json"
	"reflect"
)

func JsonEqual(s1, s2 string) bool {
	var j1, j2 any

	err := json.Unmarshal([]byte(s1), &j1)
	if err != nil {
		return false
	}

	err = json.Unmarshal([]byte(s2), &j2)
	if err != nil {
		return false
	}

	return reflect.DeepEqual(j1, j2)
}

// isEmptyConfig checks if a json.RawMessage represents an empty/absent config
// (nil, empty slice, or empty object {})
func isEmptyConfig(msg json.RawMessage) bool {
	if len(msg) == 0 {
		return true
	}
	// Check for empty object "{}" (possibly with whitespace)
	trimmed := string(msg)
	return trimmed == "{}" || trimmed == "{ }" || trimmed == "null"
}

func JsonEqualRaw(a, b json.RawMessage) bool {
	// Treat nil, empty slice, empty object {}, and null as equivalent
	aEmpty := isEmptyConfig(a)
	bEmpty := isEmptyConfig(b)
	if aEmpty && bEmpty {
		return true
	}
	if aEmpty || bEmpty {
		return false
	}

	return JsonEqual(string(a), string(b))
}

// JsonEqualIgnoreArrayOrder compares two JSON objects treating all arrays as sets (order agnostic).
// Null, empty arrays, empty maps, and absent keys are treated as semantically equivalent.
func JsonEqualIgnoreArrayOrder(a, b json.RawMessage) (bool, error) {
	aEmpty := len(a) == 0
	bEmpty := len(b) == 0
	if aEmpty && bEmpty {
		return true, nil
	}

	var objA, objB any
	if !aEmpty {
		if err := json.Unmarshal(a, &objA); err != nil {
			return false, err
		}
	}
	if !bEmpty {
		if err := json.Unmarshal(b, &objB); err != nil {
			return false, err
		}
	}

	return deepEqualIgnoreArrayOrder(objA, objB), nil
}

// isEmptyValue returns true for values that are semantically empty:
// nil, empty []any, empty map[string]any.
func isEmptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	}
	return false
}

func deepEqualIgnoreArrayOrder(a, b any) bool {
	// Treat nil, empty array, and empty map as equivalent
	if isEmptyValue(a) && isEmptyValue(b) {
		return true
	}

	switch valA := a.(type) {
	case map[string]any:
		valB, ok := b.(map[string]any)
		if !ok {
			return false
		}

		// Iterate the union of keys from both maps
		seen := make(map[string]struct{})
		for k := range valA {
			seen[k] = struct{}{}
		}
		for k := range valB {
			seen[k] = struct{}{}
		}

		for k := range seen {
			va, aHas := valA[k]
			vb, bHas := valB[k]

			switch {
			case aHas && bHas:
				if !deepEqualIgnoreArrayOrder(va, vb) {
					return false
				}
			case aHas && !bHas:
				if !isEmptyValue(va) {
					return false
				}
			case !aHas && bHas:
				if !isEmptyValue(vb) {
					return false
				}
			}
		}
		return true

	case []any:
		valB, ok := b.([]any)
		if !ok {
			return false
		}

		if len(valA) != len(valB) {
			return false
		}

		// Treat array as a set - match elements regardless of order
		copyA := make([]any, len(valA))
		copyB := make([]any, len(valB))

		for i, elemA := range valA {
			found := false
			for j, elemB := range valB {
				if deepEqualIgnoreArrayOrder(elemA, elemB) && copyB[j] == nil {
					copyA[i] = 1 // Mark as matched
					copyB[j] = 1 // Mark as matched
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}

		for _, v := range copyB {
			if v == nil {
				return false
			}
		}
		return true

	default:
		return reflect.DeepEqual(a, b)
	}
}

// MergeJSON merges multiple JSON objects into a single JSON object.
// Later objects in the list override earlier ones if there are key conflicts.
// Returns an error if any of the JSON objects cannot be unmarshaled or if the result cannot be marshaled.
func MergeJSON(jsons ...json.RawMessage) (json.RawMessage, error) {
	merged := make(map[string]any)

	for _, j := range jsons {
		// Skip nil or empty JSON
		if len(j) == 0 || string(j) == "null" {
			continue
		}

		var obj map[string]any
		if err := json.Unmarshal(j, &obj); err != nil {
			return nil, err
		}

		// Merge into the result
		for k, v := range obj {
			merged[k] = v
		}
	}

	result, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	return result, nil
}
