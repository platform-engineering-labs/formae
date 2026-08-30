// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"
)

// StripOpaqueRefValues removes $value from every opaque reference envelope (an
// object carrying "$visibility":"Opaque" together with either "$ref" or
// "$gen") in raw, returning a copy. The reference pointer is preserved; the
// resolved/authored secret value is never persisted. It then validates that no
// opaque envelope still carries $value and errors if one does (a shape it
// could not normalize). Input is not mutated.
//
// $gen is included alongside $ref because it is always opaque by construction
// (the schema fixes $visibility to "Opaque" on every $gen), so a generator's
// resolved value is exactly as sensitive as an opaque $ref's and must never
// reach storage in cleartext.
func StripOpaqueRefValues(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, nil // not JSON we manage; leave as-is
	}
	stripped := stripOpaqueRefValues(v)
	if err := assertNoOpaqueRefValue(stripped, ""); err != nil {
		return nil, err
	}
	return json.Marshal(stripped)
}

func stripOpaqueRefValues(v any) any {
	switch t := v.(type) {
	case map[string]any:
		_, hasRef := t["$ref"]
		_, hasGen := t["$gen"]
		opaqueRef := (hasRef || hasGen) && t["$visibility"] == "Opaque"
		out := make(map[string]any, len(t))
		for k, val := range t {
			if opaqueRef && k == "$value" {
				continue // drop the resolved value; keep the reference
			}
			out[k] = stripOpaqueRefValues(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = stripOpaqueRefValues(val)
		}
		return out
	default:
		return v
	}
}

func assertNoOpaqueRefValue(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		_, hasRef := t["$ref"]
		_, hasGen := t["$gen"]
		if (hasRef || hasGen) && t["$visibility"] == "Opaque" {
			if _, has := t["$value"]; has {
				return fmt.Errorf("opaque reference at %q still carries $value after normalization", path)
			}
		}
		for k, val := range t {
			if err := assertNoOpaqueRefValue(val, path+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		for i, val := range t {
			if err := assertNoOpaqueRefValue(val, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
