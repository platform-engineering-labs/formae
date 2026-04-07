// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test structs ---

// v1Payload represents an initial version of a message.
type v1Payload struct {
	Name  string          `json:"Name"`
	Value int             `json:"Value"`
	Props json.RawMessage `json:"Props"`
}

// v2Payload adds an extra field compared to v1.
type v2Payload struct {
	Name    string          `json:"Name"`
	Value   int             `json:"Value"`
	Props   json.RawMessage `json:"Props"`
	NewFlag bool            `json:"NewFlag"`
}

func TestRoundTripWithRawMessage(t *testing.T) {
	original := v1Payload{
		Name:  "test-resource",
		Value: 42,
		Props: json.RawMessage(`{"key":"value","nested":{"a":1}}`),
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	var decoded v1Payload
	err = decodeMsgpack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	assert.JSONEq(t, string(original.Props), string(decoded.Props))
}

func TestForwardCompatibility_V2PayloadDecodedAsV1(t *testing.T) {
	// Encode a v2 payload (has NewFlag field)
	original := v2Payload{
		Name:    "forward-compat",
		Value:   99,
		Props:   json.RawMessage(`{"cloud":"aws"}`),
		NewFlag: true,
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	// Decode into v1 struct -- extra field NewFlag should be silently ignored
	var decoded v1Payload
	err = decodeMsgpack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	assert.JSONEq(t, string(original.Props), string(decoded.Props))
}

func TestBackwardCompatibility_V1PayloadDecodedAsV2(t *testing.T) {
	// Encode a v1 payload (missing NewFlag field)
	original := v1Payload{
		Name:  "backward-compat",
		Value: 7,
		Props: json.RawMessage(`{"region":"us-east-1"}`),
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	// Decode into v2 struct -- missing NewFlag should get zero value
	var decoded v2Payload
	err = decodeMsgpack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	assert.JSONEq(t, string(original.Props), string(decoded.Props))
	assert.False(t, decoded.NewFlag, "missing field should default to zero value")
}

func TestNonZeroDefaultsPreserved(t *testing.T) {
	// Encode a v1 payload (missing NewFlag)
	original := v1Payload{
		Name:  "defaults",
		Value: 1,
		Props: json.RawMessage(`{}`),
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	// Pre-populate the target with a non-zero default
	decoded := v2Payload{
		NewFlag: true, // set before decode
	}
	err = decodeMsgpack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	// NewFlag was not in the payload, so the pre-set value should be preserved
	assert.True(t, decoded.NewFlag, "pre-set non-zero default should be preserved when field is absent from payload")
}

func TestNilRawMessageHandling(t *testing.T) {
	original := v1Payload{
		Name:  "nil-props",
		Value: 0,
		Props: nil,
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	var decoded v1Payload
	err = decodeMsgpack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	assert.Nil(t, decoded.Props, "nil json.RawMessage should round-trip as nil")
}

func TestCompressionReducesSize(t *testing.T) {
	// Create a large payload with repetitive data that compresses well
	bigJSON := `{"` + strings.Repeat(`key`, 100) + `":"` + strings.Repeat(`value`, 200) + `"}`
	original := v1Payload{
		Name:  "compression-test",
		Value: 12345,
		Props: json.RawMessage(bigJSON),
	}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	// The raw msgpack (without zstd) would be roughly the size of the data.
	// With zstd compression, the encoded size should be significantly smaller
	// than the raw JSON input size.
	rawSize := len(bigJSON) + len("compression-test") + 8 // rough estimate of raw data
	encodedSize := len(encoded)

	assert.Less(t, encodedSize, rawSize,
		"compressed size (%d) should be less than raw data size (%d)", encodedSize, rawSize)
}

func TestMsgpackTagFallsBackToJSONTag(t *testing.T) {
	// Struct with only json tags (no msgpack tags) -- the library should use json tags
	type jsonTagOnly struct {
		FieldA string `json:"field_a"`
		FieldB int    `json:"field_b"`
	}

	// Struct with msgpack tags -- should use msgpack tag names
	type msgpackTagged struct {
		FieldA string `msgpack:"field_a"`
		FieldB int    `msgpack:"field_b"`
	}

	original := jsonTagOnly{FieldA: "hello", FieldB: 42}

	var buf bytes.Buffer
	err := encodeMsgpack(&buf, &original)
	encoded := buf.Bytes()
	require.NoError(t, err)

	// Decode into a struct with msgpack tags using the same field names --
	// this verifies the json tag names match msgpack tag names and are interoperable.
	var decoded msgpackTagged
	err = decodeMsgpack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.FieldA, decoded.FieldA)
	assert.Equal(t, original.FieldB, decoded.FieldB)
}
