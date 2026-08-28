// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValue_HashSetsHashedFlagAndIsIdempotent(t *testing.T) {
	v := &Value{Value: "super-secret", Visibility: VisibilityOpaque, Strategy: StrategyUpdate}

	h := v.Hash()
	require.True(t, h.Hashed, "Hash() must set Hashed=true")
	require.Len(t, h.Value.(string), 64)
	require.Equal(t, VisibilityOpaque, h.Visibility)
	require.Equal(t, StrategyUpdate, h.Strategy)

	// Idempotent: hashing an already-hashed value returns it unchanged.
	again := h.Hash()
	require.Equal(t, h.Value, again.Value, "re-hashing must not double-hash")
	require.True(t, again.Hashed)
}

func TestValue_HashedMarshalsDollarHashed(t *testing.T) {
	h := (&Value{Value: "s", Visibility: VisibilityOpaque}).Hash()
	b, err := json.Marshal(h)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"$hashed":true`)
}

func TestValue_LogValueRedactsOpaque(t *testing.T) {
	v := &Value{Value: "super-secret", Visibility: VisibilityOpaque}
	assert.Equal(t, "<redacted>", v.LogValue().String())

	clear := &Value{Value: "public", Visibility: VisibilityClear}
	assert.Equal(t, "public", clear.LogValue().String())
}

func TestValue_JSONPathRoundTrip(t *testing.T) {
	v := Value{Visibility: "Opaque", JSONPath: "db.password"}
	b, err := json.Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"$json":"db.password"`) {
		t.Fatalf("missing $json in %s", b)
	}
	var back Value
	if err := json.Unmarshal(b, &back); err != nil || back.JSONPath != "db.password" {
		t.Fatalf("round-trip failed: %+v err %v", back, err)
	}
}

// A Value carrying resolution provenance survives the JSON round trip, and
// Hash() preserves both provenance and the extraction path alongside the
// digested value.
func TestValue_ResolvedFromRoundTripAndHash(t *testing.T) {
	v := &Value{
		Strategy:     StrategyUpdate,
		Visibility:   VisibilityOpaque,
		Value:        "plain",
		JSONPath:     "password",
		ResolvedFrom: "v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	data, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"$resolvedFrom"`)
	var back Value
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, v.ResolvedFrom, back.ResolvedFrom)

	hashed := v.Hash()
	assert.True(t, hashed.Hashed)
	assert.Equal(t, v.ResolvedFrom, hashed.ResolvedFrom, "hashing must not drop provenance")
	assert.Equal(t, v.JSONPath, hashed.JSONPath, "hashing must not drop the extraction path")
}
