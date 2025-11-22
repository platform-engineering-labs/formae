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

func JsonEqualRaw(a, b json.RawMessage) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	return JsonEqual(string(a), string(b))
}

// JsonEqualIgnoreArrayOrder compares two JSON objects treating all arrays as sets (order aagnostic)
func JsonEqualIgnoreArrayOrder(a, b json.RawMessage) (bool, error) {
	if a == nil && b == nil {
		return true, nil
	}
	if a == nil || b == nil {
		return false, nil
	}

	if len(a) == 0 && len(b) == 0 {
		return true, nil
	}
	if len(a) == 0 || len(b) == 0 {
		return false, nil
	}

	var objA, objB any
	if err := json.Unmarshal(a, &objA); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &objB); err != nil {
		return false, err
	}

	return deepEqualIgnoreArrayOrder(objA, objB), nil
}

func deepEqualIgnoreArrayOrder(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}

	switch valA := a.(type) {
	case map[string]any:
		valB, ok := b.(map[string]any)
		if !ok {
			return false
		}

		if len(valA) != len(valB) {
			return false
		}

		for k, v := range valA {
			vb, ok := valB[k]
			if !ok || !deepEqualIgnoreArrayOrder(v, vb) {
				return false
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
