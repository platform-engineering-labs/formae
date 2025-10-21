// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"sort"
	"strings"
)

// mapToString converts a map[string]string to a consistent string representation.
// It sorts the map keys to ensure the output string is always the same for the
// same input map, which is crucial for a reliable conversion back to a map.
// The format is "key1:value1,key2:value2,...".
func MapToString(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}

	// Get all keys and sort them. This guarantees a consistent order.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(k)
		sb.WriteString(":")
		sb.WriteString(m[k])
	}

	return sb.String()
}

// stringToMap converts a string formatted by mapToString back into a
// map[string]string. It returns an error if the string format is invalid.
func StringToMap(s string) map[string]string {
	if len(s) == 0 {
		return make(map[string]string)
	}

	result := make(map[string]string)

	for pair := range strings.SplitSeq(s, ",") {
		parts := strings.SplitN(pair, ":", 2)
		key := parts[0]
		value := parts[1]
		result[key] = value
	}

	return result
}
