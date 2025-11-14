// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"encoding/json"
)

// MapToString converts a map[string]T to a JSON string representation.
// It uses JSON encoding to ensure the output is consistent and can handle
// arbitrary types. The map is serialized to a JSON object string.
func MapToString[T any](m map[string]T) string {
	if len(m) == 0 {
		return ""
	}

	// Marshal to JSON
	data, err := json.Marshal(m)
	if err != nil {
		// This shouldn't happen with maps, but handle it gracefully
		return ""
	}

	return string(data)
}

// StringToMap converts a JSON string back into a map[string]T.
// It uses JSON decoding to handle arbitrary types.
func StringToMap[T any](s string) map[string]T {
	if len(s) == 0 {
		return make(map[string]T)
	}

	result := make(map[string]T)
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		// Return empty map on error
		return make(map[string]T)
	}

	return result
}
