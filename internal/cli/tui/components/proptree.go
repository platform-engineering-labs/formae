// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// PropertyLines renders a JSON property object as indented "key: value" lines,
// the same listing style the inventory detail screen uses for a resource's
// properties. It is the shared home for that format so the simulation-preview
// create card and the inventory detail read identically.
//
// Rendering rules (mirrors inventoryview/spec.go jsonTree — keep in sync):
//   - keys sorted alphabetically
//   - nested objects recurse with a two-space indent under "key:"
//   - arrays render as "- item" lines; AWS {Key,Value} tags collapse to
//     "- <Key>: <Value>"
//   - Forma special-value wrappers are simplified: {$value} unwraps, {$ref}
//     renders "<value>  → <label.property>", {$gen} renders "<value>  →
//     <generator>" (or "<value>  → <label.output>" for its authored shape),
//     opaque values are masked but a reference's target still shows.
//
// indent is the number of leading spaces for the top level.
func PropertyLines(raw json.RawMessage, indent int) []string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return []string{string(raw)}
	}
	return propTreeMap(m, indent)
}

const propOpaqueMask = "••••••••"

type propOpaque struct{}

type propRef struct {
	value  any
	target string
}

func propTreeMap(m map[string]any, indent int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	prefix := strings.Repeat(" ", indent)
	var lines []string
	for _, k := range keys {
		v := propSimplify(m[k])
		switch tv := v.(type) {
		case map[string]any:
			lines = append(lines, fmt.Sprintf("%s%s:", prefix, k))
			lines = append(lines, propTreeMap(tv, indent+2)...)
		case []any:
			lines = append(lines, fmt.Sprintf("%s%s:", prefix, k))
			lines = append(lines, propTreeArray(tv, indent+2)...)
		default:
			lines = append(lines, fmt.Sprintf("%s%s: %s", prefix, k, propScalar(v)))
		}
	}
	return lines
}

func propTreeArray(arr []any, indent int) []string {
	prefix := strings.Repeat(" ", indent)
	var lines []string
	for _, item := range arr {
		sv := propSimplify(item)
		m, ok := sv.(map[string]any)
		if !ok {
			lines = append(lines, fmt.Sprintf("%s- %s", prefix, propScalar(sv)))
			continue
		}
		if key, val, ok := propKeyValue(m); ok {
			lines = append(lines, fmt.Sprintf("%s- %s: %s", prefix, key, propScalar(val)))
			continue
		}
		sub := propTreeMap(m, 0)
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

// propSimplify collapses Forma's special-value wrappers into plain data, mirroring
// inventoryview/spec.go simplifyValue.
func propSimplify(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	_, hasVal := m["$value"]
	_, hasRef := m["$ref"]
	_, hasStrat := m["$strategy"]
	_, hasVis := m["$visibility"]
	res, _ := m["$res"].(bool)
	gen, _ := m["$gen"].(bool)
	if !hasVal && !hasRef && !hasStrat && !hasVis && !res && !gen {
		return v
	}
	if vis, _ := m["$visibility"].(string); vis == pkgmodel.VisibilityOpaque {
		// A reference is masked, not blanked: the resolved value is withheld
		// like any opaque scalar, but which target it names (a resource
		// property, or which generator) is safe metadata and still shows.
		if hasRef || res || gen {
			return propRef{value: propOpaque{}, target: propResolvableTarget(m)}
		}
		return propOpaque{}
	}
	inner := propSimplify(m["$value"])
	if hasRef || res || gen {
		return propRef{value: inner, target: propResolvableTarget(m)}
	}
	return inner
}

func propResolvableTarget(m map[string]any) string {
	if label, _ := m["$label"].(string); label != "" {
		if prop, _ := m["$property"].(string); prop != "" {
			return label + "." + prop
		}
		if output, _ := m["$output"].(string); output != "" {
			return label + "." + output
		}
		return label
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		return strings.TrimPrefix(ref, "formae://")
	}
	if generator, _ := m["$generator"].(string); generator != "" {
		return generator
	}
	return ""
}

func propScalar(v any) string {
	switch t := v.(type) {
	case propOpaque:
		return propOpaqueMask
	case propRef:
		val := propScalar(t.value)
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
		// Unquoted, matching the inventory detail screen's formatScalar.
		return fmt.Sprintf("%v", v)
	}
}

func propKeyValue(m map[string]any) (string, any, bool) {
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
	return key, propSimplify(rawVal), true
}
