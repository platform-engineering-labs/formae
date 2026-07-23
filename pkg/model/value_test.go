// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"encoding/json"
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
