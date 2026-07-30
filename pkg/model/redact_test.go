// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRedactOpaqueForLog_RedactsOpaqueValue(t *testing.T) {
	in := map[string]any{"$visibility": "Opaque", "$value": "super-secret"}
	out := RedactOpaqueForLog(in).(map[string]any)

	if out["$value"] != RedactedForLog {
		t.Fatalf("expected $value = %q, got %v", RedactedForLog, out["$value"])
	}
	if in["$value"] != "super-secret" {
		t.Fatalf("input was mutated: $value = %v", in["$value"])
	}

	// Positive assertion: output $value must equal RedactedForLog exactly.
	if got, want := out["$value"], any(RedactedForLog); got != want {
		t.Fatalf("expected $value == %q, got %v", want, got)
	}
}

func TestRedactOpaqueForLog_RedactsOpaqueRefEnvelope(t *testing.T) {
	in := map[string]any{
		"$ref":        "formae://res",
		"$visibility": "Opaque",
		"$value":      "super-secret",
	}
	out := RedactOpaqueForLog(in).(map[string]any)

	if out["$value"] != RedactedForLog {
		t.Fatalf("expected $value redacted, got %v", out["$value"])
	}
	if out["$ref"] != "formae://res" {
		t.Fatalf("expected $ref preserved, got %v", out["$ref"])
	}
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
	if auth["$value"] != RedactedForLog {
		t.Fatalf("nested opaque value not redacted: %v", auth["$value"])
	}
	elem := out["list"].([]any)[0].(map[string]any)
	if elem["$value"] != RedactedForLog {
		t.Fatalf("opaque value in slice not redacted: %v", elem["$value"])
	}
}

func TestRedactOpaqueForLog_PreservesClearValue(t *testing.T) {
	in := map[string]any{"$visibility": "Clear", "$value": "public"}
	out := RedactOpaqueForLog(in).(map[string]any)

	if out["$value"] != "public" {
		t.Fatalf("clear value must be preserved, got %v", out["$value"])
	}
}

func TestRedactOpaqueForLog_PassesThroughScalars(t *testing.T) {
	if got := RedactOpaqueForLog("plain"); got != "plain" {
		t.Fatalf("scalar changed: %v", got)
	}
	nested := []any{"a", map[string]any{"k": "v"}}
	if got := RedactOpaqueForLog(nested); !reflect.DeepEqual(got, nested) {
		t.Fatalf("non-opaque structure changed: %v", got)
	}
}

func TestRedactOpaqueForLog_RedactsOpaqueInsideJSONRawMessage(t *testing.T) {
	raw := json.RawMessage(`{"auth":{"$visibility":"Opaque","$value":"super-secret"}}`)
	in := map[string]any{
		"payload": raw,
	}
	out := RedactOpaqueForLog(in).(map[string]any)

	// The payload must have been decoded and recursed into, producing a map.
	payload, ok := out["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload to be map[string]any after redaction, got %T", out["payload"])
	}
	auth, ok := payload["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth to be map[string]any, got %T", payload["auth"])
	}

	// Positive assertion: $value must equal RedactedForLog.
	if got := auth["$value"]; got != RedactedForLog {
		t.Fatalf("expected $value = %q, got %v", RedactedForLog, got)
	}

	// Confirm plaintext does not appear when the result is serialized.
	// Use a bytes.Buffer + json.Encoder with HTML escaping disabled so we can
	// also check for the literal RedactedForLog string.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		t.Fatalf("failed to encode result: %v", err)
	}
	result := buf.String()

	if strings.Contains(result, "super-secret") {
		t.Fatalf("plaintext secret leaked into redacted output: %s", result)
	}
	if !strings.Contains(result, RedactedForLog) {
		t.Fatalf("expected %q in redacted output, got: %s", RedactedForLog, result)
	}
}

func TestRedactOpaqueForLog_UnparseableByteSlicePassedThrough(t *testing.T) {
	bad := []byte{0xff, 0xfe}
	in := map[string]any{"raw": bad}
	out := RedactOpaqueForLog(in).(map[string]any)

	got, ok := out["raw"].([]byte)
	if !ok {
		t.Fatalf("expected []byte, got %T", out["raw"])
	}
	if !reflect.DeepEqual(got, bad) {
		t.Fatalf("unparseable []byte was modified: %v", got)
	}
}
