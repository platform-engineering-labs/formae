// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"bytes"
	"encoding/json"
	"reflect"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ReadEquivalent reports whether a freshly read property document carries the
// same state as the stored one under the schema's collection semantics, applied
// at every depth: an unkeyed collection is a set, a field hinted as an ordered
// array compares positionally, an entity set matches its elements by key, and
// an atomic field compares as a whole. Hint names are dotted and index-free,
// so a hint on Items.Steps governs Steps inside every element of Items. Empty
// and null documents compare as empty objects.
func ReadEquivalent(stored, read json.RawMessage, schema pkgmodel.Schema) (bool, error) {
	var a, b any
	if err := json.Unmarshal(emptyAsObject(stored), &a); err != nil {
		return false, err
	}
	if err := json.Unmarshal(emptyAsObject(read), &b); err != nil {
		return false, err
	}
	return readEquivalence{hints: schema.Hints}.equalAt("", a, b), nil
}

type readEquivalence struct {
	hints map[string]pkgmodel.FieldHint
}

func (e readEquivalence) equalAt(path string, a, b any) bool {
	hint := e.hints[path]
	if hint.UpdateMethod == pkgmodel.FieldUpdateMethodAtomic {
		return reflect.DeepEqual(a, b)
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for key, x := range av {
			y, present := bv[key]
			if !present || !e.equalAt(childPath(path, key), x, y) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		switch hint.UpdateMethod {
		case pkgmodel.FieldUpdateMethodArray:
			for i := range av {
				if !e.equalAt(path, av[i], bv[i]) {
					return false
				}
			}
			return true
		case pkgmodel.FieldUpdateMethodEntitySet:
			if hint.IndexField != "" {
				return e.equalKeyed(path, hint.IndexField, av, bv)
			}
		}
		return e.equalUnordered(path, av, bv)
	default:
		return reflect.DeepEqual(a, b)
	}
}

// equalKeyed matches entity-set elements by their key field, then compares
// each pair; elements without a usable key fall back to unordered matching.
func (e readEquivalence) equalKeyed(path, indexField string, av, bv []any) bool {
	byKey := map[any]any{}
	var unkeyed []any
	for _, y := range bv {
		if k, ok := elementKey(y, indexField); ok {
			byKey[k] = y
		} else {
			unkeyed = append(unkeyed, y)
		}
	}
	var rest []any
	for _, x := range av {
		k, ok := elementKey(x, indexField)
		if !ok {
			rest = append(rest, x)
			continue
		}
		y, present := byKey[k]
		if !present || !e.equalAt(path, x, y) {
			return false
		}
		delete(byKey, k)
	}
	return len(byKey) == 0 && e.equalUnordered(path, rest, unkeyed)
}

// equalUnordered treats both slices as multisets: every element on one side
// must pair with a distinct equivalent element on the other.
func (e readEquivalence) equalUnordered(path string, av, bv []any) bool {
	if len(av) != len(bv) {
		return false
	}
	used := make([]bool, len(bv))
	for _, x := range av {
		matched := false
		for j, y := range bv {
			if !used[j] && e.equalAt(path, x, y) {
				used[j], matched = true, true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func elementKey(v any, indexField string) (any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	k, ok := m[indexField]
	if !ok {
		return nil, false
	}
	switch k.(type) {
	case string, float64, bool:
		return k, true
	}
	return nil, false
}

func childPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func emptyAsObject(doc json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(doc)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}
