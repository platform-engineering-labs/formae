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
// and array-nested dotted leaves (the ContainerDefinitions.Cpu shape, which
// the strip removes from both sides unconditionally) compare as a multiset
// of leaf values.
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

		if len(parts) > 1 {
			if fieldExistsInMap(desiredMap, parts) {
				continue
			}
			from, to, moved = compareLeafMultisets(oldMap, newMap, parts)
		} else if !fieldExistsInMap(desiredMap, parts) {
			from, to, moved = compareWholeField(oldMap, newMap, path, hint)
		} else if hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "" {
			declaredKeys := entitySetKeys(desiredMap[path], hint.IndexField)
			if len(declaredKeys) == 0 {
				// Explicit empty (or keyless) declaration: the diff plans
				// the drain; nothing is suppressed.
				continue
			}
			from, to, moved = compareEntitySetSubsets(oldMap[path], newMap[path], hint.IndexField, declaredKeys)
		} else {
			continue
		}

		if !moved {
			continue
		}

		if pathOpaque(schema, path) || valueHasOpaqueMarker(oldMap[path]) || valueHasOpaqueMarker(newMap[path]) {
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

// compareLeafMultisets handles dotted provider-default paths. The strip
// removes these leaves from both sides in every reachable array element, and
// set-based array comparison has no stable element pairing, so the honest
// comparison is the multiset of leaf values on each side.
func compareLeafMultisets(oldMap, newMap map[string]any, parts []string) (json.RawMessage, json.RawMessage, bool) {
	var oldLeaves, newLeaves []any
	collectLeaves(oldMap, parts, &oldLeaves)
	collectLeaves(newMap, parts, &newLeaves)
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

// collectLeaves walks a dotted path, descending through arrays without
// consuming a path segment, and appends every leaf value found.
func collectLeaves(node any, parts []string, out *[]any) {
	switch v := node.(type) {
	case []any:
		for _, elem := range v {
			collectLeaves(elem, parts, out)
		}
	case map[string]any:
		val, ok := v[parts[0]]
		if !ok {
			return
		}
		if len(parts) == 1 {
			*out = append(*out, val)
			return
		}
		collectLeaves(val, parts[1:], out)
	}
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
