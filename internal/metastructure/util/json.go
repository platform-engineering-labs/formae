// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
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
	return JsonEqualIgnoreArrayOrderStrictRoots(a, b, nil)
}

// JsonEqualIgnoreArrayOrderStrictRoots is JsonEqualIgnoreArrayOrder with an
// exemption set: for top-level fields named in strictRoots, empties are
// VALUES - an empty collection differs from an absent key and from a
// non-empty value (the preserveEmptyValues field hint). Arrays stay
// order-agnostic in both modes.
func JsonEqualIgnoreArrayOrderStrictRoots(a, b json.RawMessage, strictRoots map[string]bool) (bool, error) {
	return jsonEqualIgnoreArrayOrderStrictRoots(a, b, strictRoots, false)
}

// JsonEqualIgnoreArrayOrderStrictRootsExactNumbers preserves exact decimal
// values instead of converting numbers to float64. Equivalent decimal spellings
// (including exponent notation) remain equal; array and empty rules are shared.
func JsonEqualIgnoreArrayOrderStrictRootsExactNumbers(a, b json.RawMessage, strictRoots map[string]bool) (bool, error) {
	return jsonEqualIgnoreArrayOrderStrictRoots(a, b, strictRoots, true)
}

func jsonEqualIgnoreArrayOrderStrictRoots(a, b json.RawMessage, strictRoots map[string]bool, exactNumbers bool) (bool, error) {
	aEmpty := len(a) == 0
	bEmpty := len(b) == 0
	if aEmpty && bEmpty {
		return true, nil
	}

	decode := json.Unmarshal
	if exactNumbers {
		decode = decodeExactComparisonJSON
	}
	var objA, objB any
	if !aEmpty {
		if err := decode(a, &objA); err != nil {
			return false, err
		}
	}
	if !bEmpty {
		if err := decode(b, &objB); err != nil {
			return false, err
		}
	}

	if len(strictRoots) > 0 {
		mapA, okA := objA.(map[string]any)
		mapB, okB := objB.(map[string]any)
		if okA && okB {
			for root := range strictRoots {
				va, aHas := mapA[root]
				vb, bHas := mapB[root]
				if aHas != bHas {
					return false, nil
				}
				if aHas && !deepEqualArraysAsSets(va, vb) {
					return false, nil
				}
				delete(mapA, root)
				delete(mapB, root)
			}
		}
	}

	return deepEqualIgnoreArrayOrder(objA, objB), nil
}

// deepEqualArraysAsSets compares values with arrays as sets but with empties
// significant: an empty collection equals only an empty collection of the
// same kind, and a key present on one side only is a difference regardless
// of its value.
func deepEqualArraysAsSets(a, b any) bool {
	switch valA := a.(type) {
	case json.Number:
		valB, ok := b.(json.Number)
		return ok && canonicalDecimal(valA.String()) == canonicalDecimal(valB.String())
	case map[string]any:
		valB, ok := b.(map[string]any)
		if !ok || len(valA) != len(valB) {
			return false
		}
		for k, va := range valA {
			vb, has := valB[k]
			if !has || !deepEqualArraysAsSets(va, vb) {
				return false
			}
		}
		return true
	case []any:
		valB, ok := b.([]any)
		if !ok || len(valA) != len(valB) {
			return false
		}
		matched := make([]bool, len(valB))
		for _, elemA := range valA {
			found := false
			for j, elemB := range valB {
				if !matched[j] && deepEqualArraysAsSets(elemA, elemB) {
					matched[j] = true
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
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
	case json.Number:
		valB, ok := b.(json.Number)
		return ok && canonicalDecimal(valA.String()) == canonicalDecimal(valB.String())
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

// canonicalDecimal never expands an exponent into a huge decimal string.
// Inputs are number tokens validated by the JSON decoder.
func canonicalDecimal(number string) string {
	sign := ""
	if strings.HasPrefix(number, "-") {
		sign = "-"
		number = number[1:]
	}
	exponent := new(big.Int)
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		exponent.SetString(number[i+1:], 10)
		number = number[:i]
	}
	if i := strings.IndexByte(number, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(number)-i-1)))
		number = number[:i] + number[i+1:]
	}
	number = strings.TrimLeft(number, "0")
	if number == "" {
		return "0"
	}
	trimmed := strings.TrimRight(number, "0")
	exponent.Add(exponent, big.NewInt(int64(len(number)-len(trimmed))))
	return sign + trimmed + "e" + exponent.String()
}

func decodeExactComparisonJSON(raw []byte, dest any) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid comparison JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(dest)
}

// JsonEqualExactNumbers compares exact JSON structure, with numeric spelling
// equivalence only. Array order, absent members, null and empty collections
// remain distinct; object member order is immaterial. This is suitable before
// transformations whose semantics may depend on array order or emptiness.
func JsonEqualExactNumbers(a, b json.RawMessage) (bool, error) {
	var before, after any
	if err := decodeExactComparisonJSON(a, &before); err != nil {
		return false, err
	}
	if err := decodeExactComparisonJSON(b, &after); err != nil {
		return false, err
	}
	return deepEqualExactNumbers(before, after), nil
}

func deepEqualExactNumbers(a, b any) bool {
	switch left := a.(type) {
	case json.Number:
		right, ok := b.(json.Number)
		return ok && canonicalDecimal(left.String()) == canonicalDecimal(right.String())
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, present := right[key]
			if !present || !deepEqualExactNumbers(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, value := range left {
			if !deepEqualExactNumbers(value, right[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
