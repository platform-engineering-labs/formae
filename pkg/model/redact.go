// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"bytes"
	"encoding/json"
	"strings"
)

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
		// An embedded field ({"$embed": true, "$template": "...RS base64(env) US..."})
		// hides opaque values base64-framed inside the $template string, where the
		// generic walk below cannot reach them. Redact each span's $value in place.
		if t["$embed"] == true {
			if tmpl, ok := t["$template"].(string); ok {
				out := make(map[string]any, len(t))
				for k, val := range t {
					if k == "$template" {
						out[k] = redactEmbedTemplate(tmpl)
						continue
					}
					out[k] = RedactOpaqueForLog(val)
				}
				return out
			}
		}
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

// redactEmbedTemplate redacts opaque $value fields carried inside the framed
// spans of an $embed $template string, then re-frames each span so the template
// stays structurally intact. If the template cannot be scanned (corrupt framing),
// it fails closed: the whole template is replaced with RedactedForLog rather than
// risk leaking an unparseable payload.
func redactEmbedTemplate(tmpl string) string {
	spans, err := ScanEmbedSpans(tmpl)
	if err != nil {
		return RedactedForLog
	}
	out := tmpl
	// Splice back-to-front so earlier spans' byte offsets stay valid.
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		redacted := RedactOpaqueForLog(json.RawMessage(sp.EnvelopeJSON))
		reJSON, err := marshalNoEscape(redacted)
		if err != nil {
			// Should not happen for a span we just decoded; fail closed.
			reJSON = `"` + RedactedForLog + `"`
		}
		out = out[:sp.Start] + FrameEnvelope(reJSON) + out[sp.End:]
	}
	return out
}

// marshalNoEscape marshals v to JSON without escaping <, >, & so the redaction
// marker stays human-readable (RedactedForLog contains angle brackets).
func marshalNoEscape(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
