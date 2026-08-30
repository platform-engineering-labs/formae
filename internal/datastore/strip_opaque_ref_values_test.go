//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStripOpaqueRefValues_StripsNestedOpaqueRefValue verifies that $value is
// removed from an opaque $ref envelope nested inside a larger JSON object.
func TestStripOpaqueRefValues_StripsNestedOpaqueRefValue(t *testing.T) {
	input := json.RawMessage(`{
		"name": "my-secret",
		"credentials": {
			"$ref": "secrets://my-org/prod/db-password",
			"$visibility": "Opaque",
			"$value": "super-secret-plaintext"
		}
	}`)
	// Keep a copy of the original to assert input not mutated.
	original := make(json.RawMessage, len(input))
	copy(original, input)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	// Input must not have been mutated.
	assert.Equal(t, string(original), string(input))

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	creds, ok := result["credentials"].(map[string]any)
	require.True(t, ok, "credentials should be a map")

	assert.Equal(t, "secrets://my-org/prod/db-password", creds["$ref"], "$ref must be preserved")
	assert.Equal(t, "Opaque", creds["$visibility"], "$visibility must be preserved")
	_, hasValue := creds["$value"]
	assert.False(t, hasValue, "$value must be stripped")

	// The plaintext must not appear anywhere in the output.
	assert.NotContains(t, string(out), "super-secret-plaintext")
}

// TestStripOpaqueRefValues_KeepsRefVisibilityStrategy verifies that $ref,
// $visibility, and $strategy are all preserved after stripping $value.
func TestStripOpaqueRefValues_KeepsRefVisibilityStrategy(t *testing.T) {
	input := json.RawMessage(`{
		"token": {
			"$ref": "secrets://vault/token",
			"$visibility": "Opaque",
			"$strategy": "inline",
			"$value": "tok-abc123"
		}
	}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	token := result["token"].(map[string]any)
	assert.Equal(t, "secrets://vault/token", token["$ref"])
	assert.Equal(t, "Opaque", token["$visibility"])
	assert.Equal(t, "inline", token["$strategy"])
	_, hasValue := token["$value"]
	assert.False(t, hasValue, "$value must be stripped")
}

// TestStripOpaqueRefValues_LeavesNonOpaqueUntouched verifies that an object
// with $ref but without $visibility:"Opaque" is left entirely unchanged.
func TestStripOpaqueRefValues_LeavesNonOpaqueUntouched(t *testing.T) {
	input := json.RawMessage(`{
		"ptr": {
			"$ref": "other://some/path",
			"$visibility": "Plaintext",
			"$value": "visible-value"
		}
	}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	ptr := result["ptr"].(map[string]any)
	assert.Equal(t, "visible-value", ptr["$value"], "$value must remain for non-opaque ref")
}

// TestStripOpaqueRefValues_LeavesPlainRefUntouched verifies that a plain $ref
// object (no $visibility key at all) is left unchanged.
func TestStripOpaqueRefValues_LeavesPlainRefUntouched(t *testing.T) {
	input := json.RawMessage(`{"link": {"$ref": "resource://some/thing"}}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	link := result["link"].(map[string]any)
	assert.Equal(t, "resource://some/thing", link["$ref"])
}

// TestStripOpaqueRefValues_LeavesInlineOpaqueWithoutRefUntouched verifies that
// an object with $visibility:"Opaque" but no $ref is not modified.
func TestStripOpaqueRefValues_LeavesInlineOpaqueWithoutRefUntouched(t *testing.T) {
	input := json.RawMessage(`{
		"secret": {
			"$visibility": "Opaque",
			"$value": "inline-secret"
		}
	}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	secret := result["secret"].(map[string]any)
	assert.Equal(t, "inline-secret", secret["$value"], "$value must be kept when $ref is absent")
}

// TestStripOpaqueRefValues_InputNotMutated verifies that the original
// json.RawMessage bytes are unchanged after the call.
func TestStripOpaqueRefValues_InputNotMutated(t *testing.T) {
	input := json.RawMessage(`{"pw": {"$ref": "s://x", "$visibility": "Opaque", "$value": "sekret"}}`)
	original := string(input)

	_, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	assert.Equal(t, original, string(input), "input slice must not be mutated")
}

// TestStripOpaqueRefValues_PlaintextGoneFromOutput verifies that after
// stripping, the resolved secret string does not appear in the JSON output.
func TestStripOpaqueRefValues_PlaintextGoneFromOutput(t *testing.T) {
	input := json.RawMessage(`{
		"a": {"$ref": "s://a", "$visibility": "Opaque", "$value": "plaintext-secret-a"},
		"b": {"$ref": "s://b", "$visibility": "Opaque", "$value": "plaintext-secret-b"}
	}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	assert.NotContains(t, string(out), "plaintext-secret-a")
	assert.NotContains(t, string(out), "plaintext-secret-b")
}

// TestStripOpaqueRefValues_StripsInsideArray verifies that stripping works on
// opaque $ref envelopes that appear inside a JSON array.
func TestStripOpaqueRefValues_StripsInsideArray(t *testing.T) {
	input := json.RawMessage(`[
		{"$ref": "s://x", "$visibility": "Opaque", "$value": "hidden"},
		{"$ref": "s://y", "$visibility": "Opaque", "$value": "also-hidden"}
	]`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "hidden")
}

// TestStripOpaqueRefValues_StripsNullRefOpaqueValue verifies that an envelope
// whose $ref key is present but JSON null, together with $visibility:"Opaque",
// is still treated as an opaque $ref envelope and has its $value stripped. The
// envelope is defined by the presence of the $ref key, not by its value being
// non-null.
func TestStripOpaqueRefValues_StripsNullRefOpaqueValue(t *testing.T) {
	input := json.RawMessage(`{
		"credentials": {
			"$ref": null,
			"$visibility": "Opaque",
			"$value": "super-secret-plaintext"
		}
	}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	creds := result["credentials"].(map[string]any)
	_, hasValue := creds["$value"]
	assert.False(t, hasValue, "$value must be stripped even when $ref is null")
	assert.NotContains(t, string(out), "super-secret-plaintext")
}

// TestAssertNoOpaqueRefValue_RejectsNullRefWithValue verifies the reject-guard
// treats a present-but-null $ref key together with $visibility:"Opaque" as an
// opaque $ref envelope, so a surviving $value is rejected rather than persisted.
func TestAssertNoOpaqueRefValue_RejectsNullRefWithValue(t *testing.T) {
	envelope := map[string]any{
		"$ref":        nil,
		"$visibility": "Opaque",
		"$value":      "super-secret",
	}

	err := assertNoOpaqueRefValue(envelope, "")
	require.Error(t, err, "guard must reject a null-$ref opaque envelope that still carries $value")
	assert.NotContains(t, err.Error(), "super-secret", "error must not leak the secret value")
}

// TestStripOpaqueRefValues_EmptyInput verifies that an empty RawMessage is
// returned as-is without error.
func TestStripOpaqueRefValues_EmptyInput(t *testing.T) {
	out, err := StripOpaqueRefValues(json.RawMessage{})
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestAssertNoOpaqueRefValue_RejectsOpaqueRefWithValue verifies the reject-guard
// directly: an opaque $ref envelope that still carries $value (the shape the
// strip must reject) must produce an error, and that error must not contain
// the plaintext secret value.
func TestAssertNoOpaqueRefValue_RejectsOpaqueRefWithValue(t *testing.T) {
	// Construct that shape directly: an opaque $ref that still carries $value.
	envelope := map[string]any{
		"$ref":        "formae://x",
		"$visibility": "Opaque",
		"$value":      "super-secret",
	}

	err := assertNoOpaqueRefValue(envelope, "")
	require.Error(t, err, "guard must reject an opaque $ref that still carries $value")
	assert.NotContains(t, err.Error(), "super-secret", "error must not leak the secret value")
}

// TestAssertNoOpaqueRefValue_AcceptsStrippedOpaqueRef verifies that the guard
// returns nil when an opaque $ref envelope has had $value correctly removed.
func TestAssertNoOpaqueRefValue_AcceptsStrippedOpaqueRef(t *testing.T) {
	envelope := map[string]any{
		"$ref":        "formae://x",
		"$visibility": "Opaque",
		// no $value — correctly stripped
	}

	err := assertNoOpaqueRefValue(envelope, "")
	require.NoError(t, err, "guard must accept a properly-stripped opaque $ref")
}

// A generated value resolved onto a destination must never reach at-rest
// storage. A $gen envelope is opaque by construction exactly like an opaque
// $ref, so its resolved $value must be stripped identically: preserved
// reference metadata ($gen, $generator, $output), $value gone from the
// serialized bytes entirely.
func TestStripOpaqueRefValues_GenEnvelopeValueIsStripped(t *testing.T) {
	input := json.RawMessage(`{
		"password": {
			"$gen": true,
			"$generator": "2ABcDeFgHiJkLmNoPqRsTuVwXyZ",
			"$output": "value",
			"$visibility": "Opaque",
			"$value": "gen-cleartext-sentinel-9f3a"
		}
	}`)

	out, err := StripOpaqueRefValues(input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))

	pw, ok := result["password"].(map[string]any)
	require.True(t, ok, "password should be a map")

	assert.Equal(t, true, pw["$gen"], "$gen must be preserved")
	assert.Equal(t, "2ABcDeFgHiJkLmNoPqRsTuVwXyZ", pw["$generator"], "$generator must be preserved")
	assert.Equal(t, "Opaque", pw["$visibility"], "$visibility must be preserved")
	_, hasValue := pw["$value"]
	assert.False(t, hasValue, "$value must be stripped from an opaque $gen envelope")

	assert.NotContains(t, string(out), "gen-cleartext-sentinel-9f3a",
		"the generated secret must not appear anywhere in the serialized output")
}

// The fail-closed assertion must recognise an opaque $gen envelope exactly
// like an opaque $ref: a $gen that still carries $value after normalization
// is a shape the strip could not normalize and must be rejected, not
// silently persisted.
func TestStripOpaqueRefValues_GenEnvelopeValueTripsTheAssertion(t *testing.T) {
	envelope := map[string]any{
		"$gen":        true,
		"$generator":  "2ABcDeFgHiJkLmNoPqRsTuVwXyZ",
		"$output":     "value",
		"$visibility": "Opaque",
		"$value":      "gen-cleartext-sentinel-9f3a",
	}

	err := assertNoOpaqueRefValue(envelope, "")
	require.Error(t, err, "guard must reject an opaque $gen envelope that still carries $value")
	assert.NotContains(t, err.Error(), "gen-cleartext-sentinel-9f3a", "error must not leak the secret value")
}

// Stripping the resolved secret value from an opaque reference envelope must
// keep the provenance digest: the digest is the persisted record the next
// plan compares against.
func TestStripOpaqueRefValues_PreservesResolvedFrom(t *testing.T) {
	digest := "v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	in := json.RawMessage(`{"url":{"$ref":"formae://k#/S","$visibility":"Opaque","$value":"secret","$resolvedFrom":"` + digest + `"}}`)
	out, err := StripOpaqueRefValues(in)
	require.NoError(t, err)
	var m map[string]map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	_, hasValue := m["url"]["$value"]
	assert.False(t, hasValue)
	assert.Equal(t, digest, m["url"]["$resolvedFrom"])
}
