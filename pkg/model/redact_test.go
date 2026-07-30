// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"reflect"
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
