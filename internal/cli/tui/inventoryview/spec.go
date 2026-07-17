// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// unwrapRefValue returns m["$value"] for maps that carry both "$ref" and
// "$value" keys (i.e. Forma resolvable refs), otherwise v is returned as-is.
func unwrapRefValue(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	_, hasRef := m["$ref"]
	val, hasVal := m["$value"]
	if hasRef && hasVal {
		return val
	}
	return v
}

// compactKV renders a JSON object as a comma-separated "k1: v1, k2: v2" string.
// Keys are sorted alphabetically. Values that are resolvable refs are unwrapped
// via unwrapRefValue before formatting.
func compactKV(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := unwrapRefValue(m[k])
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}
	return strings.Join(parts, ", ")
}

// jsonTree renders a JSON object as indented "key: value" lines.
// Keys are sorted alphabetically. Nested maps recurse with two-space indent.
// Arrays render each element as "- item" on its own line. Resolvable ref maps
// are unwrapped via unwrapRefValue before deciding how to render.
func jsonTree(raw json.RawMessage, indent int) []string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return []string{string(raw)}
	}
	return jsonTreeMap(m, indent)
}

// jsonTreeMap renders a map[string]any into lines using the jsonTree rules.
func jsonTreeMap(m map[string]any, indent int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	prefix := strings.Repeat(" ", indent)
	var lines []string
	for _, k := range keys {
		v := unwrapRefValue(m[k])
		switch tv := v.(type) {
		case map[string]any:
			lines = append(lines, fmt.Sprintf("%s%s:", prefix, k))
			lines = append(lines, jsonTreeMap(tv, indent+2)...)
		case []any:
			lines = append(lines, fmt.Sprintf("%s%s:", prefix, k))
			for _, item := range tv {
				lines = append(lines, fmt.Sprintf("%s- %v", prefix+"  ", item))
			}
		default:
			lines = append(lines, fmt.Sprintf("%s%s: %v", prefix, k, v))
		}
	}
	return lines
}
