// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "encoding/json"

// RedactedForLog is substituted for the plaintext of an opaque value when it is
// prepared for logging or other diagnostics.
const RedactedForLog = "<redacted>"

// RedactOpaqueForLog returns a deep copy of v that is safe to pass to slog or
// json.Marshal for diagnostics: the "$value" of every opaque envelope is replaced
// by RedactedForLog. An opaque envelope is a map carrying "$visibility": "Opaque"
// (with or without a "$ref"). Maps and slices are walked recursively; every other
// kind of data is returned unchanged. The input is never mutated.
// json.RawMessage and []byte values are attempted as JSON: if they parse
// successfully the decoded value is recursed into and the (redacted) decoded
// result is returned; unparseable byte slices are returned unchanged.
func RedactOpaqueForLog(v any) any {
	switch t := v.(type) {
	case map[string]any:
		opaque := t["$visibility"] == VisibilityOpaque
		out := make(map[string]any, len(t))
		for k, val := range t {
			if opaque && k == "$value" {
				out[k] = RedactedForLog
				continue
			}
			out[k] = RedactOpaqueForLog(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = RedactOpaqueForLog(val)
		}
		return out
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(t, &decoded); err != nil {
			return v
		}
		return RedactOpaqueForLog(decoded)
	case []byte:
		var decoded any
		if err := json.Unmarshal(t, &decoded); err != nil {
			return v
		}
		return RedactOpaqueForLog(decoded)
	default:
		return v
	}
}
