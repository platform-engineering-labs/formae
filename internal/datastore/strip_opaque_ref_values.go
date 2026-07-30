// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"
)

// StripOpaqueRefValues removes $value from every opaque $ref envelope (an object
// carrying both "$ref" and "$visibility":"Opaque") in raw, returning a copy. The
// reference pointer is preserved; the resolved/authored secret value is never
// persisted. It then validates that no opaque $ref envelope still carries $value
// and errors if one does (a shape it could not normalize). Input is not mutated.
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
		opaqueRef := t["$ref"] != nil && t["$visibility"] == "Opaque"
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
		if t["$ref"] != nil && t["$visibility"] == "Opaque" {
			if _, has := t["$value"]; has {
				return fmt.Errorf("opaque $ref at %q still carries $value after normalization", path)
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
