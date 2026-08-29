// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// SuppressedFieldDiff describes out-of-band movement on provider-default
// content the patch pipeline's strip passes hide from the diff: a
// hasProviderDefault field the user did not declare (or, for a partially
// declared EntitySet field, the elements with undeclared keys) whose value
// differs between two observations of the resource.
//
// From and To carry the suppressed content on each side, nil when the side
// has no value for the path. For an opaque path both are nil: the diff
// records the path and the fact of movement only, never values or digests,
// because callers persist and display these records.
type SuppressedFieldDiff struct {
	Path   string
	From   json.RawMessage
	To     json.RawMessage
	Opaque bool
}

// SuppressedFieldDiffs compares two stored property documents restricted to
// the content the provider-default strip passes would hide from a diff
// against the given desired properties. It reports one entry per suppressed
// field whose content moved, sorted by path.
//
// The suppressed content per hasProviderDefault field is exactly what the
// strip passes suppress: the whole field when desired omits it (absent or
// null, the fieldExistsInMap semantics of removeProviderDefaultFieldsBoth),
// and, for an EntitySet field with one or more declared keys, the elements
// whose key is not among the declared keys (what
// removeProviderDefaultEntitySetElements filters). An explicit empty
// EntitySet declaration suppresses nothing: that is a user-initiated clear
// the diff plans.
//
// Comparison is schema-aware so representation churn is not movement:
// EntitySet content compares keyed by IndexField, collections without an
// index compare as unordered multisets, Array-hinted fields compare ordered,
// and dotted leaves compare as a multiset of leaf values collected with the
// strip's own regime rules: a leaf reached through an array is suppressed
// unconditionally (the ContainerDefinitions.Cpu shape, stripped from both
// sides regardless of declaration), while a pure-object leaf is suppressed
// only when desired omits it.
func SuppressedFieldDiffs(oldProps, newProps, desired json.RawMessage, schema pkgmodel.Schema) ([]SuppressedFieldDiff, error) {
	oldMap, err := unmarshalPropsObject(oldProps)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal old properties: %w", err)
	}
	newMap, err := unmarshalPropsObject(newProps)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal new properties: %w", err)
	}
	desiredMap, err := unmarshalPropsObject(desired)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal desired properties: %w", err)
	}

	paths := append([]string(nil), schema.HasProviderDefault()...)
	sort.Strings(paths)

	var diffs []SuppressedFieldDiff
	for _, path := range paths {
		parts := strings.Split(path, ".")
		hint := schema.Hints[path]

		var from, to json.RawMessage
		var moved bool
		// opacityProbe holds exactly the decoded content a note would carry,
		// so envelope opacity markers anywhere within it force sanitization.
		var opacityProbe []any

		if len(parts) > 1 {
			oldLeaves := collectSuppressedLeaves(oldMap, desiredMap, parts)
			newLeaves := collectSuppressedLeaves(newMap, desiredMap, parts)
			from, to, moved = compareLeafMultisets(oldLeaves, newLeaves)
			opacityProbe = append(append(opacityProbe, oldLeaves...), newLeaves...)
		} else if !fieldExistsInMap(desiredMap, parts) {
			from, to, moved = compareWholeField(oldMap, newMap, path, hint)
			opacityProbe = []any{oldMap[path], newMap[path]}
		} else if hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "" {
			declaredKeys := entitySetKeys(desiredMap[path], hint.IndexField)
			if len(declaredKeys) == 0 {
				// Explicit empty (or keyless) declaration: the diff plans
				// the drain; nothing is suppressed.
				continue
			}
			from, to, moved = compareEntitySetSubsets(oldMap[path], newMap[path], hint.IndexField, declaredKeys)
			opacityProbe = []any{oldMap[path], newMap[path]}
		} else {
			continue
		}

		if !moved {
			continue
		}

		opaque := pathOpaque(schema, path)
		for _, v := range opacityProbe {
			if valueHasOpaqueMarker(v) {
				opaque = true
				break
			}
		}
		if opaque {
			diffs = append(diffs, SuppressedFieldDiff{Path: path, Opaque: true})
			continue
		}
		diffs = append(diffs, SuppressedFieldDiff{Path: path, From: from, To: to})
	}

	return diffs, nil
}

func unmarshalPropsObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// compareWholeField compares a fully suppressed field per its collection
// semantics. A side where the field is absent renders as nil.
func compareWholeField(oldMap, newMap map[string]any, path string, hint pkgmodel.FieldHint) (json.RawMessage, json.RawMessage, bool) {
	oldVal, oldHas := oldMap[path]
	newVal, newHas := newMap[path]
	if !oldHas && !newHas {
		return nil, nil, false
	}

	equal := false
	switch {
	case oldHas != newHas:
		equal = false
	case hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "":
		oldArr, _ := oldVal.([]any)
		newArr, _ := newVal.([]any)
		equal = entitySetContentEqual(oldArr, newArr, hint.IndexField)
	case hint.UpdateMethod == pkgmodel.FieldUpdateMethodArray:
		equal = canonicalJSON(oldVal) == canonicalJSON(newVal)
	default:
		oldArr, oldIsArr := oldVal.([]any)
		newArr, newIsArr := newVal.([]any)
		if oldIsArr && newIsArr {
			equal = multisetEqual(oldArr, newArr)
		} else {
			equal = canonicalJSON(oldVal) == canonicalJSON(newVal)
		}
	}
	if equal {
		return nil, nil, false
	}

	var from, to json.RawMessage
	if oldHas {
		from = json.RawMessage(canonicalJSON(oldVal))
	}
	if newHas {
		to = json.RawMessage(canonicalJSON(newVal))
	}
	return from, to, true
}

// compareEntitySetSubsets compares only the elements whose key is not among
// the declared keys: the portion removeProviderDefaultEntitySetElements
// filters out of the diff. Keyless elements stay visible to the diff (the
// filter keeps them), so they are not suppressed content here.
func compareEntitySetSubsets(oldVal, newVal any, indexField string, declaredKeys map[string]struct{}) (json.RawMessage, json.RawMessage, bool) {
	oldArr, _ := oldVal.([]any)
	newArr, _ := newVal.([]any)
	oldSub := suppressedEntitySetElements(oldArr, indexField, declaredKeys)
	newSub := suppressedEntitySetElements(newArr, indexField, declaredKeys)
	if entitySetContentEqual(oldSub, newSub, indexField) {
		return nil, nil, false
	}
	return json.RawMessage(canonicalJSON(sortedByKey(oldSub, indexField))),
		json.RawMessage(canonicalJSON(sortedByKey(newSub, indexField))),
		true
}

func suppressedEntitySetElements(arr []any, indexField string, declaredKeys map[string]struct{}) []any {
	var out []any
	for _, elem := range arr {
		elemMap, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		keyVal, ok := elemMap[indexField]
		if !ok {
			continue
		}
		if _, declared := declaredKeys[fmt.Sprintf("%v", keyVal)]; declared {
			continue
		}
		out = append(out, elem)
	}
	return out
}

func entitySetKeys(val any, indexField string) map[string]struct{} {
	keys := map[string]struct{}{}
	arr, _ := val.([]any)
	for _, elem := range arr {
		if elemMap, ok := elem.(map[string]any); ok {
			if keyVal, ok := elemMap[indexField]; ok {
				keys[fmt.Sprintf("%v", keyVal)] = struct{}{}
			}
		}
	}
	return keys
}

// entitySetContentEqual compares keyed elements by key and keyless elements
// as an unordered multiset, so element order is never movement.
func entitySetContentEqual(a, b []any, indexField string) bool {
	aKeyed, aKeyless := partitionByKey(a, indexField)
	bKeyed, bKeyless := partitionByKey(b, indexField)
	if len(aKeyed) != len(bKeyed) {
		return false
	}
	for k, v := range aKeyed {
		if bKeyed[k] != v {
			return false
		}
	}
	return multisetEqual(aKeyless, bKeyless)
}

func partitionByKey(arr []any, indexField string) (map[string]string, []any) {
	keyed := map[string]string{}
	var keyless []any
	for _, elem := range arr {
		if elemMap, ok := elem.(map[string]any); ok {
			if keyVal, ok := elemMap[indexField]; ok {
				keyed[fmt.Sprintf("%v", keyVal)] = canonicalJSON(elem)
				continue
			}
		}
		keyless = append(keyless, elem)
	}
	return keyed, keyless
}

func sortedByKey(arr []any, indexField string) []any {
	out := append([]any(nil), arr...)
	sort.Slice(out, func(i, j int) bool {
		ki, kj := "", ""
		if m, ok := out[i].(map[string]any); ok {
			ki = fmt.Sprintf("%v", m[indexField])
		}
		if m, ok := out[j].(map[string]any); ok {
			kj = fmt.Sprintf("%v", m[indexField])
		}
		if ki != kj {
			return ki < kj
		}
		return canonicalJSON(out[i]) < canonicalJSON(out[j])
	})
	if out == nil {
		out = []any{}
	}
	return out
}

// compareLeafMultisets compares two suppressed-leaf collections. Set-based
// array comparison has no stable element pairing, so the honest comparison
// is the multiset of leaf values on each side.
func compareLeafMultisets(oldLeaves, newLeaves []any) (json.RawMessage, json.RawMessage, bool) {
	if multisetEqual(oldLeaves, newLeaves) {
		return nil, nil, false
	}
	return renderLeafList(oldLeaves), renderLeafList(newLeaves), true
}

func renderLeafList(leaves []any) json.RawMessage {
	if len(leaves) == 0 {
		return nil
	}
	sorted := append([]any(nil), leaves...)
	sort.Slice(sorted, func(i, j int) bool { return canonicalJSON(sorted[i]) < canonicalJSON(sorted[j]) })
	return json.RawMessage(canonicalJSON(sorted))
}

// collectSuppressedLeaves walks a dotted path with the strip's own regime
// rules (see stripProviderDefaultPath): descending through an array switches
// to the unconditional regime, where every reachable leaf is suppressed
// content regardless of what desired declares; a walk that stays in objects
// keeps the conditional regime, where the leaf is suppressed only when
// desired omits the full path.
func collectSuppressedLeaves(root any, desired map[string]any, parts []string) []any {
	var out []any
	declared := fieldExistsInMap(desired, parts)
	var walk func(node any, rest []string, inArray bool)
	walk = func(node any, rest []string, inArray bool) {
		switch v := node.(type) {
		case []any:
			for _, elem := range v {
				walk(elem, rest, true)
			}
		case map[string]any:
			val, ok := v[rest[0]]
			if !ok {
				return
			}
			if len(rest) == 1 {
				if inArray || !declared {
					out = append(out, val)
				}
				return
			}
			walk(val, rest[1:], inArray)
		}
	}
	walk(root, parts, false)
	return out
}

func multisetEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, v := range a {
		counts[canonicalJSON(v)]++
	}
	for _, v := range b {
		key := canonicalJSON(v)
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	return true
}

// canonicalJSON renders a decoded value deterministically (encoding/json
// sorts map keys), so equality can be a string comparison.
func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

// pathOpaque reports whether an Opaque schema hint covers the path: the hint
// on the path itself, on an ancestor (an opaque container makes its content
// opaque), or on a descendant (an opaque leaf makes the container's content
// unsafe to render).
func pathOpaque(schema pkgmodel.Schema, path string) bool {
	for _, op := range schema.Opaque() {
		if op == path || strings.HasPrefix(path, op+".") || strings.HasPrefix(op, path+".") {
			return true
		}
	}
	return false
}

// valueHasOpaqueMarker reports whether a decoded value carries envelope
// opacity anywhere within: a $visibility: Opaque marker or $hashed material.
// Fails toward opaque so sanitization never depends on the schema alone.
func valueHasOpaqueMarker(v any) bool {
	switch node := v.(type) {
	case map[string]any:
		if vis, ok := node["$visibility"].(string); ok && vis == "Opaque" {
			return true
		}
		if _, ok := node["$hashed"]; ok {
			return true
		}
		for _, child := range node {
			if valueHasOpaqueMarker(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if valueHasOpaqueMarker(child) {
				return true
			}
		}
	}
	return false
}
