// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressDecompressJSON(t *testing.T) {
	original := json.RawMessage(`{"key":"value","nested":{"a":1}}`)
	compressed, err := CompressJSON(original)
	require.NoError(t, err)
	assert.True(t, len(compressed) > 0)
	assert.NotEqual(t, []byte(original), compressed)

	decompressed, err := DecompressJSON(compressed)
	require.NoError(t, err)
	assert.JSONEq(t, string(original), string(decompressed))
}

func TestCompressJSON_Nil(t *testing.T) {
	compressed, err := CompressJSON(nil)
	require.NoError(t, err)
	assert.Nil(t, compressed)
}

func TestDecompressJSON_Nil(t *testing.T) {
	decompressed, err := DecompressJSON(nil)
	require.NoError(t, err)
	assert.Nil(t, decompressed)
}

func TestDecompressJSON_Empty(t *testing.T) {
	decompressed, err := DecompressJSON([]byte{})
	require.NoError(t, err)
	assert.Nil(t, decompressed)
}
