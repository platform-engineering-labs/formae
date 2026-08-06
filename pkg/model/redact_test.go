// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactOpaqueForLog_RedactsOpaqueValue(t *testing.T) {
	in := map[string]any{"$visibility": "Opaque", "$value": "super-secret"}
	out := RedactOpaqueForLog(in).(map[string]any)

	assert.Equal(t, RedactedForLog, out["$value"])
	assert.Equal(t, "super-secret", in["$value"], "input must not be mutated")
}

func TestRedactOpaqueForLog_RedactsOpaqueRefEnvelope(t *testing.T) {
	in := map[string]any{
		"$ref":        "formae://res",
		"$visibility": "Opaque",
		"$value":      "super-secret",
	}
	out := RedactOpaqueForLog(in).(map[string]any)

	assert.Equal(t, RedactedForLog, out["$value"])
	assert.Equal(t, "formae://res", out["$ref"], "$ref must be preserved")
}

func TestRedactOpaqueForLog_RedactsNestedAndInSlice(t *testing.T) {
	in := map[string]any{
		"config": map[string]any{
			"auth": map[string]any{"$visibility": "Opaque", "$value": "super-secret"},
		},
		"list": []any{
			map[string]any{"$visibility": "Opaque", "$value": "another-secret"},
		},
	}
	out := RedactOpaqueForLog(in).(map[string]any)

	auth := out["config"].(map[string]any)["auth"].(map[string]any)
	assert.Equal(t, RedactedForLog, auth["$value"], "nested opaque value must be redacted")
	elem := out["list"].([]any)[0].(map[string]any)
	assert.Equal(t, RedactedForLog, elem["$value"], "opaque value in slice must be redacted")
}

func TestRedactOpaqueForLog_PreservesClearValue(t *testing.T) {
	in := map[string]any{"$visibility": "Clear", "$value": "public"}
	out := RedactOpaqueForLog(in).(map[string]any)

	assert.Equal(t, "public", out["$value"], "clear value must be preserved")
}

func TestRedactOpaqueForLog_PassesThroughScalars(t *testing.T) {
	assert.Equal(t, "plain", RedactOpaqueForLog("plain"))
	nested := []any{"a", map[string]any{"k": "v"}}
	assert.Equal(t, nested, RedactOpaqueForLog(nested), "non-opaque structure must be unchanged")
}

func TestRedactOpaqueForLog_RedactsOpaqueInsideJSONRawMessage(t *testing.T) {
	raw := json.RawMessage(`{"auth":{"$visibility":"Opaque","$value":"super-secret"}}`)
	in := map[string]any{
		"payload": raw,
	}
	out := RedactOpaqueForLog(in).(map[string]any)

	// The payload must have been decoded and recursed into, producing a map.
	payload, ok := out["payload"].(map[string]any)
	require.Truef(t, ok, "expected payload to be map[string]any after redaction, got %T", out["payload"])
	auth, ok := payload["auth"].(map[string]any)
	require.Truef(t, ok, "expected auth to be map[string]any, got %T", payload["auth"])

	assert.Equal(t, RedactedForLog, auth["$value"])

	// Confirm plaintext does not appear when the result is serialized. Disable
	// HTML escaping so we can also check for the literal RedactedForLog string.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(out))
	result := buf.String()

	assert.NotContains(t, result, "super-secret", "plaintext secret must not leak into redacted output")
	assert.Contains(t, result, RedactedForLog)
}

func TestRedactOpaqueForLog_RedactsOpaqueInsideEmbedTemplate(t *testing.T) {
	// A secret interpolated into a string field is stored as an embed:
	// {$embed:true, $template:"...<RS base64(envelope) US>..."} where the framed
	// envelope carries the resolved plaintext in $value. The generic walk cannot
	// see it (the value lives base64-framed inside a string), so the redactor
	// must decode, redact, and re-frame each span.
	const secret = "super-secret-embedded"
	envelope := `{"$ref":"formae://res","$visibility":"Opaque","$value":"` + secret + `"}`
	tmpl := "https://user:" + FrameEnvelope(envelope) + "@example.com"
	in := map[string]any{
		"url": map[string]any{"$embed": true, "$template": tmpl},
	}

	out := RedactOpaqueForLog(in).(map[string]any)

	// The redacted template's span must no longer decode to the plaintext.
	redactedTmpl := out["url"].(map[string]any)["$template"].(string)
	spans, err := ScanEmbedSpans(redactedTmpl)
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].EnvelopeJSON, RedactedForLog, "span $value must be redacted")
	assert.NotContains(t, spans[0].EnvelopeJSON, secret, "plaintext must not survive in the span")

	// No serialized form may leak, including the base64 frame of the plaintext
	// envelope.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(out))
	result := buf.String()
	assert.NotContains(t, result, secret, "plaintext must not leak")
	assert.NotContains(t, result, base64.StdEncoding.EncodeToString([]byte(envelope)),
		"the base64 frame carrying the plaintext must not survive")

	// Input must not be mutated.
	assert.Equal(t, tmpl, in["url"].(map[string]any)["$template"].(string), "input must not be mutated")
}

func TestRedactOpaqueForLog_PreservesClearEmbedSpan(t *testing.T) {
	// A non-opaque (Clear) embedded value must survive redaction unchanged.
	envelope := `{"$ref":"formae://res","$visibility":"Clear","$value":"public-part"}`
	tmpl := "prefix-" + FrameEnvelope(envelope) + "-suffix"
	in := map[string]any{"field": map[string]any{"$embed": true, "$template": tmpl}}

	out := RedactOpaqueForLog(in).(map[string]any)
	redactedTmpl := out["field"].(map[string]any)["$template"].(string)
	spans, err := ScanEmbedSpans(redactedTmpl)
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].EnvelopeJSON, "public-part", "clear embedded value must be preserved")
}

func TestRedactOpaqueForLog_UnparseableByteSlicePassedThrough(t *testing.T) {
	bad := []byte{0xff, 0xfe}
	in := map[string]any{"raw": bad}
	out := RedactOpaqueForLog(in).(map[string]any)

	got, ok := out["raw"].([]byte)
	require.Truef(t, ok, "expected []byte, got %T", out["raw"])
	assert.Equal(t, bad, got, "unparseable []byte must not be modified")
}
