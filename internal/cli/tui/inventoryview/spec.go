// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// opaqueVal is a sentinel for a masked secret/opaque value. Its concrete value
// is never rendered — only a fixed mask.
type opaqueVal struct{}

// opaqueMask is the placeholder shown for opaque (secret) values.
const opaqueMask = "••••••••"

// refVal is a resolvable reference: a concrete value plus the resource property
// it points at (e.g. "lifeline-vpc.VpcId"). target may be empty if it can't be
// derived, in which case only the value is shown.
type refVal struct {
	value  any
	target string
}

// simplifyValue collapses Forma's special-value wrappers into plain data:
//
//   - {$strategy, $visibility:Clear, $value}  → the $value (SetOnce/Update alike)
//   - {$visibility:Opaque, ...}               → opaqueVal (masked on render)
//   - {$ref|$res, $label, $property, $value}  → refVal{value, "label.property"}
//
// Ordinary objects (no "$"-prefixed markers) are returned unchanged so nested
// maps keep recursing. Nested $value payloads are simplified recursively.
func simplifyValue(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	_, hasVal := m["$value"]
	_, hasRef := m["$ref"]
	_, hasStrat := m["$strategy"]
	_, hasVis := m["$visibility"]
	res, _ := m["$res"].(bool)
	if !hasVal && !hasRef && !hasStrat && !hasVis && !res {
		return v // ordinary object — recurse as-is
	}

	if vis, _ := m["$visibility"].(string); vis == pkgmodel.VisibilityOpaque {
		return opaqueVal{}
	}

	inner := simplifyValue(m["$value"])
	if hasRef || res {
		return refVal{value: inner, target: resolvableTarget(m)}
	}
	return inner
}

// resolvableTarget derives a friendly "label.property" (or bare label / ref) for
// a resolvable wrapper, or "" if nothing usable is present.
func resolvableTarget(m map[string]any) string {
	if label, _ := m["$label"].(string); label != "" {
		if prop, _ := m["$property"].(string); prop != "" {
			return label + "." + prop
		}
		return label
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		return strings.TrimPrefix(ref, "formae://")
	}
	return ""
}

// formatScalar renders a simplified (non-container) value for display.
func formatScalar(v any) string {
	switch t := v.(type) {
	case opaqueVal:
		return opaqueMask
	case refVal:
		val := formatScalar(t.value)
		switch {
		case t.target == "":
			return val
		case val == "":
			return "→ " + t.target
		default:
			return val + "  → " + t.target
		}
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatCompact renders a value for a single-line/table context: like
// formatScalar but drops the resolvable target annotation (too noisy inline).
func formatCompact(v any) string {
	if r, ok := v.(refVal); ok {
		return formatCompact(r.value)
	}
	return formatScalar(v)
}

// compactKV renders a JSON object as a comma-separated "k1: v1, k2: v2" string.
// Keys are sorted alphabetically. Special-value wrappers are simplified first.
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
		parts = append(parts, fmt.Sprintf("%s: %s", k, formatCompact(simplifyValue(m[k]))))
	}
	return strings.Join(parts, ", ")
}

// jsonTree renders a JSON object as indented "key: value" lines. See jsonTreeMap
// for the rendering rules.
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

// jsonTreeMap renders a map[string]any into lines:
//   - keys sorted alphabetically
//   - nested objects recurse with a two-space indent
//   - arrays render via jsonTreeArray
//   - special-value wrappers are simplified (see simplifyValue)
func jsonTreeMap(m map[string]any, indent int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	prefix := strings.Repeat(" ", indent)
	var lines []string
	for _, k := range keys {
		v := simplifyValue(m[k])
		switch tv := v.(type) {
		case map[string]any:
			lines = append(lines, fmt.Sprintf("%s%s:", prefix, k))
			lines = append(lines, jsonTreeMap(tv, indent+2)...)
		case []any:
			lines = append(lines, fmt.Sprintf("%s%s:", prefix, k))
			lines = append(lines, jsonTreeArray(tv, indent+2)...)
		default:
			lines = append(lines, fmt.Sprintf("%s%s: %s", prefix, k, formatScalar(v)))
		}
	}
	return lines
}

// jsonTreeArray renders an array's elements as "- item" lines. Objects with the
// AWS {Key, Value} tag shape collapse to "- <Key>: <Value>"; other objects
// render nested with the dash on their first line; scalars render inline.
func jsonTreeArray(arr []any, indent int) []string {
	prefix := strings.Repeat(" ", indent)
	var lines []string
	for _, item := range arr {
		sv := simplifyValue(item)
		m, ok := sv.(map[string]any)
		if !ok {
			lines = append(lines, fmt.Sprintf("%s- %s", prefix, formatScalar(sv)))
			continue
		}
		if key, val, ok := asKeyValue(m); ok {
			lines = append(lines, fmt.Sprintf("%s- %s: %s", prefix, key, formatScalar(val)))
			continue
		}
		sub := jsonTreeMap(m, 0)
		for i, l := range sub {
			dash := "  "
			if i == 0 {
				dash = "- "
			}
			lines = append(lines, prefix+dash+l)
		}
	}
	return lines
}

// asKeyValue recognises the AWS tag shape {"Key": <string>, "Value": <any>} and
// returns the key string and simplified value. ok is false for any other shape.
func asKeyValue(m map[string]any) (string, any, bool) {
	if len(m) != 2 {
		return "", nil, false
	}
	rawKey, hasKey := m["Key"]
	rawVal, hasVal := m["Value"]
	if !hasKey || !hasVal {
		return "", nil, false
	}
	key, ok := rawKey.(string)
	if !ok {
		return "", nil, false
	}
	return key, simplifyValue(rawVal), true
}
