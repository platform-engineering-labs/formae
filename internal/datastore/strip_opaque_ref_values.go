// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"
)

// StripOpaqueRefValues removes $value from every opaque reference envelope in
// raw, returning a copy: a $ref carrying "$visibility":"Opaque", or a $gen
// unconditionally. The reference pointer is preserved; the resolved/authored
// secret value is never persisted. It then validates that no opaque envelope
// still carries $value and errors if one does (a shape it could not
// normalize). Input is not mutated.
//
// $gen never gates on $visibility. The schema fixes $visibility to "Opaque"
// on every $gen, so nothing legitimate loses a $value by treating every $gen
// as opaque outright — and unlike $ref, there is no non-opaque $gen shape to
// preserve by NOT stripping. This is defence in depth: PKL and translation
// already guarantee $visibility is present and "Opaque" on every $gen this
// process produces, but a $gen missing that field (a malformed or
// hand-constructed row) must still fail closed rather than persist in
// cleartext because the marker it happened to be checked against was absent.
func StripOpaqueRefValues(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, nil // not JSON we manage; leave as-is
	}
	stripped := stripOpaqueRefValues(v)
	if err := assertNoOpaqueEnvelopeValue(stripped, ""); err != nil {
		return nil, err
	}
	return json.Marshal(stripped)
}

func stripOpaqueRefValues(v any) any {
	switch t := v.(type) {
	case map[string]any:
		_, hasRef := t["$ref"]
		_, hasGen := t["$gen"]
		// $gen has no non-opaque shape: the schema fixes $visibility to
		// "Opaque" on every $gen, so a $gen is stripped unconditionally
		// rather than gated on a $visibility reading that could be absent or
		// wrong. $ref legitimately has a non-opaque form, so it still gates
		// on the declared $visibility.
		opaqueRef := hasGen || (hasRef && t["$visibility"] == "Opaque")
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

func assertNoOpaqueEnvelopeValue(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		_, hasRef := t["$ref"]
		_, hasGen := t["$gen"]
		if hasGen || (hasRef && t["$visibility"] == "Opaque") {
			if _, has := t["$value"]; has {
				return fmt.Errorf("opaque reference at %q still carries $value after normalization", path)
			}
		}
		for k, val := range t {
			if err := assertNoOpaqueEnvelopeValue(val, path+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		for i, val := range t {
			if err := assertNoOpaqueEnvelopeValue(val, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
